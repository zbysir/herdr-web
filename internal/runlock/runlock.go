// Package runlock 用一把文件锁回答一个问题：**这个数据目录上是不是已经有服务在跑**。
//
// 为什么需要：命令行子命令（devices / revoke）在 ctl.sock 连不上时会退回**直接改文件**。
// 要是那会儿服务其实在跑，两个进程各写一份 devices.json，后写的赢、静默丢数据。
// 这不是假想 —— 实测撞到过：HERDR_WEB_DIR 指得太深，unix socket 路径超过 104 字节，
// ctl.Listen 失败了但服务照样在跑，于是「连不上 socket」并不等于「没有服务」。
//
// 用 flock 而不是写 pid 文件：进程死了锁自动释放，不会留下一把过期的锁把自己挡在门外。
package runlock

import (
	"os"
	"path/filepath"
	"syscall"
)

func path(dir string) string { return filepath.Join(dir, "run.lock") }

// Acquire 拿住这个数据目录的独占锁。
//
// ok=false 表示已经有别的进程占着（也就是「服务在跑」），这时 err 是 nil ——
// 那是一个正常答案，不是错误。release 总是可以安全调用。
func Acquire(dir string) (release func(), ok bool, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return func() {}, false, err
	}
	f, err := os.OpenFile(path(dir), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return func() {}, false, nil
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, true, nil
}

// InUse 只问「有没有服务在跑」，不留着锁。
//
// 抢到又立刻放掉，中间有个极小的窗口 —— 对「该不该直接改文件」这个判断够用了，
// 因为真正的写入路径那边还有服务自己持着的那把锁。
func InUse(dir string) bool {
	release, ok, err := Acquire(dir)
	if err != nil {
		return false // 连锁都建不起来，别因此挡住命令行
	}
	release()
	return !ok
}
