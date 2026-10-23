package relay

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Store 是中继落盘的全部东西：哪些主机名注册过、各自的公钥，以及还没用掉的邀请码。
//
// **这里面没有一样东西能用来登录任何地方** —— 公钥本来就是公开的，邀请码只存哈希。
// 这是整个中继设计的承重墙（docs/dev/RELAY.md §1），往里加字段之前先想清楚它泄露了会怎样。
//
// 命令行（`herdr-relay invite`）和跑着的服务会同时改这两个文件，所以每次读改写都拿
// 一把文件锁（flock）。不走「通过服务的接口改」：服务挂着的时候也得能出邀请码、删主机。
type Store struct{ Dir string }

// Host 是一台注册过的主机。
type Host struct {
	Name    string    `json:"name"`
	Key     []byte    `json:"key"` // Ed25519 公钥
	Created time.Time `json:"created"`
}

type invite struct {
	Hash    string    `json:"hash"` // sha256(邀请码) 的 hex
	Host    string    `json:"host"`
	Expires time.Time `json:"expires"`
}

// InviteTTL：邀请码多久作废。够人从 VPS 上抄下来、到 Mac 上敲完 join，短到截图漏出去
// 也来不及用。
const InviteTTL = 30 * time.Minute

func (s Store) path(name string) string { return filepath.Join(s.Dir, name) }

// locked 在文件锁里跑 fn。锁文件和数据文件分开：数据文件是先写临时文件再 rename 换上去的，
// 锁在它身上的话 rename 之后锁的就是一个已经不在目录里的旧 inode。
func (s Store) locked(fn func() error) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path("lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func (s Store) load(name string, v any) error {
	b, err := os.ReadFile(s.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// 读坏了就报错，**不当成空表**：当成空表的下一步就是写回一份空表，把所有主机抹掉
	return json.Unmarshal(b, v)
}

func (s Store) save(name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path(name + ".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(name))
}

// Hosts 列出所有注册过的主机（按名字排）。
func (s Store) Hosts() ([]Host, error) {
	var hs []Host
	err := s.locked(func() error { return s.load("hosts.json", &hs) })
	sort.Slice(hs, func(i, j int) bool { return hs[i].Name < hs[j].Name })
	return hs, err
}

// Lookup 找一台主机；没有就是 nil。
func (s Store) Lookup(name string) (*Host, error) {
	hs, err := s.Hosts()
	if err != nil {
		return nil, err
	}
	for i := range hs {
		if hs[i].Name == name {
			return &hs[i], nil
		}
	}
	return nil, nil
}

// Remove 删掉一台主机。返回删没删到。
func (s Store) Remove(name string) (bool, error) {
	var found bool
	err := s.locked(func() error {
		var hs []Host
		if err := s.load("hosts.json", &hs); err != nil {
			return err
		}
		out := hs[:0]
		for _, h := range hs {
			if h.Name == name {
				found = true
				continue
			}
			out = append(out, h)
		}
		return s.save("hosts.json", out)
	})
	return found, err
}

// NewInvite 给一个主机名出邀请码。码只在这里返回一次，落盘的是哈希。
//
// 邀请码**绑死一个主机名**：拿到码的人只能注册这一个名字，不能拿去占别的。
func (s Store) NewInvite(host string) (string, error) {
	var raw [16]byte // 128 位：不进限速那一层，猜不动
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	code := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:]))
	err := s.locked(func() error {
		var ivs []invite
		if err := s.load("invites.json", &ivs); err != nil {
			return err
		}
		now := time.Now()
		out := ivs[:0]
		for _, iv := range ivs { // 顺手清掉过期的
			if iv.Expires.After(now) {
				out = append(out, iv)
			}
		}
		out = append(out, invite{Hash: hashCode(code), Host: host, Expires: now.Add(InviteTTL)})
		return s.save("invites.json", out)
	})
	return code, err
}

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(code))))
	return hex.EncodeToString(sum[:])
}

// errBadInvite 一律同一句话：别让「码不对」「码过期」「码不是给这个名字的」能被分出来。
var errBadInvite = errors.New("邀请码无效（错了、过期了、用过了，或者不是给这个主机名的）")

// Redeem 用邀请码把一把公钥登记到主机名上。码用一次就废。
//
// 这个名字已经有人注册过时**照样覆盖**：邀请码是中继的主人当场为这个名字出的，意思就是
// 「这个名字归拿码的人」—— 主机丢了密钥（重装系统）时就靠这条重新认领。
func (s Store) Redeem(host, code string, key []byte) error {
	return s.locked(func() error {
		var ivs []invite
		if err := s.load("invites.json", &ivs); err != nil {
			return err
		}
		want := hashCode(code)
		now := time.Now()
		hit := -1
		for i, iv := range ivs {
			if subtle.ConstantTimeCompare([]byte(iv.Hash), []byte(want)) == 1 {
				hit = i
			}
		}
		if hit < 0 || ivs[hit].Host != host || !ivs[hit].Expires.After(now) {
			return errBadInvite
		}
		ivs = append(ivs[:hit], ivs[hit+1:]...)
		if err := s.save("invites.json", ivs); err != nil {
			return err
		}

		var hs []Host
		if err := s.load("hosts.json", &hs); err != nil {
			return err
		}
		out := hs[:0]
		for _, h := range hs {
			if h.Name != host {
				out = append(out, h)
			}
		}
		out = append(out, Host{Name: host, Key: key, Created: now})
		if err := s.save("hosts.json", out); err != nil {
			return fmt.Errorf("登记主机失败: %w", err)
		}
		return nil
	})
}
