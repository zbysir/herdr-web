# herdr-web

<p align="center">
  <img src="assets/logo.png" alt="herdr-web" width="96" />
</p>

<p align="center">
  <b>English</b> · <a href="README.zh-CN.md">简体中文</a>
</p>

A terminal in your browser, built for running [`herdr`](https://github.com/zbysir/herdr).
One Go binary with the frontend baked in. Works on phones.

**Voice compose** is the point of this project: dictate on a tablet, select the words that came out
wrong and say them again, then hand the whole paragraph to an agent's input line. A phone is enough
to get by; a tablet in landscape gives you 211 columns — that is a workstation.

This document covers **installing, using and configuring** it. Why each thing works the way it does,
and what it cost to learn, lives in the [documents listed at the end](#documents) — those are the
real substance of this project. They are in Chinese.

## Install

```bash
npm install -g @bysir/herdr-web       # easiest if you have node; upgrades come free
herdr-web                             # listens on 127.0.0.1 only
```

No node (common on servers):

```bash
curl -fsSL https://raw.githubusercontent.com/zbysir/herdr-web/master/install.sh | sh
```

Installs into `~/.local/bin`. To install somewhere else, the variable has to go to `sh`, **not** to `curl`:

```bash
curl -fsSL …/install.sh | HERDR_WEB_INSTALL_DIR=/opt/bin sh    # right
HERDR_WEB_INSTALL_DIR=/opt/bin curl -fsSL …/install.sh | sh    # wrong — curl gets it, the script never sees it
```

The wrong one **does not fail**; it quietly installs to the default directory. Same shape for `HERDR_WEB_INSTALL_VER=v0.1.0` to pin a version.

The installer **always verifies sha256** and refuses to install if neither `sha256sum` nor `shasum` exists — there is a login shell behind this thing.

Other ways in:

```bash
make build && ./herdr-web             # from source (frontend → internal/webui/dist → go build)
HERDR_WEB_HOST=0.0.0.0 ./herdr-web    # listen on the LAN, and print a QR code for your phone
```

`go install github.com/zbysir/herdr-web/cmd/herdr-web@latest` works too, but **what you get has no frontend**: the web assets are produced by `make build` and embedded, and they are not in the repo, so `go install` can't see them. That binary is only useful with `--web <dir>` pointing at a frontend you built yourself, or for the CLI subcommands. Use one of the three above if you want the page.

**No native Windows build** — install it inside WSL. Not laziness: the terminal in the browser needs a real PTY (Go side uses `creack/pty`, whose Windows implementation is a `return nil, ErrUnsupported` stub) and herdr itself speaks over a unix socket. Inside WSL it is simply the Linux build, fully functional; the browser end was always cross-platform, so `http://localhost:7788/` on Windows works fine. On win32 the npm package prints that explanation instead of installing something that cannot run.

To run it as a service that starts at boot, see [Daemon](#daemon). To upgrade, see [Updating](#updating).

**Environment variables are the only source of configuration** (no config file; the only flag is `--web`). The full list, how to set it, and a few common setups are under [Configuration](#configuration). Subcommands: `herdr-web --help`.

On startup it prints the addresses you can reach it at. When listening on `0.0.0.0` it scores the interfaces and marks the one your phone can actually reach with `← use this one from your phone` (the pile of OrbStack / VPN virtual interfaces gets pushed to the bottom); that is the address encoded in the QR code.

**Pair each device once.** The startup banner carries a one-time pairing code (5 minutes, single use) and its QR code — scan it from your phone and you are in, zero typing. After that your bookmark holds no secret (the credential lives in an `HttpOnly` cookie), and changing Wi-Fi, changing subnets or rebooting costs you nothing. To pair another device, run `herdr-web pair` on the machine.

Two ways to scan:

- **Your camera app** (works everywhere): the code is just a link with `?pair=`; scanning opens it and you land already paired.
- **"Scan with camera" inside the pairing page**: opens the rear camera, points at the code on the host screen, pairs on recognition without navigating. This button **only appears when it can work** — it needs `BarcodeDetector` (the system decoder, which saves tens of KB of JS; macOS uses Vision, Android uses ML Kit, and **iOS Safari and Chrome on Linux do not have it**) plus a camera (only granted in a secure context — plain http on a LAN gets nothing). If either is missing the button is not rendered at all, rather than left there to fail on click.

If neither is convenient, type the 8-digit code into the pairing page; it submits itself once you have typed 8 characters. What the in-page scanner reads is reduced to the `pair=` part and goes through the same `POST /auth/pair`, so the security model is unchanged (only someone at the machine can produce a code).

```bash
herdr-web pair          # print a fresh one-time pairing code + QR
herdr-web devices       # list paired devices (label / last seen / last IP / expiry)
herdr-web revoke <id>   # kick one (all = everything); the next request gets 401
herdr-web unlock        # clear the global "too many failures" circuit breaker
```

The ⚙ at the right end of the top bar is the **settings panel**; its "Devices" page shows who has paired, lets you **sign this device out**, and kick others. Kicking one and kicking everyone both take two clicks. **The web UI never issues a pairing code** (not even to an already-paired device) — see [Security](#security) for why.

**Out of codes? Go back to the machine and run `herdr-web pair`.** That is not laziness either — see below.

The old never-expiring `~/.herdr-web/token` is demoted to **bootstrap only**: an old bookmark exchanges it for a device credential on first open and scrubs the token out of the URL, after which you should `rm ~/.herdr-web/token`. Details and reasoning in [SECURITY.md](SECURITY.md) (Chinese).

Once connected it **types `herdr` for you**. To type something else, or nothing: `HERDR_WEB_ONCONNECT` (set it to an empty string to stay in the shell). Adding a path segment to the URL (`/work`) gives you **a different herdr session** — see [First run](#first-run). The old "run herdr" button in the top bar is gone: with autotyping it earns its place less than once a day, and the soft key bar ships a preset for it if you want one.

**The admin page is at `http://127.0.0.1:<port+1>/`** (also in the startup banner): certificate status, one-click issue/renew, generated DNS `.env` snippets, pairing codes, device kicking. It is **bound to loopback and does not exist on the public internet**, so it needs no login — anything that can reach it already has your shell. Why not "an authenticated page on the main server": authentication is a control that can fail, unreachability is a property; and the admin page must not depend on the very certificate it exists to fix (a broken certificate would lock you out of the page that repairs it).

## First run

On startup it prints the addresses you can reach it at. When listening on `0.0.0.0` it scores the
interfaces and marks the one your phone can actually reach with `← use this one from your phone`;
that is the address encoded in the QR code.

**Pair each device once.** The startup banner carries a one-time pairing code (5 minutes, single use)
and its QR code — scan it from your phone and you are in, zero typing. After that your bookmark holds
no secret (the credential lives in an `HttpOnly` cookie), and changing Wi-Fi, changing subnets or
rebooting costs you nothing. Three ways to scan: your camera app (the code is a link with `?pair=`),
"scan with camera" inside the pairing page (only shown when it can work — it needs `BarcodeDetector`
and a camera, which requires a secure context), or typing the 8-digit code into the pairing page.

```bash
herdr-web pair          # print a fresh one-time pairing code + QR
herdr-web devices       # list paired devices (label / last seen / last IP / expiry)
herdr-web revoke <id>   # kick one (all = everything); the next request gets 401
herdr-web unlock        # clear the global "too many failures" circuit breaker
```

**The web UI never issues a pairing code** (not even to an already-paired device) — see
[Security](#security). Out of codes? Go back to the machine and run `herdr-web pair`.

Once connected it **types `herdr` for you**. To type something else, or nothing:
`HERDR_WEB_ONCONNECT` (an empty string means stay in the shell).

**A path segment in the URL is a different herdr session**: `/work` types
`herdr --session work` and creates it if needed; `/scratch` is another one. Two bookmarks are two
working contexts that survive closing the browser. Names are `[A-Za-z0-9._-]`, 40 characters max;
an invalid one is an error rather than a silent fallback to the default session — using the wrong
socket would **silently deliver your words into another herdr**.

**The admin page is at `http://127.0.0.1:<port+1>/`**: certificate status, one-click issue/renew,
generated DNS `.env` snippets, pairing codes, device kicking. It is bound to loopback and does not
exist on the public internet, so it needs no login — anything that can reach it already has your shell.

**Local shell only.** To reach another machine, ssh from inside herdr — herdr does that itself, so
this layer implements no host management and no key storage, and the "the browser can touch your
private keys" attack surface never exists.

## What you get

### Outbox (voice compose)

The strip with a textarea at the bottom of the page is the outbox; the ✎ in the top bar toggles it
and it is **on by default**. You dictate or type in there, fix what came out wrong, then hand the
whole paragraph to one of herdr's panes.

| Control | What it does |
|---|---|
| **Target** | Defaults to "follow herdr's current pane" — nothing to pick, it goes to whatever you have focused in herdr. You can also pin one from the dropdown |
| **Post** `⌘↵` / `Ctrl↵` | Clears the remote input line first, then submits the whole thing. `Enter` inserts a newline and does not submit |
| **Pull back** | Grabs what is already in the remote input line into the textarea for editing (useful when the remote side has been Tab-completing) |
| **Auto pull** | Every 500ms by default. Switching panes swaps in the new pane's content; **never overwrites a local draft**, it just says so in the status line |
| **Two-way** | Local edits get pushed back into the remote input line (without Enter). Off by default — see the caveats below |
| **Image** | Upload an image; the path is inserted **at the cursor**. On a phone it offers camera / library; on a desktop just `⌘V` a screenshot into the box, or drop a file. You do not need the outbox open for this — bind `act:img` on the soft key bar, or paste anywhere on the page |
| `↑` | With an empty box, recalls the last thing you posted (30 kept locally) |
| `Esc` | **Forwarded to the terminal.** Esc means nothing inside a plain textarea, while the agent needs it constantly (overlays like `/usage` close with it); focus does not move, so you can press it repeatedly |

Uploading does not need the outbox open: bind `act:img` on the soft key bar, or **paste anywhere on
the page** (an image in the clipboard is uploaded directly). Where the path lands depends on whether
the outbox is open — appended to your draft, or typed straight into the terminal.

→ Why a separate box at all, how images actually work, the two-way caveats, measured polling
latency: [OUTBOX.md](OUTBOX.md)

### Soft key bar

Phones have no Ctrl key, and herdr's `ctrl+b` prefix depends on one. The keys live **on the server**
(`~/.herdr-web/softkeys.json`), so phone / tablet / desktop share one set of definitions, edited in
Settings → Soft keys.

- The "Keys" field takes a **key spec**; space-separated entries fire in sequence — `ctrl+b c` is the
  prefix plus c, one tap.
- Supports `ctrl+x` `alt+x` `shift+tab`, named keys (`esc tab enter space bs del ins up down left
  right home end pgup pgdn f1-f12`) and literal text (`text:/new`; quote it if it has spaces).
- `sticky:ctrl` / `sticky:alt` are **sticky** modifiers: tap once to light it up, then a letter sends
  the combination.
- `act:` actions run in the browser and send no bytes: `act:kbd` (system keyboard), `act:img`
  (upload), `act:panes` (pane list), `act:files` (file browsing), `act:clip` / `act:paste`
  ([copy and paste on a phone](MOBILE.md#手机上怎么复制--粘贴)).
- Every key has a **"double-tap"** checkbox; close pane / close tab / `/clear` ship with it on —
  keys sit close together and a misfire cannot be undone.
- "Load presets" pours sixty-odd keys into "My keys", after which every one of them is yours to edit.

Key specs are parsed into bytes **on the server**, so a typo is reported at save time — telling you
which key and where it stopped making sense — rather than shipped as a key that does nothing.

### Pane list · notices

The ▦ in the top bar (or `act:panes` on the soft key bar; on a phone you can also tap herdr's own
`switch`) opens a list of panes, one per row — **tap one and you are there, zoomed full screen**.
You can filter (tab / title / path / pane id) and show only panes running an agent. The list
refreshes itself every 4 seconds.

When an agent stops to wait for you (or has just finished), **a card appears in the top right
carrying what it said**, and a badge lights up on the ▦. Tapping the card jumps there. Opening the
pane list is what marks them read.

It is an index, not a second interface: after the tap you are looking at the same herdr terminal, and
every keyboard habit is unchanged.

→ Sort order, the "3 minutes ago" column, when a notice fires, how the badge counts, system
notifications: [MOBILE.md](MOBILE.md)
　How that text is scraped off the screen: [COMPOSER.md](COMPOSER.md)

### File browsing

The agent says "the plot is at `/tmp/plot-3.png`" — **tap that path and look at it**. Absolute paths
open directly; `./out/a.png` resolves against that pane's cwd. The 📁 in the top bar (or `act:files`)
is the fallback: it starts from every pane's cwd + the upload directory + home + temp, `..` walks all
the way to `/`, and you can paste an absolute path to open it.

Images (png / jpg / gif / webp, identified by magic number) are shown, text is shown as-is, anything
else downloads. From the viewer you can **hand the file to the agent** in one tap (its absolute path
goes into the outbox).

**There is no boundary by default** — anyone who can open this page already has a login shell, so an
allowlist would not stop them and would only get in the way daily. If you want one, set
`HERDR_WEB_FILE_ROOTS` (that is a real jail); to remove the feature, `HERDR_WEB_FILES=0`.

→ The short-lived link route and the four hard rules on it (never `text/html`, why SVG is safe to
render): [SECURITY.md](SECURITY.md)

### Phones and tablets

When a program has mouse reporting on (herdr does), touch gestures are taken over entirely:

| Gesture | Behaviour |
|---|---|
| One-finger vertical swipe | Converted to SGR wheel reports by line height — `CSI < 64/65 ; col ; row M` — and sent to the program; with mouse reporting off it scrolls the local scrollback |
| Tap | With mouse reporting, sends `CSI < 0 ; col ; row M/m` (clicking panes and tabs both work) and **does not pop the system keyboard**; without it, focuses the hidden textarea (a tap there does mean "I want to type"). **Sending it out waits for the double-tap window (320ms)**, see "Double tap"; the page's own action (claiming herdr's `switch` button to open the pane list) happens immediately |
| Long press (≈380ms) | **Grab**: press the left button and hold, plus `CSI < 32` motion reports, so moving afterwards is a drag — this is how you resize herdr's pane borders on a phone. Releasing sends the matching `m` |
| Double tap | Show / hide the system keyboard. **The first tap leaves nothing behind** — anything that "goes out and cannot be taken back" (the mouse report to the program in the pane, opening the path or URL you tapped) waits out the double-tap window (320ms) and the whole lot is cancelled when a second tap arrives. The line is "does this leave the browser": what does not (opening our own panel) happens immediately, or every tap feels stuck. Why it has to be this strict: Claude Code has its own clickable UI (expanding a block, **picking an option**), and a leaked first tap picks the option for you |

**The outbox and the soft key bar are one dock**: drag either side edge to change its width (when an
IME covers half the screen, shrink the whole dock into what is left), and the three handles on the
top edge of the key area set the height and the boundaries; double-tap any handle to reset. A phone
in portrait (< 440px) switches to another tier: no handles, full width, one horizontally-scrolling
row of keys. **Landscape and portrait keep separate sets**, swapped on rotation.

**The top bar is yours to arrange**, and **layouts are stored per kind of device**: the six keys you
arranged on a phone do not follow you to the desktop, while the definitions stay shared.

→ Why the gestures are split this way, how the keyboard is handled, copy and paste on a phone, the
details of the dock and the top bar: [MOBILE.md](MOBILE.md)

### Settings panel

The ⚙ at the right end of the top bar, in four pages: **Terminal** (font size / light-dark, kitty
protocol / Option as Meta / copy on select / synchronized output, herdr's switch opening our pane
list, the badge on the panel icon), **Top bar**, **Soft keys**, **Devices**. Above the tabs there is
one more row: which layout profile this device uses. The three overlays (pane list / files /
settings) are mutually exclusive.

### Keyboard

herdr's shortcuts are almost all `ctrl+b` plus an ordinary key, which legacy encoding can express.
The kitty protocol covers what legacy cannot and is on by default (Settings → Terminal):
`Ctrl+Shift+letter`, `Ctrl+digit`, `Ctrl+Enter` / `Shift+Enter` / `Ctrl+Tab`.

Keys the browser keeps for itself: on macOS `⌘W` `⌘T` `⌘N` `Ctrl+Tab`; on Windows/Linux also
`Ctrl+W` `Ctrl+T` `Ctrl+N` `Ctrl+Shift+I/J/C`. Installing as a PWA gets some of them back.

Copy `⌘C` (or `Ctrl+Shift+C`) · paste `⌘V` · clear `⌘K` · `Option` is Meta by default. Copy and
paste on a phone is a different story — herdr copies to the clipboard of **the machine running
herdr** — see [MOBILE.md](MOBILE.md#手机上怎么复制--粘贴).

## Configuration

**Environment variables are the only source of configuration.** There is no config file, and the only flag is `--web` (point at a frontend directory during development). It is funnelled through [viper](https://github.com/spf13/viper) in `internal/config/` (`SetEnvPrefix("HERDR_WEB")` + `AutomaticEnv()`), so settings and variable names map one to one.

Not reading a config file is deliberate: there is a login shell behind this port, so "which configuration is actually in effect" has to be visible at a glance — environment variables are right there in `ps`, in the systemd unit, in the launchd plist. Add "there might also be a yaml in some directory" and the first half day of any incident goes into finding out which one won. Same reason there is no "flags override environment": one setting with two entry points means having to specify precedence.

### How to set it

```bash
# Try something: prefix the command, applies to this run only
HERDR_WEB_PORT=8000 HERDR_WEB_ONCONNECT= ./herdr-web

# Permanent: in ~/.zshrc (when you start it by hand in a terminal)
export HERDR_WEB_HOST=0.0.0.0
export HERDR_WEB_TLS=auto

# Permanent: launchd (macOS) in the plist's EnvironmentVariables;
# systemd in the unit's Environment= / EnvironmentFile=
```

Three rules, all about not guessing:

- **An explicit empty string counts.** `HERDR_WEB_ONCONNECT=` means "type nothing on connect"; it does not fall back to the default `herdr`. Every switch with a default depends on this to be turnable off.
- **A malformed integer is treated as unset** (falls back to the default) rather than silently becoming 0; below-minimum values are clamped. `HERDR_WEB_DEVICE_TTL_DAYS=9O` (letter O) will not turn device credentials into "never expires".
- **Booleans accept `1` / `true`** (any case); anything else is off.

Changes take effect on restart — configuration is read once at startup. To confirm what was read, look at the startup banner: shell, data directory, herdr socket, TLS tier and paired device count are all printed there.

### Basics

| Variable | Default | Meaning |
|---|---|---|
| `HERDR_WEB_PORT` | `7788` | Port |
| `HERDR_WEB_HOST` | `127.0.0.1` | Listen address; `0.0.0.0` opens it to the LAN |
| `HERDR_WEB_TOKEN` | reads `~/.herdr-web/token` | **Legacy**; only good for bootstrapping once (exchanged for a device credential). Not generated on new installs |
| `HERDR_WEB_SHELL` | `$SHELL` | The shell run inside the PTY |
| `HERDR_WEB_ONCONNECT` | `herdr` | Typed into the PTY on connect (Enter included). **Set it to an empty string to type nothing.** **Session URLs ignore this** (`/work` always types `herdr --session work`, see [First run](#first-run)) — to always land in a session, bookmark the URL rather than setting this |
| `HERDR_WEB_ONCONNECT_MS` | `250` | How long to wait before typing that line. The wait starts **after the shell's first output** — an rc file touching `stty`, or a completion plugin initialising, **silently swallows** characters typed too early. If the auto-typed line does not land, raise it |
| `HERDR_WEB_DIR` | `~/.herdr-web` | Data directory, in two layers: configuration and files (`softkeys.json` / `tls/` / `uploads/`) at the root, **internal data** (device credentials, passkey public keys) under `data/` — those two are not meant to be hand-edited, and tampering is reported in the terminal. **Keep the path short**: a unix socket (`ctl.sock`) is opened inside it, and beyond ~100 bytes it cannot bind, which breaks the subcommands |
| `HERDR_WEB_FILES` | on | `=0` turns file browsing off: `/api/files/*` and `/_f/` all 404, and the 📁 in the top bar is not drawn (an entry point that opens onto a wall of 404s is worse than no entry point) |
| `HERDR_WEB_FILE_ROOTS` | empty | Comma-separated directories. Set, this is **a real allowlist** (a jail) and only those trees are visible. **Empty means no boundary** — the reasoning is in [File browsing](#file-browsing). `~` is expanded; non-absolute entries are discarded (relative to what? keeping them only makes the prefix check pass somewhere surprising) |

### Outbox / talking to herdr

| Variable | Default | Meaning |
|---|---|---|
| `HERDR_WEB_SOCKET` | `$HERDR_SOCKET_PATH` or `~/.config/herdr/herdr.sock` | The herdr socket the outbox connects to. **Do not rely on `HERDR_SOCKET_PATH`**: `dropEnv` strips `HERDR_*`, and this process may not have been started from a herdr pane at all |
| `HERDR_WEB_POLL_MS` | `500` | How often the outbox checks "where is focus, what is in the input line". Minimum 200 |
| `HERDR_WEB_PUSH_MS` | `700` | With "two-way" on, how long after you stop typing the draft is pushed. Minimum 100 |
| `HERDR_WEB_NOTICE_MS` | `4000` | How often notices (the cards and the unread badge) ask "anything new". **`0` turns the whole notice feature off** and the frontend stops polling. Anything under 1000 is treated as 1000 — this tick only reads memory on the server (it does not touch the herdr socket), but a notice is inherently 2.5 seconds behind the state change (debounce), so polling harder cannot beat that |
| `HERDR_WEB_SETTLE_MS` | `120` | How long to wait between two `pane.read` calls (to defeat the one-frame snapshot lag). **Never 0**: herdr sometimes answers in 1-2ms, both reads land on the same frame, and the clear loop misreads that as "cannot be cleared". The clear path has its own 120ms floor |

### Exposure / TLS / credentials

Details in [SECURITY.md](SECURITY.md) (Chinese).

| Variable | Default | Meaning |
|---|---|---|
| `HERDR_WEB_EXPOSED` | off | `=1` **declares that this port is reachable from the internet** (frp / port forwarding / tunnels). Behind frp the process usually listens on 127.0.0.1 and every request also comes from 127.0.0.1, so "is the listen address local" tells you nothing; it cannot be detected, only declared. Once declared: TLS is mandatory and loopback-without-pairing is turned off |
| `HERDR_WEB_TLS_CERT` / `_KEY` | empty | Use the certificate you supply. If you own a domain and got a real certificate via DNS-01, take this route — zero browser warnings, no profiles to install, least friction |
| `HERDR_WEB_ACME_DNS` | empty | Let herdr-web **get its own certificate**; the value is the DNS provider: `cloudflare` / `alidns` / `tencentcloud` / `route53` / `digitalocean` / `huaweicloud`. It uses DNS-01, so nothing has to reach you from outside — behind NAT, or with the domain pointed at a LAN address, it still works. **Where to get each provider's token and what scope it needs: [DNS.md](DNS.md)** (Chinese) |
| `HERDR_WEB_ACME_EMAIL` | empty | ACME account email. Can be empty, but then you get no expiry reminders either |
| `HERDR_WEB_ACME_STAGING` | off | `=1` uses Let's Encrypt staging. **Turn it on while debugging**: production allows 5 certificates per domain set per week, and a few attempts lock you out for a week |
| `HERDR_WEB_TLS` | see notes | `auto` self-signed (local CA + 397-day leaf, re-issued automatically when the IP changes) / `off` plaintext / `proxy` something in front already terminated TLS. Default: exposed or listening on the LAN → `auto`, purely local → `off` |
| `HERDR_WEB_HOSTNAME` | empty | Domains allowed in the `Host` header, comma separated. **IPs always pass, domains must be listed** — this is the only defence against DNS rebinding, and anything else gets a 421 |
| `HERDR_WEB_PUBLIC_URL` | empty | The address you **actually visit** (`https://herdr.example.com:17788`). With frp the public port is often not the local one, and without this the QR code in the banner is useless. The domain in it is allowlisted automatically |
| `HERDR_WEB_DEVICE_TTL_DAYS` | `90` | How long a device credential survives without use (renewed on every use). `0` = **never expires** |
| `HERDR_WEB_RPID` | derived | The domain a passkey is bound to. Defaults to the first `HERDR_WEB_HOSTNAME`, or `localhost` when purely local. **A bare IP is not a valid value** — such deployments cannot use passkeys |
| `HERDR_WEB_REAUTH_HOURS` | `24` | Once a passkey is registered, how long a session credential remains valid after the last biometric check. `0` = no re-verification (passkeys serve only as the login / new-device path). **Does nothing at all while no passkey is registered** |
| `HERDR_WEB_LEGACY_TOKEN` | `on` | `on` / `loopback` (the old token only works locally) / `off`. Once migrated, just delete the token file |
| `HERDR_WEB_TRUST_LOOPBACK` | off | `=1` exempts requests from 127.0.0.1 from pairing. **Never turn this on behind frp or a reverse proxy** — there, public requests also arrive from 127.0.0.1, i.e. everyone is "local". When on, it additionally requires `Host` to be a loopback literal |
| `HERDR_WEB_TRUST_PROXY` | off | `=1` is required to read `X-Forwarded-For`. With no trusted proxy in front, leaving it on lets an attacker forge the source IP with a header and walk around per-IP rate limiting |
| `HERDR_WEB_INSECURE` | off | `=1` permits "exposed but no TLS". No legitimate use beyond temporary debugging |
| `HERDR_WEB_UPDATE_CHECK` | on | `=0` disables automatic update checks. With it off the process makes **no outbound requests at all** — a hard requirement in the kind of environment where an internal machine must not dial out. Only the automatic check is disabled; `herdr-web update --check` still works |

### Troubleshooting

| Variable | Default | Meaning |
|---|---|---|
| `HERDR_WEB_DEBUG_INPUT` | off | `=1` logs every batch of bytes written into the PTY as hex (including the auto-typed line, prefixed `onconnect`). The only way to answer "what exactly did that key send" — guessing does not work |

### Read but not prefixed with `HERDR_WEB_`

| Variable | When it matters |
|---|---|
| `SHELL` | The shell run inside the PTY when `HERDR_WEB_SHELL` is unset (falling back to `/bin/zsh`) |
| `HERDR_SOCKET_PATH` | Fallback herdr socket when `HERDR_WEB_SOCKET` is unset. **Do not count on it being there**: `dropEnv` strips `HERDR_*` from child processes (to prevent nesting), and this process may not have started from a herdr pane |

### A few common setups

```bash
# 1. Purely local (default): plain http, since loopback is a secure context anyway
./herdr-web

# 2. Phone / tablet on the LAN: self-signed TLS, pair by scanning the banner QR
HERDR_WEB_HOST=0.0.0.0 ./herdr-web

# 3. Exposed through frp / a tunnel: EXPOSED must be declared (the process only
#    listens on 127.0.0.1 and cannot tell whether anyone outside can reach it),
#    PUBLIC_URL decides which address the QR code encodes
HERDR_WEB_EXPOSED=1 HERDR_WEB_TLS=proxy \
HERDR_WEB_PUBLIC_URL=https://herdr.example.com \
HERDR_WEB_HOSTNAME=herdr.example.com ./herdr-web

# 4. Your own domain + a real certificate (zero browser warnings, least friction)
HERDR_WEB_HOST=0.0.0.0 HERDR_WEB_HOSTNAME=herdr.example.com \
HERDR_WEB_TLS_CERT=/etc/ssl/herdr/fullchain.pem \
HERDR_WEB_TLS_KEY=/etc/ssl/herdr/privkey.pem ./herdr-web

# 5. Do not drop into herdr on connect (stay in the shell)
HERDR_WEB_ONCONNECT= ./herdr-web
```

## Daemon

Install it as a user-level service that starts on boot:

```bash
herdr-web service install     # macOS → launchd LaunchAgent; Linux → systemd user unit
herdr-web service status      # installed? running? PID? where are the logs?
herdr-web service logs        # tail -f the log
herdr-web service restart     # needed after replacing the binary
herdr-web service uninstall   # stop and remove (data and logs untouched)
```

**Configuration is copied out of the current shell at install time.** So the order is "get the environment right, then install"; changing configuration means installing again (it is idempotent — overwrite and restart). To read it from a file:

```bash
herdr-web service install --env-file .env
```

What gets copied is every `HERDR_WEB_*`, plus `PATH` / `SHELL` / `HOME` / `USER` / `LOGNAME` / `LANG` / `LC_ALL` / `TERM` / `HERDR_SOCKET_PATH`. `install` prints the whole list — from then on, "which configuration is this machine's service actually using" can only be answered by the plist / unit, so it is cheapest to read it at install time.

**DNS provider credentials carry the `HERDR_WEB_` prefix too** (`HERDR_WEB_CLOUDFLARE_DNS_API_TOKEN`, `HERDR_WEB_ALICLOUD_ACCESS_KEY` and friends), so the rule above already copies them — exporting them in your shell is enough, no `--env-file` required. The prefix is not cosmetic: a bare `CLOUDFLARE_DNS_API_TOKEN` matches neither the prefix nor the allowlist, so it is not copied, and that failure only surfaces at the first issuance (or three months later, at the first renewal). lego still reads the bare names, but those can only reach the service through `--env-file`; when both are set, the prefixed one wins. Per-provider variable names are in [DNS.md](DNS.md).

Keys in `--env-file` go in **wholesale** (and override the current environment). The file is read at `install` time only and never touched again. In the list `install` prints, credentials show up as asterisks and a length — that output often lands in a pane with an agent in it.

The plist / unit is **0600** — its contents are exactly that environment in plaintext.

**Copying `PATH` is mandatory, and it is the most common failure after installing as a service**: launchd's default `PATH` is only `/usr/bin:/bin:/usr/sbin:/sbin`, so `HERDR_WEB_ONCONNECT=herdr` turns into `herdr: command not found` while the page just shows an empty shell with no clue why.

Why user-level rather than system-level: this process opens **your** shell. Running it as a root system service means the terminal in the browser is root's, permissions jump straight to maximum, and `~/.herdr-web` and `~/.config/herdr/herdr.sock` all point at somebody else's home.

Platform-specific traps:

| | File | Note |
|---|---|---|
| macOS | `~/Library/LaunchAgents/io.github.zbysir.herdr-web.plist` | A LaunchAgent starts **at login**, not at boot. On a machine with automatic login the two are equivalent; otherwise you have to log in once. "Start with nobody logged in" would require a system-level daemon in `/Library/LaunchDaemons`, which makes the shell root's — this project does not do that. |
| Linux | `~/.config/systemd/user/herdr-web.service` | `install` also runs `loginctl enable-linger`. **Without linger the service is stopped when you log out of ssh** — for a machine you want to reach at any time, that is the same as not running at all. If it fails it tells you to run `sudo loginctl enable-linger $USER`. |

Logs are at `~/.herdr-web/logs/herdr-web.log` on both platforms (deliberately identical, so the docs and `service logs` have one answer). On Linux `journalctl --user -u herdr-web` works as well.

`service status` reporting "installed but not running" means **it crashes on start**, and the reason is only in the log — launchd and systemd both keep retrying with a few seconds of backoff, so without looking you would assume it is running.

Windows, and Linux without systemd (containers, WSL1), are told clearly that this cannot work and what to do instead, rather than being given something that will not run. On WSL2, add `[boot] systemd=true` to `/etc/wsl.conf` and `wsl --shutdown` to restart, and it works.

## Updating

```bash
herdr-web update            # check and upgrade
herdr-web update --check    # check only, change nothing
herdr-web update --restart  # upgrade, then restart the service
herdr-web version           # current version + how it was installed
```

**How it upgrades depends on how it was installed**, and `update` works that out itself (from the executable's path, resolving symlinks first):

| Installed via | Upgrade action |
|---|---|
| npm | runs `npm install -g @bysir/herdr-web@latest` |
| homebrew | runs `brew upgrade herdr-web` |
| `go install` | runs `go install …@latest` |
| release archive / install.sh | **does it itself**: download → verify sha256 → write a temp file in the same directory → atomic `rename` |

Package-manager installs are not touched directly because editing things inside `node_modules` / `Cellar` gets overwritten the next time that package manager runs — wasted effort.

Three things about the self-managed path are deliberate: **verify before landing** (a `checksums.txt` mismatch aborts everything), **the temp file must be in the same directory** (a cross-directory `rename` gives EXDEV), and **the old file is not deleted** (on unix, renaming over a running executable is allowed, the old inode is still held by the process, so the current process runs safely until it exits).

**Replacing the file is not the same as replacing the running process.** Only a restart takes effect, and a restart kills every terminal session in use — so it is not done by default, only with `--restart`.

New-version notices appear in three places:

- the last line of the **startup banner** (from cache, so no request is made on the startup path — on a slow network that would turn into "startup hangs for ten seconds");
- a strip at the top of the **admin page**, with the current version, the command to run and a link to the release notes;
- while the service is running, a daily background check writes one line to the **log** when a new version appears (once per version, not daily nagging).

Checks go to GitHub Releases' anonymous API, with results cached in `~/.herdr-web/update.json` (on disk, so frequent restarts do not mean checking every time; failures are stamped too, so a machine with no connectivity does not eat a timeout on every start). `HERDR_WEB_UPDATE_CHECK=0` disables the automatic check entirely — with it off, this process makes no outbound requests at all. Local builds (where `version` reports `dev`) neither check nor nag.

## Security

**This thing amounts to a shell over HTTP** (the outbox alone can make an agent run commands, even
without a PTY), so the door is designed on that premise. What is implemented:

- **Pair each device once.** A one-time code is exchanged for a per-device credential in an
  `HttpOnly; SameSite=Strict` cookie; the server **stores only sha256** — the agents on this machine
  read untrusted content all day, so the credential file being read by prompt injection is a daily
  risk, not a theoretical one.
- **Credentials bind to a device, not an IP.** Changing networks costs nothing; trusting an IP loses
  both ways.
- **No secrets in URLs.** `?pair=` is exchanged for a cookie and scrubbed with a 302, so bookmark
  sync and screenshots stop being leak channels.
- **Revocable.** `herdr-web revoke`, or Settings → Devices; the next request gets 401.
- **Only someone at the machine can produce a pairing code** — no path on the web issues one. That
  terminal is the only out-of-band factor in the system.
- **Refuses to start when exposed without TLS.** A Host allowlist blocks DNS rebinding; Origin +
  `SameSite=Strict` + a custom header make three layers against CSRF; guessing a pairing code gets
  exponential backoff, per-IP lockout and a global breaker.
- **Passkeys are the second factor** (the server stores only the public key). With one registered,
  moving to a new device does not require going back to the machine, and session credential lifetime
  can drop from three months to one day.

→ Threat model, the reasoning behind each choice, what is not built yet: [SECURITY.md](SECURITY.md)
　Reaching it from the internet (frp / tunnels) and the four TLS tiers: [DEPLOY.md](DEPLOY.md)

## Documents

Everything below is in Chinese — that is where the "why" lives.

| What you want | Where |
|---|---|
| Outbox: why a separate box, how images work, measured polling latency | [OUTBOX.md](OUTBOX.md) |
| Reading the screen: scraping the input line, scraping what the agent said | [COMPOSER.md](COMPOSER.md) |
| herdr socket API semantics, verified by hand | [HERDR-API.md](HERDR-API.md) |
| The whole phone / tablet layer (gestures, keyboard, dock, top bar, notices, clipboard) | [MOBILE.md](MOBILE.md) |
| Security design and threat model; the rules on the file-serving route | [SECURITY.md](SECURITY.md) |
| Where to run it, public access, TLS tiers | [DEPLOY.md](DEPLOY.md) |
| Getting a DNS token from each provider and the scope it needs | [DNS.md](DNS.md) |
| Read before changing code (layout, releasing, colours, the silent traps) | [CLAUDE.md](CLAUDE.md) |

MIT.
