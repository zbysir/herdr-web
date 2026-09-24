# herdr-web

<p align="center">
  <img src="assets/logo.png" alt="herdr-web" width="96" />
</p>

<p align="center">
  <a href="README.md">简体中文</a> · <b>English</b>
</p>

A terminal in your browser, built for running [`herdr`](https://github.com/zbysir/herdr).
One Go binary with the frontend baked in. Works on phones.

**Voice compose** is the point: dictate on a tablet, select the words that came out wrong and say
them again, then hand the whole paragraph to an agent's input line. A phone gets you by; a tablet
in landscape gives you 211 columns — that is a workstation.

```
┌─────────────────────────────────────────────┐
│  Panes  Files  Diff  Chat   ...   Settings  │  top bar: you pick the buttons
├─────────────────────────────────────────────┤
│                                             │
│    herdr's panes, exactly as on the desktop │
│                                             │
├─────────────────────────────────────────────┤
│  the paragraph you just said...  [  Send  ] │  outbox: one line, Enter sends
│  [kbd][^B][Ctrl][Esc][Arrows]...swipe...    │  shortcut bar: phones have no Ctrl
└─────────────────────────────────────────────┘
```

## Install

```bash
npm install -g @bysir/herdr-web    # easiest if you have node; upgrades come free
herdr-web                          # listens on 127.0.0.1 only
```

No node (common on servers):

```bash
curl -fsSL https://raw.githubusercontent.com/zbysir/herdr-web/master/install.sh | sh
```

Installs into `~/.local/bin` and **always verifies sha256** — there is a login shell behind this thing.

No native Windows build; install it inside WSL (the terminal needs a real PTY). `go install` works
too, but **what you get has no frontend** — the web assets are built by `make build` and are not in
the repo.

→ From source, pinning a version, changing the install dir: [使用说明 · 装](docs/USAGE.zh-CN.md#装) (Chinese)

## Use

To reach it from a phone or tablet:

```bash
HERDR_WEB_HOST=0.0.0.0 herdr-web
```

The startup banner carries a **one-time pairing code** and its QR code; scan it and you are in.
**Pair once per device** — the credential lives in an HttpOnly cookie, so changing Wi-Fi, changing
subnets or rebooting costs you nothing. On connect it types `herdr` for you.

A path in the URL is **a different herdr session**: `/work` runs `herdr --session work`, so two
bookmarks are two separate working sets that survive closing the browser.

```bash
herdr-web pair          # another one-time pairing code + QR
herdr-web devices       # list paired devices
herdr-web revoke <id>   # kick one (all = everything); the next request gets 401
```

→ Admin page, session naming rules: [使用说明 · 第一次跑](docs/USAGE.zh-CN.md#第一次跑) (Chinese)

## What you get

| | |
|---|---|
| **Outbox** | The one-line input at the bottom: say a whole paragraph, send it to the agent's input line. Enter sends, `⇧↵` breaks a line; paste a screenshot with `⌘V`, or shoot a photo/video on the phone and get its path appended |
| **Shortcut bar** | Phones have no Ctrl key, and herdr is all `ctrl+b`. Keys are yours to define (key script, icon, popup group, pinned so they don't scroll away); arrow keys repeat when held |
| **Pane list** | One row per pane; tap one to jump there and go full screen. A red dot appears when an agent is waiting on you |
| **Chat** | Reads the agent's own session transcript into a conversation stream instead of that screenful of TUI — far easier to read on a phone |
| **Files** | The agent says "plot is at `/tmp/plot-3.png`" — tap the path and look at it. Images render, markdown renders as a document, text files can be edited in place |
| **Diff** | `git diff` is unreadable in a phone terminal: this wraps lines, highlights the changed words, and streams every file of one change continuously. **Read-only** |
| **Phones and tablets** | Touch gestures are fully taken over (drag = wheel reporting, long press = drag a pane border), the bottom dock can move to the right edge, portrait and landscape keep separate layouts |
| **Install as an app** | PWA: its own window, no address bar or toolbar — several free terminal rows |

→ How each one works: [使用说明 · 能干什么](docs/USAGE.zh-CN.md#能干什么) (Chinese)

## Configure

**Environment variables are the only source of configuration** (no config file; the only flag is
`--web`). A few common setups:

```bash
herdr-web                                       # 1. purely local (default), plain http
HERDR_WEB_HOST=0.0.0.0 herdr-web                # 2. phone/tablet on the LAN: self-signed TLS + QR
HERDR_WEB_ONCONNECT= herdr-web                  # 3. stay in the shell, don't enter herdr

# 4. exposed through frp / a tunnel: point the tunnel at the public port, never at the main one
HERDR_WEB_PUBLIC_PORT=17788 HERDR_WEB_TLS=proxy \
HERDR_WEB_PUBLIC_URL=https://herdr.example.com \
HERDR_WEB_HOSTNAME=herdr.example.com herdr-web
```

The main port (7788 by default) **only serves the local network**: a peer that is not loopback or
private gets a 403. To expose it, open `HERDR_WEB_PUBLIC_PORT` — what counts is *which listener the
request landed on*, not a declaration, because on a machine with a tunnel running, `127.0.0.1:7788`
may well be reachable from the whole internet with no local symptom at all.

→ The full table of 40-odd variables: [使用说明 · 配置](docs/USAGE.zh-CN.md#配置) (Chinese)

## Daemon · Updating

```bash
herdr-web service install    # macOS → launchd; Linux → systemd user unit, starts at login
herdr-web update             # check + upgrade, by however it was installed
```

Configuration is snapshotted from the current shell at `install` time, so changing it means running
`install` again with a different environment (it is idempotent).

→ [Daemon](docs/USAGE.zh-CN.md#守护进程) · [Updating](docs/USAGE.zh-CN.md#更新) (Chinese)

## Security

This is effectively a shell over HTTP, and the door is built on that assumption:

- **Pair once per device**: a one-time code buys a per-device credential; the server stores **only a sha256**;
- **Credentials bind to the device, not the IP**, so changing networks never logs you out; **no secrets in URLs** (`?pair=` becomes a cookie and is 302'd away);
- **Only someone sitting at the machine can mint a pairing code** — no path in the web UI produces one; that is the single out-of-band factor;
- **Exposed without TLS refuses to start**; a Host allow-list stops DNS rebinding, three mechanisms stop CSRF, code guessing gets exponential backoff plus lockout;
- **Passkeys are a second factor**; the server stores only public keys.

→ Threat model and the reasoning behind each line: [SECURITY.md](docs/dev/SECURITY.md) (Chinese)

## Documents

| What you want | Where |
|---|---|
| Installing, using, every configuration option | [docs/USAGE.zh-CN.md](docs/USAGE.zh-CN.md) |
| Where to run it, public access, the four TLS tiers | [DEPLOY.md](DEPLOY.md) |
| Getting a DNS token from each provider | [DNS.md](DNS.md) |
| **Why it is built this way**, hand-verified semantics, traps that fail silently | [docs/dev/](docs/dev/README.md) |
| Read before changing code (layout, releases, palette) | [CLAUDE.md](CLAUDE.md) |

**Those documents are in Chinese** — they are the real substance of this project, and this page is
the only English one.

MIT.
