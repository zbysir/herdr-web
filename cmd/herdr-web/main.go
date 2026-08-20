// Command herdr-web 是浏览器里的 herdr 终端：一个二进制，起一个 web server，
// 前端产物嵌在里面。
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"

	"git.huglight.cn/bysir/herdr-web/internal/config"
	"git.huglight.cn/bysir/herdr-web/internal/qr"
	"git.huglight.cn/bysir/herdr-web/internal/server"
	"git.huglight.cn/bysir/herdr-web/internal/webui"
)

func main() {
	webDir := flag.String("web", "", "从这个目录伺候前端（开发用；留空则用嵌进二进制的那份）")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("读配置失败: %v", err)
	}

	var web fs.FS
	if *webDir != "" {
		web = os.DirFS(*webDir)
		if _, err := fs.Stat(web, "index.html"); err != nil {
			log.Fatalf("-web %s 里没有 index.html", *webDir)
		}
	} else {
		web = webui.FS()
	}

	srv := server.New(cfg, web)
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("监听 %s 失败: %v", addr, err)
	}

	banner(cfg, web == nil)
	if err := http.Serve(ln, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}

func banner(cfg *config.Config, noWeb bool) {
	url := func(h string) string {
		return fmt.Sprintf("http://%s:%d/?token=%s", h, cfg.Port, cfg.Token)
	}
	fmt.Println()
	fmt.Println("  herdr-web 已启动")
	fmt.Println("  " + url("127.0.0.1"))

	var nics []nic
	if !cfg.Loopback {
		nics = lanAddresses()
	}
	for i, n := range nics {
		tag := ""
		if i == 0 {
			tag = "  ← 手机用这个"
		}
		fmt.Printf("  %s   %s%s\n", url(n.Address), n.Name, tag)
	}
	fmt.Printf("  shell：%s   数据目录：%s\n", cfg.Shell, cfg.Dir)
	fmt.Printf("  herdr socket：%s\n", cfg.Socket)
	if noWeb {
		fmt.Println("  ⚠️  没有前端产物：先 npm --prefix web run build，或者用 -web web 指向开发目录")
	}

	if len(nics) > 0 {
		if lines := qr.Render(url(nics[0].Address)); len(lines) > 0 {
			fmt.Println()
			for _, l := range lines {
				fmt.Println("  " + l)
			}
		}
		fmt.Println()
		fmt.Println("  ⚠️  正在监听 " + cfg.Host + "：局域网里任何拿到 token 的人都能拿到你的 shell。")
		fmt.Println("     临时试用可以，别长期这么放着；要长期用请套 TLS + 真身份认证的反代。")
		fmt.Println("     注意 http 不是安全上下文，手机上剪贴板相关功能会退化。")
	}
	fmt.Println()
}

type nic struct {
	Name    string
	Address string
	score   int
}

// lanAddresses 机器上虚拟网卡一大堆（OrbStack / VPN / bridge），挑出手机真能连上的那个。
func lanAddresses() []nic {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := []nic{}
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
			n := nic{Name: ifc.Name, Address: ip}
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
			if isPrivate(ip) {
				n.score += 2
			}
			out = append(out, n)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	return out
}

func isPrivate(ip string) bool {
	p := net.ParseIP(ip)
	return p != nil && p.IsPrivate()
}
