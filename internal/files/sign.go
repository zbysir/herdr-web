package files

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 短时签名链接：`/_f/<token>`，**不带 cookie 也能开**。
//
// 为什么非要这么一条路。`/api/*` 上 cookie 认证的请求要求一个自定义头
// （`X-Herdr-Web`，见 server/authapi.go 的 requireAuth）—— 那是 CSRF 的第三道防线。
// 而 `<img src>`、「在新标签打开」、iOS 上「长按存到相册」**都设不了头**，一律 403。
// 所以图片这条路必须换一种凭据：先用带头的请求换一张**只对这一个路径、只在这几分钟
// 有效**的票，再把票放进 URL。
//
// 这是一张能力票（capability），不是身份票：
//
//   - 绑死一个绝对路径，换不到第二个文件；
//   - TTL 很短，过期就废；
//   - 签名密钥是**进程起来时随机生成、只在内存里**的 —— 重启即全废，也不存在
//     「磁盘上多一个长期秘密」这件事（这个项目为了少一个明文秘密，连旧 token 都
//     降级了，见 docs/dev/SECURITY.md）。
//
// 代价说清楚：票在 URL 里，所以它会进浏览器历史、会出现在截图里。谁拿到那串东西，
// 在 TTL 之内就能读那一个文件。referrer-policy 已经是 no-referrer（server/guard.go），
// 所以至少不会跟着外链漏出去。

// TokenTTL 一张票活多久。
//
// 15 分钟是这么来的：`<img>` 是当场就取的，一分钟都够；真正需要长一点的是「在新标签
// 打开那张图、待会儿再回来刷一下」。再长就只是在放大「URL 被看到」的窗口，而重新点
// 一下图片就能拿一张新票，成本几乎为零。
const TokenTTL = 15 * time.Minute

// Signer 一个进程一把随机密钥，不落盘。
type Signer struct {
	key []byte
	ttl time.Duration
}

func NewSigner() (*Signer, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("生成签名密钥失败：%w", err)
	}
	return &Signer{key: key, ttl: TokenTTL}, nil
}

var b64 = base64.RawURLEncoding

// Sign 出一张票。payload 是 `<过期秒>:<绝对路径>`，两段都在签名覆盖范围里 ——
// 只签路径不签过期时间，票就是永久的。
func (s *Signer) Sign(path string, now time.Time) string {
	payload := strconv.FormatInt(now.Add(s.ttl).Unix(), 10) + ":" + path
	enc := b64.EncodeToString([]byte(payload))
	return enc + "." + b64.EncodeToString(s.mac(enc))
}

// Verify 验一张票，回它绑的那个路径。
//
// 顺序很重要：**先验签名再看过期**。反过来的话，一个构造出来的、过期字段写得很远的
// 假票会先通过过期检查，虽然最后照样会被签名拦下，但错误消息会变成「路径不对」之类
// 的东西 —— 排查时会以为签名机制是通的。
func (s *Signer) Verify(tok string, now time.Time) (string, error) {
	enc, sig, ok := strings.Cut(tok, ".")
	if !ok {
		return "", errors.New("链接格式不对")
	}
	got, err := b64.DecodeString(sig)
	if err != nil {
		return "", errors.New("链接格式不对")
	}
	// 定时比较：签名校验的分支时间不能泄露「猜对了几个字节」
	if subtle.ConstantTimeCompare(got, s.mac(enc)) != 1 {
		return "", errors.New("链接签名不对（多半是服务重启过，签名密钥只在内存里）")
	}
	raw, err := b64.DecodeString(enc)
	if err != nil {
		return "", errors.New("链接格式不对")
	}
	expStr, path, ok := strings.Cut(string(raw), ":")
	if !ok {
		return "", errors.New("链接格式不对")
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return "", errors.New("链接格式不对")
	}
	if now.Unix() > exp {
		return "", fmt.Errorf("链接过期了（只有 %s）", TokenTTL)
	}
	return path, nil
}

func (s *Signer) mac(enc string) []byte {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte(enc))
	return h.Sum(nil)
}
