// Package relay 是云端中继的两头：中继自己（server.go，跑在 VPS 上的 herdr-relay）和
// 主机这一侧（host.go，herdr-web 主动连出去、把隧道里冒出来的连接当成一个监听）。
//
// 设计和「为什么」在 docs/dev/RELAY.md。动这个包之前记住那份文档的出发点：**按中继那台
// VPS 已经被人拿下来设计**。所以下面每一处都守着同一条线 —— 中继手里没有任何能用来
// 登录、解密、冒充的东西：
//
//   - 手机的 TLS **原样转给主机**，中继只读 ClientHello 里的 SNI（sni.go）。中继一旦终止
//     TLS，页面的 JS 就由它发，它想读什么改一行 JS 就读到了 —— 端到端加密在网页上的前提是
//     「页面不是中间人发的」；
//   - 主机向中继证明身份用 Ed25519 签名（中继存的是公钥），不用共享秘密：中继那张表泄露了
//     等于没泄露；
//   - 中继加在每条子流开头的那个小头（手机的真实 IP）是它**说的**，主机拿它只做限速，
//     不做任何认证判断。
package relay

import (
	"crypto/ed25519"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
)

// TunnelPath 是中继控制面上主机连进来的那条路（WebSocket）。
const TunnelPath = "/tunnel"

// challenge：中继 → 主机，握手第一句。
type challenge struct {
	Nonce []byte `json:"nonce"`
}

// hello：主机 → 中继。Invite 只在第一次注册（join）时带。
type hello struct {
	V      int    `json:"v"`
	Host   string `json:"host"`
	Key    []byte `json:"key"` // Ed25519 公钥
	Sig    []byte `json:"sig"`
	Invite string `json:"invite,omitempty"`
}

// verdict：中继 → 主机，握手最后一句。OK 之后两边在同一条连接上切进 yamux。
type verdict struct {
	OK  bool   `json:"ok"`
	Err string `json:"err,omitempty"`
}

// signed 是主机要签的那串字节。
//
// 带上中继自己的域名：同一把主机密钥要是注册在两台中继上，一台中继能拿着自己发的挑战去另一台
// 冒充 —— 它只要把主机对它的回答原样转过去。nonce 挡不住这个（nonce 是它自己出的），域名挡得住。
func signed(relayDomain, host string, nonce []byte) []byte {
	return []byte("herdr-relay/1\n" + relayDomain + "\n" + host + "\n" + string(nonce))
}

func verify(relayDomain string, h hello, nonce []byte) bool {
	if len(h.Key) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(h.Key), signed(relayDomain, h.Host, nonce), h.Sig)
}

/* ------------------------------------------------------------------ 子流的头 */

// streamHeader 是中继在每条子流开头写的东西：手机的真实地址。
//
// 为什么要这个：走 frp 时主机眼里所有请求都是 127.0.0.1，按 IP 的限速整层空转（SECURITY.md §8
// 「frp 把两个默认策略打穿了」）。中继这条路把真实 IP 捡回来了。
// 它是**中继说的** —— 中继被拿下时能乱填，后果只是限速按错的 IP 算，所以绝不能拿它做认证判断
// （这个口在 auth 那边是「公网口」，本来就不认任何「因为你在本机」的豁免）。
type streamHeader struct {
	Src string `json:"src"`
}

// 头很小，上限只是防止一条坏数据让主机读一大块进内存。
const maxHeader = 1024

func writeHeader(w io.Writer, h streamHeader) error {
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	var n [2]byte
	binary.BigEndian.PutUint16(n[:], uint16(len(b)))
	_, err = w.Write(append(n[:], b...))
	return err
}

func readHeader(r io.Reader) (streamHeader, error) {
	var h streamHeader
	var n [2]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return h, err
	}
	l := int(binary.BigEndian.Uint16(n[:]))
	if l > maxHeader {
		return h, fmt.Errorf("子流的头太长（%d 字节）", l)
	}
	b := make([]byte, l)
	if _, err := io.ReadFull(r, b); err != nil {
		return h, err
	}
	return h, json.Unmarshal(b, &h)
}

/* ------------------------------------------------------------------ 名字 */

// CleanHost 把主机名规整成小写、去掉末尾的点，并核一遍是不是一个像样的 DNS 名字。
//
// 中继按 SNI 查表，SNI 是浏览器按 URL 里的主机名发的（小写、没有末尾的点），两边写法
// 不一致的表现是「注册成功、就是连不上」，一个字都不报。
func CleanHost(h string) (string, error) {
	h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
	if h == "" || len(h) > 253 {
		return "", errors.New("主机名为空或太长")
	}
	if net.ParseIP(h) != nil {
		return "", errors.New("主机名不能是 IP（SNI 里不会出现 IP，浏览器按 IP 连时根本不发 SNI）")
	}
	for _, l := range strings.Split(h, ".") {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return "", fmt.Errorf("主机名 %q 里有不合法的一段 %q", h, l)
		}
		for _, c := range l {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", fmt.Errorf("主机名 %q 里有不合法的字符 %q", h, c)
			}
		}
	}
	return h, nil
}
