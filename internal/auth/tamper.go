package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sync"
)

// tamperGuard 记住「我们最后一次读到 / 写出的内容」，用来发现外部改动。
//
// 为什么这两个文件值得单独盯：它们**怕写不怕读**。读走没用（一个只有哈希，一个只有
// 公钥），但谁能往里写就能给自己发一份凭据 ——
//
//   - 往 devices.json 加一条记录：随便挑一个自己知道原文的令牌，把它的哈希写进去；
//   - 往 passkeys.json 加一行自己的公钥。
//
// 而这台机器上跑的 agent 是以你的身份跑的，写得动。
//
// 这层只能检测不能阻止（能写文件的东西也能改掉我们记的哈希）。但两件事仍然成立：
// 服务跑着的时候从不重读这两个文件，所以**外部写入当场不生效**；而且改动会被看见。
type tamperGuard struct {
	mu     sync.Mutex
	path   string
	want   string
	warned bool
}

func (g *tamperGuard) note(path string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.path = path
	g.want = fileHash(path)
	g.warned = false
}

// check 发现不一致就调一次 alert；同一次改动只报一次，免得每 30 秒刷屏。
func (g *tamperGuard) check(what string, alert func(string)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.path == "" || g.want == "" || alert == nil {
		return
	}
	got := fileHash(g.path)
	if got == g.want {
		return
	}
	if g.warned {
		return
	}
	g.warned = true
	how := "被改过"
	if got == "" {
		how = "被删了或者读不出来了"
	}
	alert(g.path + " " + how + "，不是本进程写的。\n" +
		"     本进程用的是内存里那份，所以外部写进去的东西**现在不生效**（重启才会读）。\n" +
		"     但请查一下是谁改的：往" + what + "里加一行就等于给自己发一份能进来的凭据。")
}

func fileHash(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
