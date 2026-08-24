// Package auth 管「谁能进来」：一次性配对码 + 每台设备一份长期凭据。
//
// 设计目标只有一句：**一台设备配一次，之后再也不用管**。所以：
//
//   - 长期凭据绑**设备**（cookie 里的随机令牌），不绑 IP。IP 会被 DHCP 复用（客人拿到
//     你上周批准过的地址就等于拿到你的 shell），也会自己变（换 Wi-Fi、租约到期），绑了
//     就是「既不安全又要反复配对」两头都输。同理不绑 UA —— 系统一升级就得重配。
//   - 落盘只存 sha256。这台机器上跑的 agent 天天读不可信内容，凭据文件被 prompt
//     injection 读走是日常风险，不是理论风险。
//   - 配对码只在内存里、5 分钟过期、用一次就废。所以磁盘上不存在任何能直接登录的明文。
//
// 轮换和重用检测（docs/dev/SECURITY.md 的 L4）还没做，Hash 现在是一个而不是一串。
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CookieName 前端读不到（HttpOnly），只有服务端用。
const CookieName = "hw_dev"

// CodeTTL：配对码的有效期。短到「拍走截图也没用」，长到够你走回沙发上扫码。
const CodeTTL = 5 * time.Minute

// 配对码的字母表去掉了 0/1/I/L/O —— 这个码是要在终端里念给自己、在手机上手输的。
const codeAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

const codeLen = 8

// flushEvery：LastSeen 变化不值得每个请求写一次盘（发件箱 500ms 一拍）。
const flushEvery = 30 * time.Second

type Device struct {
	ID       string    `json:"id"`
	Label    string    `json:"label"`
	Hash     string    `json:"hash"` // sha256(令牌明文)，明文只在签发那一刻存在
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"lastSeen"`
	LastIP   string    `json:"lastIp"`
	Expires  time.Time `json:"expires"` // 零值 = 永不过期
	// VerifiedAt 是上一次「人证明了自己在场」的时刻：配对成功，或者过了一次 passkey。
	// 注册过 passkey 之后，服务端会要求这个时间足够新，否则要求重新验一次 ——
	// 这是把「cookie 被偷」的可用窗口从整个 TTL 压到一天的那个机制。
	VerifiedAt time.Time `json:"verifiedAt"`
	// Parent 是「把我带出来的那份凭据」的设备 ID（局域网直连交接出来的那种，见
	// MintHandoff）。空 = 自己配的对，没有上级。
	//
	// 这个字段的**唯一**用途是让撤销级联：docs/dev/SECURITY.md §11 不许网页上出配对码，理由是
	// 「配对码创造的是一份不随创造者一起被撤销的凭据」—— 手机被拿走一次就能留下一台
	// 踢不掉的设备。交接这条路要成立，就必须让它随上级一起死，否则那条理由原样成立。
	Parent string `json:"parent,omitempty"`
}

func (d Device) Expired(now time.Time) bool {
	return !d.Expires.IsZero() && now.After(d.Expires)
}

type Config struct {
	Dir string
	// LegacyFile：分层之前 devices.json 的位置。新位置没有、这儿有，就搬过去。
	// 显式传路径而不是自己去猜 ../，是为了这段逻辑能被测到。
	LegacyFile string
	// TTL 是滑动过期（每次用都续期）。0 = 永不过期。
	TTL time.Duration
	// Secure 决定 cookie 要不要 Secure 属性。**http 下必须是 false**，否则浏览器压根
	// 不发这个 cookie，表现是「一直跳回配对页」而不是报错。
	Secure bool
	// LegacyToken："on" | "loopback" | "off"，见 docs/dev/SECURITY.md 的迁移路径。
	LegacyToken string
	Token       string
	// TrustLoopback：从 127.0.0.1 连上来的直接放行。**默认关，套 frp / 反代时必须关**
	// —— 那时候 RemoteAddr 就是本机，等于公网上谁来都算「本机」。
	TrustLoopback bool
	// TrustProxy：信任 X-Forwarded-For 里的客户端 IP。没有可信前置时绝不能开，
	// 否则攻击者自带一个 XFF 就能伪造源 IP。
	TrustProxy bool
}

type Store struct {
	cfg Config

	mu    sync.Mutex
	devs  []*Device
	codes map[string]time.Time // 配对码 → 过期时刻
	// handoffs 局域网直连的交接令牌 → 谁交接的 + 什么时候过期。和配对码分开是**故意**的：
	// 配对码那条路「只有坐在机器前的人能出」是一条写在 docs/dev/SECURITY.md 里的性质，网页上
	// 不能有任何出码的路径。交接令牌是另一种东西 —— 短命、只能在直连那个口上兑换、
	// 兑出来的设备随上级一起被撤销，见 MintHandoff。
	handoffs map[string]handoff
	dirty    bool
	flushed  time.Time
	guard    tamperGuard

	now func() time.Time // 测试用
}

func New(cfg Config) (*Store, error) {
	s := &Store{cfg: cfg, codes: map[string]time.Time{}, handoffs: map[string]handoff{}, now: time.Now}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

/* ------------------------------------------------------------------ 落盘 */

func (s *Store) file() string { return filepath.Join(s.cfg.Dir, "devices.json") }

func (s *Store) load() error {
	s.migrate()
	b, err := os.ReadFile(s.file())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var w struct{ Devices []*Device }
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	now := s.now()
	for _, d := range w.Devices {
		if !d.Expired(now) {
			s.devs = append(s.devs, d)
		}
	}
	s.guard.note(s.file())
	return nil
}

// migrate 把老位置的文件搬到新位置。只在新位置确实没有时动手，所以重复跑没事。
func (s *Store) migrate() {
	if s.cfg.LegacyFile == "" {
		return
	}
	if _, err := os.Stat(s.file()); err == nil {
		return // 新位置已经有了，别覆盖
	}
	if _, err := os.Stat(s.cfg.LegacyFile); err != nil {
		return
	}
	if os.MkdirAll(s.cfg.Dir, 0o700) != nil {
		return
	}
	_ = os.Rename(s.cfg.LegacyFile, s.file())
}

func (s *Store) flushLocked() {
	s.dirty = false
	s.flushed = s.now()
	if err := os.MkdirAll(s.cfg.Dir, 0o700); err != nil {
		return
	}
	b, err := json.MarshalIndent(struct{ Devices []*Device }{s.devs}, "", "  ")
	if err != nil {
		return
	}
	// 先写临时文件再 rename：崩在中间也不会留下一个解不开的 devices.json
	// （解不开就等于所有设备一起掉线）
	tmp := s.file() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, s.file())
	}
	s.guard.note(s.file())
}

// Flush 把攒着的 LastSeen 落盘。
func (s *Store) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirty {
		s.flushLocked()
	}
}

/* ------------------------------------------------------------------ 配对码 */

// MintCode 出一个一次性配对码。旧的不作废 —— 连着敲两次 pair 时，先扫哪个都能用。
func (s *Store) MintCode() (string, time.Time) {
	code := randCode()
	exp := s.now().Add(CodeTTL)
	s.mu.Lock()
	defer s.mu.Unlock()
	for c, e := range s.codes {
		if s.now().After(e) {
			delete(s.codes, c)
		}
	}
	s.codes[code] = exp
	return code, exp
}

/* ------------------------------------------------- 局域网直连的交接令牌 */

// HandoffTTL 交接令牌的寿命。只够「拿到它 → 立刻跳过去落地」这一下，所以给 60 秒
// （配对码是 5 分钟，那是给人走回沙发上扫码用的；这条路上没有人参与）。
const HandoffTTL = 60 * time.Second

type handoff struct {
	parent string // 谁交接的（设备 ID）——兑出来的新设备记着它，撤销时级联
	exp    time.Time
}

var ErrBadHandoff = errors.New("交接令牌不对、过期了，或者已经用过")

// MintHandoff 出一枚**局域网直连专用**的交接令牌。
//
// 为什么不能复用配对码（这是这块最容易改错的地方）：docs/dev/SECURITY.md §11 明确写了「网页上
// 不出配对码」，理由是配对码创造的是一份**不随创造者一起被撤销**的独立凭据 —— 一份被偷
// 的 cookie 就成了无限发凭据的机器，`revoke` 变成打地鼠。所以交接令牌在三处都比它窄：
//
//  1. **只能在直连那个监听上兑换**（不是「Host 看起来像内网 IP」—— Host 是客户端说的，
//     公网那条路伪造一个就绕过去了；判据是请求落在哪个监听上，见 server.FromLan）；
//  2. 60 秒、一次性；
//  3. 兑出来的设备记着 parent，**撤销上级时一起被撤销**（见 Revoke）——§11 那条理由到这儿
//     就不成立了。
//
// 令牌是 256 位随机，猜不出来，所以不进限速那一层（那一层只管短凭据的猜解）。
func (s *Store) MintHandoff(parent string) (string, time.Time) {
	tok := randToken()
	exp := s.now().Add(HandoffTTL)
	s.mu.Lock()
	defer s.mu.Unlock()
	for t, h := range s.handoffs {
		if s.now().After(h.exp) {
			delete(s.handoffs, t)
		}
	}
	s.handoffs[tok] = handoff{parent: parent, exp: exp}
	return tok, exp
}

// RedeemHandoff 用交接令牌换直连那一侧的设备凭据。
//
// **调用方必须先确认请求真的落在直连那个监听上** —— 这个函数不管那件事（它拿不到
// 监听信息），门卫在 server 那一侧，见 handleRoot 里 `?handoff=` 那一段。
func (s *Store) RedeemHandoff(tok, ua, ip string) (*Device, string, error) {
	if tok == "" {
		return nil, "", ErrBadHandoff
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var hit string
	var got handoff
	now := s.now()
	for t, h := range s.handoffs {
		if subtle.ConstantTimeCompare([]byte(t), []byte(tok)) == 1 && now.Before(h.exp) {
			hit, got = t, h
		}
	}
	if hit == "" {
		return nil, "", ErrBadHandoff
	}
	delete(s.handoffs, hit) // 一次性

	token := randToken()
	d := &Device{
		ID:       randID(),
		Label:    LabelFromUA(ua) + "（局域网）",
		Hash:     hashToken(token),
		Created:  now,
		LastSeen: now,
		LastIP:   ip,
		// 上级刚过了 requireAuth（注册过 passkey 的话连重验也过了），所以这一份的
		// 「在场证明」是从它那儿继承来的，不是凭空给的。
		VerifiedAt: now,
		Parent:     got.parent,
	}
	if s.cfg.TTL > 0 {
		d.Expires = now.Add(s.cfg.TTL)
	}
	s.devs = append(s.devs, d)
	s.flushLocked()
	return d, token, nil
}

var ErrBadCode = errors.New("配对码不对或者已经过期了")

// Redeem 用配对码换一台设备的长期凭据。返回的令牌明文只在这一次出现，之后只有哈希。
func (s *Store) Redeem(code, ua, ip string) (*Device, string, error) {
	code = normalizeCode(code)
	if code == "" {
		return nil, "", ErrBadCode
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// 遍历比对而不是直接查 map：码只有 40 位，别把「猜对了几位」这种信息泄在时间里
	var hit string
	now := s.now()
	for c, exp := range s.codes {
		if subtle.ConstantTimeCompare([]byte(c), []byte(code)) == 1 && now.Before(exp) {
			hit = c
		}
	}
	if hit == "" {
		return nil, "", ErrBadCode
	}
	delete(s.codes, hit) // 用一次就废

	token := randToken()
	d := &Device{
		ID:         randID(),
		Label:      LabelFromUA(ua),
		Hash:       hashToken(token),
		Created:    now,
		LastSeen:   now,
		LastIP:     ip,
		VerifiedAt: now, // 配对码本身就是一次「你在机器前」的证明
	}
	if s.cfg.TTL > 0 {
		d.Expires = now.Add(s.cfg.TTL)
	}
	s.devs = append(s.devs, d)
	s.flushLocked()
	return d, token, nil
}

// NewDevice 不走配对码直接签一台设备。只给两个地方用：旧 `?token=` 的一次性迁移，
// 和命令行显式加白。别拿它当认证路径。
func (s *Store) NewDevice(ua, ip, note string) (*Device, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	token := randToken()
	label := LabelFromUA(ua)
	if note != "" {
		label += "（" + note + "）"
	}
	d := &Device{ID: randID(), Label: label, Hash: hashToken(token), Created: now, LastSeen: now, LastIP: ip, VerifiedAt: now}
	if s.cfg.TTL > 0 {
		d.Expires = now.Add(s.cfg.TTL)
	}
	s.devs = append(s.devs, d)
	s.flushLocked()
	return d, token
}

/* ------------------------------------------------------------------ 设备 */

func (s *Store) Devices() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Device, 0, len(s.devs))
	for _, d := range s.devs {
		out = append(out, *d)
	}
	return out
}

// Revoke 支持 ID 前缀，命令行上不用抄全。
// Revoke 撤销一台设备，**连它交接出去的那些一起**。
//
// 级联不是顺手做的方便：局域网直连交接出来的设备（Parent 指着这一台）如果能留下来，
// 那 docs/dev/SECURITY.md §11 反对「网页上出码」的理由就原样成立了 —— 手机被拿走一次，就留下
// 一台你踢不掉的设备。所以「踢掉手机」必须意味着「它带出来的那些也一起没」。
func (s *Store) Revoke(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range s.devs {
		if d.ID == id || (len(id) >= 4 && strings.HasPrefix(d.ID, id)) {
			label, gone := d.Label, d.ID
			s.devs = append(s.devs[:i], s.devs[i+1:]...)
			// 再扫一遍把它的孩子摘掉。一层就够：交接出来的那份自己不能再交接
			// （它没有 passkey，也进不了 /api/handoff 那道门 —— 见 apiHandoff）。
			kept := s.devs[:0]
			for _, x := range s.devs {
				if x.Parent != gone {
					kept = append(kept, x)
				}
			}
			s.devs = kept
			s.flushLocked()
			return label, true
		}
	}
	return "", false
}

func (s *Store) RevokeAll() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.devs)
	s.devs = nil
	s.flushLocked()
	return n
}

/* ------------------------------------------------------------------ 认证 */

// Ident 是「这个请求是谁」。Ambient 表示凭据是浏览器自动带上的（cookie / 源 IP），
// 那种请求才需要防 CSRF；`?token=` 那种显式凭据外站猜不到，不需要。
type Ident struct {
	Kind    string // "device" | "legacy" | "loopback"
	Label   string
	Device  *Device
	Ambient bool
	// VerifiedAt 透出来给上层判断「要不要重新验一次」。判断放在 server 那边做，
	// 因为「有没有注册过 passkey」是它才知道的事。
	VerifiedAt time.Time
}

func (s *Store) Authenticate(r *http.Request) *Ident {
	ip := s.ClientIP(r)
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		if d := s.lookup(c.Value, ip); d != nil {
			return &Ident{Kind: "device", Label: d.Label, Device: d, Ambient: true, VerifiedAt: d.VerifiedAt}
		}
	}
	// 旧书签：只够换一次 cookie（handleRoot 里换），也允许直接调 /api（老脚本还能用）
	if tok := r.URL.Query().Get("token"); tok != "" && s.legacyOK(r) &&
		subtle.ConstantTimeCompare([]byte(tok), []byte(s.cfg.Token)) == 1 {
		return &Ident{Kind: "legacy", Label: "旧 token"}
	}
	if s.trustLoopback(r) {
		return &Ident{Kind: "loopback", Label: "本机", Ambient: true}
	}
	return nil
}

func (s *Store) lookup(token, ip string) *Device {
	h := hashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for _, d := range s.devs {
		if subtle.ConstantTimeCompare([]byte(d.Hash), []byte(h)) == 1 {
			if d.Expired(now) {
				return nil
			}
			d.LastSeen = now
			if ip != "" {
				d.LastIP = ip
			}
			if s.cfg.TTL > 0 {
				d.Expires = now.Add(s.cfg.TTL) // 滑动续期：天天用的设备永远不掉
			}
			s.dirty = true
			if now.Sub(s.flushed) > flushEvery {
				s.flushLocked()
			}
			return d
		}
	}
	return nil
}

func (s *Store) legacyOK(r *http.Request) bool {
	if s.cfg.Token == "" {
		return false
	}
	switch s.cfg.LegacyToken {
	case "off":
		return false
	case "loopback":
		// 只在本机有效：泄露给「已经能在你机器上跑代码的东西」不算泄露，它早就有 shell 了
		return remoteIsLoopback(r) && !behindProxy(r)
	default:
		return true
	}
}

// trustLoopback 三个条件全中才算「这是本机上的浏览器」：
//
//   - 明确打开了这个豁免（默认关）；
//   - 源地址是 loopback；
//   - **Host 头也是 loopback 字面量**。这条是给 frp / 反代兜底的：穿透进来的请求源地址
//     也是 127.0.0.1，但浏览器地址栏里是域名，所以 Host 会是那个域名。
//   - 没有 XFF（有前置的痕迹就一律不信）。
func (s *Store) trustLoopback(r *http.Request) bool {
	if !s.cfg.TrustLoopback || !remoteIsLoopback(r) || behindProxy(r) {
		return false
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func remoteIsLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func behindProxy(r *http.Request) bool {
	return r.Header.Get("X-Forwarded-For") != "" ||
		r.Header.Get("X-Real-Ip") != "" ||
		r.Header.Get("Forwarded") != ""
}

func (s *Store) ClientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" && s.cfg.TrustProxy {
		if i := strings.IndexByte(v, ','); i > 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// CheckTampered 看一眼凭据文件有没有被外部改过，被改过就调一次 alert。
func (s *Store) CheckTampered(alert func(string)) {
	s.guard.check("设备凭据文件", alert)
}

// MarkVerified 记一次刚做过的生物验证（passkey 断言成功）。
func (s *Store) MarkVerified(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.devs {
		if d.ID == id {
			d.VerifiedAt = s.now()
			s.flushLocked()
			return
		}
	}
}

/* ------------------------------------------------------------------ cookie */

func (s *Store) IssueCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:  CookieName,
		Value: token,
		Path:  "/",
		// SameSite=Strict：外站发起的请求一律带不上这个 cookie。代价是从别的 app 点链接
		// 进来时首次导航不带 cookie —— 但首屏本来就不需要认证（那只是个空壳 SPA），
		// 之后同源的 fetch 照样带。
		SameSite: http.SameSiteStrictMode,
		HttpOnly: true,
		Secure:   s.cfg.Secure,
		MaxAge:   int(s.cookieMaxAge().Seconds()),
	})
}

// cookieMaxAge：浏览器把 cookie 上限压到 400 天，所以「永不过期」在浏览器那边只能是
// 400 天 + 每次打开页面重新下发（whoami 那里会刷）。
func (s *Store) cookieMaxAge() time.Duration {
	if s.cfg.TTL <= 0 || s.cfg.TTL > 400*24*time.Hour {
		return 400 * 24 * time.Hour
	}
	return s.cfg.TTL
}

func (s *Store) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/",
		SameSite: http.SameSiteStrictMode, HttpOnly: true, Secure: s.cfg.Secure, MaxAge: -1,
	})
}

/* ------------------------------------------------------------------ 小工具 */

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func randToken() string {
	b := make([]byte, 32)
	must(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func randID() string {
	b := make([]byte, 6)
	must(b)
	return hex.EncodeToString(b)
}

// randCode 用拒绝采样，别让字母表长度不整除 256 引入偏斜。
func randCode() string {
	out := make([]byte, 0, codeLen)
	buf := make([]byte, 1)
	max := byte(len(codeAlphabet) * (256 / len(codeAlphabet)))
	for len(out) < codeLen {
		must(buf)
		if buf[0] >= max {
			continue
		}
		out = append(out, codeAlphabet[int(buf[0])%len(codeAlphabet)])
	}
	return string(out)
}

func must(b []byte) {
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand 读不出来：" + err.Error()) // 这台机器上没有随机数，别硬撑
	}
}

// normalizeCode 容忍手输的大小写、空格和连字符，别的一律当错。
//
// 不做 O→0 / I→1 这类「纠正」：字母表里 0 1 I L O 五个字符全都没有，所以看到它们只能
// 说明用户抄错了，猜哪个都是瞎猜。
func normalizeCode(in string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(in) {
		if r == ' ' || r == '-' || r == '_' {
			continue
		}
		if !strings.ContainsRune(codeAlphabet, r) {
			return ""
		}
		b.WriteRune(r)
	}
	if b.Len() != codeLen {
		return ""
	}
	return b.String()
}

// LabelFromUA 只求「在设备列表里能认出是哪台」，不求准。
func LabelFromUA(ua string) string {
	dev := "未知设备"
	switch {
	case strings.Contains(ua, "iPad"):
		dev = "iPad"
	case strings.Contains(ua, "iPhone"):
		dev = "iPhone"
	case strings.Contains(ua, "Android"):
		dev = "Android"
	case strings.Contains(ua, "Macintosh"), strings.Contains(ua, "Mac OS X"):
		dev = "Mac"
	case strings.Contains(ua, "Windows"):
		dev = "Windows"
	case strings.Contains(ua, "Linux"):
		dev = "Linux"
	}
	br := ""
	switch {
	case strings.Contains(ua, "Edg"):
		br = "Edge"
	case strings.Contains(ua, "CriOS"), strings.Contains(ua, "Chrome"):
		br = "Chrome"
	case strings.Contains(ua, "Firefox"), strings.Contains(ua, "FxiOS"):
		br = "Firefox"
	case strings.Contains(ua, "Safari"):
		br = "Safari"
	}
	if br == "" {
		return dev
	}
	return dev + " · " + br
}
