// Package lan 回答一个问题：这台机器在局域网里的地址是哪个。
//
// 两个调用方：启动横幅（「手机用这个」那几行、自签证书的 IP SAN），和局域网直连
// 那条路（服务端把当前地址报给网页，网页拿它嗅探能不能直连，见 server/lanapi.go）。
// 之所以收成一个包而不是各写一份：挑网卡的那堆启发式规则一旦有两份，改了一处忘了
// 另一处的表现是「横幅上写的地址和网页去嗅探的地址不是同一个」，而那种不一致只在
// 装了 OrbStack / 连着 VPN 的机器上才露出来。
package lan

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

type Addr struct {
	Name    string // 网卡名（en0 / eth0…），横幅上给人看
	Address string
	score   int
}

// Addresses 机器上虚拟网卡一大堆（OrbStack / VPN / bridge），挑出手机真能连上的那个。
// 排在最前面的那个就是最像「真无线网卡」的。
func Addresses() []Addr {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := []Addr{}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil {
				continue
			}
			ip := ipn.IP.To4().String()
			n := Addr{Name: ifc.Name, Address: ip}
			if strings.HasPrefix(ifc.Name, "en") {
				n.score += 10 // 无线 / 有线
			}
			for _, bad := range []string{"bridge", "utun", "vmnet", "llw", "awdl", "anpi", "ap", "docker", "veth", "tap", "tun"} {
				if strings.HasPrefix(ifc.Name, bad) {
					n.score -= 10
					break
				}
			}
			if strings.HasPrefix(ip, "198.18.") || strings.HasPrefix(ip, "198.19.") {
				n.score -= 20 // benchmark 段，OrbStack 在用
			}
			if strings.HasSuffix(ip, ".0") {
				n.score -= 5
			}
			if IsPrivate(ip) {
				n.score += 2
			}
			out = append(out, n)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	return out
}

func IsPrivate(ip string) bool {
	p := net.ParseIP(ip)
	return p != nil && p.IsPrivate()
}

/* ---------------------------------------------------------------- 带缓存的 */

var (
	mu     sync.Mutex
	cached []Addr
	at     time.Time
	// Now 只给测试换掉。
	Now = time.Now
)

// CacheTTL：net.Interfaces() 是个系统调用，而 CSP 头要在**每个响应**上带当前的局域网
// 地址（IP 会变，拼不死），所以这条路必须有缓存。两秒是拍的：换网之后最多两秒就跟上，
// 而两秒内的连发请求（一次开页面几十个）只走一次系统调用。
const CacheTTL = 2 * time.Second

// Current 带缓存的 Addresses。
func Current() []Addr {
	mu.Lock()
	defer mu.Unlock()
	if cached != nil && Now().Sub(at) < CacheTTL {
		return cached
	}
	cached, at = Addresses(), Now()
	return cached
}

// ResetCache 只给测试用。
func ResetCache() {
	mu.Lock()
	defer mu.Unlock()
	cached, at = nil, time.Time{}
}

// Origins 把当前局域网地址拼成 https origin。给两处用：网页嗅探的候选清单，和 CSP 的
// connect-src（不放行就连嗅探都发不出去 —— 那是同源策略之外的另一道，容易漏）。
//
// **只收私网地址**：公网 IP 挂上去等于把「从任何地方都能直连我」写进 CSP，而这个功能
// 要解决的只是「同一个 Wi-Fi 下别绕公网」。
func Origins(port int) []string {
	if port <= 0 {
		return nil
	}
	var out []string
	for _, a := range Current() {
		if !IsPrivate(a.Address) {
			continue
		}
		out = append(out, fmt.Sprintf("https://%s:%d", a.Address, port))
	}
	return out
}
