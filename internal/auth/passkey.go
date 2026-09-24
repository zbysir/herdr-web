package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// passkey：WebAuthn 凭据。
//
// 为什么是它而不是 TOTP（决定过程见 docs/dev/SECURITY.md 的 L2）：
//
//   - **服务端只存公钥。** TOTP 的共享密钥必须落盘，而这台机器上跑的 agent 天天读不可信
//     内容，凭据文件被读走是日常风险 —— 读走 TOTP 密钥就等于第二因子没了，而且你不会知道。
//     公钥被读走没有任何用。
//   - **用的时候要生物验证。** TOTP 的认证器 app 通常就在同一台手机上，手机没锁屏被拿走时
//     它加的那道和它要防的人在同一侧。
//   - **原地 Face ID，不用切 app。** 这不是舒适度问题：加第二因子的目的是把 cookie 的寿命
//     从三个月压到一天，用 TOTP 你两周之后一定会把它调回去。
//
// 两个硬前提：secure context，以及 **RPID 必须是域名** —— 裸 IP 不是合法 RPID
// （`localhost` 是规范里的特例）。所以局域网上想用 passkey 得让域名指向内网地址，
// 见 DEPLOY.md 的 C 档。
type Passkey struct {
	ID       string    `json:"id"`
	Label    string    `json:"label"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"lastUsed"`
	// Cred 里是公钥和计数器，没有任何秘密
	Cred webauthn.Credential `json:"cred"`
}

// Public 是给网页看的那份，去掉公钥细节。
type PasskeyInfo struct {
	ID       string    `json:"id"`
	Label    string    `json:"label"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"lastUsed"`
}

type PasskeyConfig struct {
	Dir string
	// LegacyFile：分层之前 passkeys.json 的位置，同 Config.LegacyFile
	LegacyFile string
	// RPID 是凭据绑定的域名。空字符串 = 这个部署用不了 passkey（裸 IP 访问）。
	RPID string
	// Origins 必须和浏览器实际发出的 Origin 完全一致，多写几个候选没关系。
	Origins []string
	Display string
}

type Passkeys struct {
	cfg PasskeyConfig
	w   *webauthn.WebAuthn

	mu     sync.Mutex
	handle []byte // 固定的 user handle：所有 passkey 属于同一个「用户」
	keys   []*Passkey
	sess   map[string]*pendingCeremony
	guard  tamperGuard

	now func() time.Time
}

type pendingCeremony struct {
	data *webauthn.SessionData
	exp  time.Time
}

// ceremonyTTL：从拿到 challenge 到提交断言之间的时限。Face ID 再慢也用不了两分钟。
const ceremonyTTL = 2 * time.Minute

func NewPasskeys(cfg PasskeyConfig) (*Passkeys, error) {
	p := &Passkeys{cfg: cfg, sess: map[string]*pendingCeremony{}, now: time.Now}
	if err := p.load(); err != nil {
		return nil, err
	}
	if cfg.RPID == "" {
		return p, nil // 用不了，但对象还在，Count() 返回 0
	}
	display := cfg.Display
	if display == "" {
		display = "herdr-web"
	}
	w, err := webauthn.New(&webauthn.Config{
		RPID: cfg.RPID, RPDisplayName: display, RPOrigins: cfg.Origins,
	})
	if err != nil {
		return nil, fmt.Errorf("passkey 配置不对（RPID=%q origins=%v）: %w", cfg.RPID, cfg.Origins, err)
	}
	p.w = w
	return p, nil
}

// CheckTampered 看一眼 passkey 文件有没有被外部改过。
func (p *Passkeys) CheckTampered(alert func(string)) {
	p.guard.check("passkey 文件", alert)
}

// Available：这个部署能不能用 passkey。
func (p *Passkeys) Available() bool { return p != nil && p.w != nil }

// UsableOn 在**这个 Host 上**能不能做 WebAuthn。
//
// 规范的判据：origin 的有效域必须**等于 RPID 或者是它的子域**。裸 IP 永远不行
// （`localhost` 是规范里的特例，靠下面的等值比较自然命中）。
//
// 为什么不能只用 Available()（「这个部署配了 RPID 没有」）：**同一个部署会同时有域名
// origin 和裸 IP origin** —— 开了局域网直连（HERDR_WEB_LAN_PORT）之后就是这样，公网那条
// 路是域名、直连那条是 IP。全局判断会在 IP 那一侧交给前端一个 `available: true`，于是页面
// 画出一个「用 passkey 登录」的按钮，而按下去只会抛 SecurityError —— 一个必然失败的按钮，
// 比没有按钮糟得多（人会以为是自己指纹没录好）。
//
// 这条**和证书无关**：把自签 CA 装到设备上、浏览器警告完全消失之后，裸 IP 上照样不行。
// 门槛是「标识必须是域名」，不是「连接够不够可信」。
func UsableOn(rpid, host string) bool {
	if rpid == "" {
		return false
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "" || net.ParseIP(host) != nil {
		return false
	}
	rpid = strings.ToLower(rpid)
	return host == rpid || strings.HasSuffix(host, "."+rpid)
}

func (p *Passkeys) Count() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.keys)
}

func (p *Passkeys) List() []PasskeyInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]PasskeyInfo, 0, len(p.keys))
	for _, k := range p.keys {
		out = append(out, PasskeyInfo{ID: k.ID, Label: k.Label, Created: k.Created, LastUsed: k.LastUsed})
	}
	return out
}

func (p *Passkeys) Delete(id string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, k := range p.keys {
		if k.ID == id || (len(id) >= 4 && len(k.ID) >= len(id) && k.ID[:len(id)] == id) {
			label := k.Label
			p.keys = append(p.keys[:i], p.keys[i+1:]...)
			p.saveLocked()
			return label, true
		}
	}
	return "", false
}

// DeleteAll 清空所有 passkey，返回清掉几把。
//
// 给「撤销全部」/ panic 那条路用：**只清设备是清不干净的**。passkey 是账号级的，不挂在
// 任何一台设备上，而 passkey 登录那条口按设计**不要求认证**（换新设备不用回机器前正是
// 它的价值）—— 所以「撤销了所有设备」之后，任何一把还留着的 passkey 都能立刻换回一份
// 新的设备凭据。少了这一下，`revoke all` 就是一句谎话：你以为按了急停，人还在。
func (p *Passkeys) DeleteAll() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.keys)
	if n == 0 {
		return 0
	}
	p.keys = nil
	p.saveLocked()
	return n
}

// RevokeEverything 是「急停」那一下：清掉所有设备**并且**清掉所有 passkey。
//
// 为什么合成一个函数而不是让四个调用点各写两行：`revoke all` 有四条入口（命令行在线 /
// 命令行离线 / 管理页 / 设置面板里那个「踢掉全部」），**漏掉任何一条，那条路上的急停就
// 还是原来那句谎话**，而且漏了完全看不出来 —— 页面照样回你「撤销了 3 台」。这类
// 「同一件事散在几处、对不上时静默」的坑，这个项目里已经踩过好几回（见 CLAUDE.md）。
//
// 返回（清掉几台设备，清掉几把 passkey），调用方要把后一个数**说出来** —— 人是冲着
// 「踢设备」来的，passkey 一起没了必须当场告诉他，否则下次登录才发现。
func RevokeEverything(s *Store, p *Passkeys) (devs, keys int) {
	return s.RevokeAll(), p.DeleteAll()
}

/* ------------------------------------------------------------------ 落盘 */

func (p *Passkeys) file() string { return filepath.Join(p.cfg.Dir, "passkeys.json") }

type passkeyFile struct {
	Handle string     `json:"handle"`
	Keys   []*Passkey `json:"keys"`
}

func (p *Passkeys) load() error {
	if p.cfg.LegacyFile != "" {
		if _, err := os.Stat(p.file()); err != nil {
			if _, err := os.Stat(p.cfg.LegacyFile); err == nil && os.MkdirAll(p.cfg.Dir, 0o700) == nil {
				_ = os.Rename(p.cfg.LegacyFile, p.file())
			}
		}
	}
	b, err := os.ReadFile(p.file())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			p.handle = randBytes(32)
			return nil
		}
		return err
	}
	var f passkeyFile
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	p.handle, _ = base64.StdEncoding.DecodeString(f.Handle)
	if len(p.handle) == 0 {
		p.handle = randBytes(32)
	}
	p.keys = f.Keys
	p.guard.note(p.file())
	return nil
}

func (p *Passkeys) saveLocked() {
	if err := os.MkdirAll(p.cfg.Dir, 0o700); err != nil {
		return
	}
	b, err := json.MarshalIndent(passkeyFile{
		Handle: base64.StdEncoding.EncodeToString(p.handle), Keys: p.keys,
	}, "", "  ")
	if err != nil {
		return
	}
	tmp := p.file() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, p.file())
	}
	p.guard.note(p.file())
}

/* ------------------------------------------------------- webauthn.User 实现 */

type passkeyUser struct {
	handle []byte
	keys   []*Passkey
}

func (u *passkeyUser) WebAuthnID() []byte          { return u.handle }
func (u *passkeyUser) WebAuthnName() string        { return "herdr-web" }
func (u *passkeyUser) WebAuthnDisplayName() string { return "herdr-web" }
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential {
	out := make([]webauthn.Credential, 0, len(u.keys))
	for _, k := range u.keys {
		out = append(out, k.Cred)
	}
	return out
}

func (p *Passkeys) userLocked() *passkeyUser {
	return &passkeyUser{handle: p.handle, keys: p.keys}
}

/* ------------------------------------------------------------------ 注册 */

var ErrNoPasskey = errors.New("这个部署用不了 passkey：需要用域名访问（裸 IP 不能当 WebAuthn 的 RPID）")

// BeginRegister 返回给浏览器的 options 和一个 ceremony id。
func (p *Passkeys) BeginRegister() (any, string, error) {
	if !p.Available() {
		return nil, "", ErrNoPasskey
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	opts, session, err := p.w.BeginRegistration(p.userLocked(),
		// 要 discoverable（resident key）：这样登录时不用先输用户名，
		// 手机上就是「点一下 → Face ID」。
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		// 要求真的验证用户（生物或 PIN）—— 不然 passkey 只是「有这台设备」，
		// 挡不住「手机没锁屏被拿走」，那正是我们要挡的那件事。
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationRequired,
		}),
	)
	if err != nil {
		return nil, "", err
	}
	return opts, p.stashLocked(session), nil
}

func (p *Passkeys) FinishRegister(ceremony, ua string, r *http.Request) (*Passkey, error) {
	if !p.Available() {
		return nil, ErrNoPasskey
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	session := p.takeLocked(ceremony)
	if session == nil {
		return nil, errors.New("这次注册已经过期了，重新点一次")
	}
	cred, err := p.w.FinishRegistration(p.userLocked(), *session, r)
	if err != nil {
		return nil, err
	}
	now := p.now()
	k := &Passkey{
		ID: hex.EncodeToString(randBytes(6)), Label: LabelFromUA(ua),
		Created: now, LastUsed: now, Cred: *cred,
	}
	p.keys = append(p.keys, k)
	p.saveLocked()
	return k, nil
}

/* ------------------------------------------------------------------ 登录 */

// BeginLogin 走 discoverable（passkey）流程：不需要先知道是谁。
func (p *Passkeys) BeginLogin() (any, string, error) {
	if !p.Available() {
		return nil, "", ErrNoPasskey
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.keys) == 0 {
		return nil, "", errors.New("还没有注册过 passkey")
	}
	opts, session, err := p.w.BeginDiscoverableLogin(
		webauthn.WithUserVerification(protocol.VerificationRequired),
	)
	if err != nil {
		return nil, "", err
	}
	return opts, p.stashLocked(session), nil
}

func (p *Passkeys) FinishLogin(ceremony string, r *http.Request) (*Passkey, error) {
	if !p.Available() {
		return nil, ErrNoPasskey
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	session := p.takeLocked(ceremony)
	if session == nil {
		return nil, errors.New("这次验证已经过期了，重新点一次")
	}
	// 只有一个「用户」，所以这个回调总是返回它；真正的身份判断在签名校验里。
	cred, err := p.w.FinishDiscoverableLogin(
		func(rawID, userHandle []byte) (webauthn.User, error) { return p.userLocked(), nil },
		*session, r)
	if err != nil {
		return nil, err
	}
	for _, k := range p.keys {
		if string(k.Cred.ID) == string(cred.ID) {
			// 计数器要写回去：库靠它发现克隆的认证器
			k.Cred = *cred
			k.LastUsed = p.now()
			p.saveLocked()
			return k, nil
		}
	}
	return nil, errors.New("这把 passkey 不在名单里（可能已经被删掉了）")
}

/* ------------------------------------------------------------------ 小工具 */

// stash/take：challenge 必须存在服务端，而且用一次就废 —— 否则可以重放。
func (p *Passkeys) stashLocked(s *webauthn.SessionData) string {
	now := p.now()
	for id, c := range p.sess {
		if now.After(c.exp) {
			delete(p.sess, id)
		}
	}
	id := hex.EncodeToString(randBytes(16))
	p.sess[id] = &pendingCeremony{data: s, exp: now.Add(ceremonyTTL)}
	return id
}

func (p *Passkeys) takeLocked(id string) *webauthn.SessionData {
	c := p.sess[id]
	if c == nil || p.now().After(c.exp) {
		delete(p.sess, id)
		return nil
	}
	delete(p.sess, id)
	return c.data
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand 读不出来：" + err.Error())
	}
	return b
}

// ReauthNeeded：注册过 passkey 之后，「上次生物验证」太久了就该再验一次。
//
// 为什么整个会话一起卡，而不是只卡「开 PTY」：**发件箱本身就是代码执行**（往一个能跑命令
// 的 agent 嘴里塞话），所以只卡终端等于装饰。一个窗口、一个概念。
//
// registered 是注册过的 passkey 把数：一把都没有时这条完全不生效 —— 否则就把自己锁在
// 一个过不去的门后面了。
// StepUpNeeded：**再注册一把 passkey** 之前要不要先用现有的验一次（§4(d) 的 step-up）。
//
// 要这道门是因为「撤销」被绕过去了，链条是这样的：拿到一个配对码的人配上设备 → 立刻给
// 自己注册一把 passkey → 你发现不对 `revoke all` → 设备清了，**他那把 passkey 还在**，
// 而 passkey 登录不要求认证，他换一份新凭据就回来了。注册那一步是这条链上唯一能掐的点。
//
// 两条判据，缺一不可：
//
//	registered == 0   一把都没有时**不设门** —— 否则第一把永远注册不上，人被锁在门外。
//	                  这也正好是「没有 passkey 就没有这条链」的情况。
//	PasskeyAt         判的是「刚过了一把 passkey」，**不是** VerifiedAt。配对会把
//	                  VerifiedAt 设成当下（见 Device.PasskeyAt 那段），按它判的话刚进来
//	                  的人天然新鲜，这道门对他等于不存在。
//
// within 给 5 分钟那个量级：够点一次 Face ID，短到偷来的 cookie 赶不上。
func StepUpNeeded(id *Ident, within time.Duration, registered int, now time.Time) bool {
	if id == nil || id.Kind != "device" || registered == 0 || within <= 0 {
		return false
	}
	return now.Sub(id.PasskeyAt) > within
}

func ReauthNeeded(id *Ident, after time.Duration, registered int, now time.Time) bool {
	if id == nil || id.Kind != "device" || after <= 0 || registered == 0 {
		return false
	}
	v := id.VerifiedAt
	if v.IsZero() && id.Device != nil {
		v = id.Device.Created // 这个功能之前配的对，拿配对时间当基准
	}
	return now.Sub(v) > after
}
