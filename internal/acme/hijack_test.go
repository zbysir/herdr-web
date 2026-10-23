package acme

import (
	"net"
	"testing"

	"github.com/miekg/dns"
)

// 53 端口被劫持 = 一个本不该应答的地址答了。用本机起一个假 DNS 冒充「劫持者」。
func TestDNSHijackedDetectsAnswer(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		_ = w.WriteMsg(m)
	})}
	go srv.ActivateAndServe()
	defer srv.Shutdown()

	old := probeAddr
	defer func() { probeAddr = old }()

	probeAddr = pc.LocalAddr().String()
	if !dnsHijacked() {
		t.Fatal("有人应答时必须认成被劫持")
	}
	if len(propagationOpts()) == 0 {
		t.Fatal("被劫持时必须换掉本机的传播自检，否则签发必然超时")
	}

	// 没人应答（端口上什么都没有）：照旧用 lego 默认的自检
	l, _ := net.ListenPacket("udp", "127.0.0.1:0")
	probeAddr = l.LocalAddr().String()
	l.Close()
	if dnsHijacked() {
		t.Fatal("没人应答不能算被劫持")
	}
	if len(propagationOpts()) != 0 {
		t.Fatal("没被劫持时不该改 lego 的默认自检")
	}
}
