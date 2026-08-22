# herdr-web

<p align="center">
  <img src="assets/logo.png" alt="herdr-web" width="96" />
</p>

<p align="center">
  <b>English</b> · <a href="README.zh-CN.md">简体中文</a>
</p>

A terminal in your browser, built for running [`herdr`](https://github.com/zbysir/herdr). One Go binary with the frontend baked in. Works on phones.

> **Voice compose** — dictate on a tablet, edit what you got wrong, then hand the whole paragraph to an agent pane — is the point of this project. See [Outbox](#outbox-voice-compose) below. Three companion documents (Chinese): design trade-offs behind the outbox in [OUTBOX.md](OUTBOX.md), the pitfalls of scraping an agent's input line in [COMPOSER.md](COMPOSER.md), and the herdr socket API semantics we verified by hand in [HERDR-API.md](HERDR-API.md).

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

Once connected it **types `herdr` for you**. To type something else, or nothing: `HERDR_WEB_ONCONNECT` (set it to an empty string to stay in the shell). Adding a path segment to the URL (`/work`) gives you **a different herdr session** — see [One URL, one session](#one-url-one-session-name). The old "run herdr" button in the top bar is gone: with autotyping it earns its place less than once a day, and the soft key bar ships a preset for it if you want one.

**The admin page is at `http://127.0.0.1:<port+1>/`** (also in the startup banner): certificate status, one-click issue/renew, generated DNS `.env` snippets, pairing codes, device kicking. It is **bound to loopback and does not exist on the public internet**, so it needs no login — anything that can reach it already has your shell. Why not "an authenticated page on the main server": authentication is a control that can fail, unreachability is a property; and the admin page must not depend on the very certificate it exists to fix (a broken certificate would lock you out of the page that repairs it).

## One URL, one session (`/{name}`)

A path segment is **a different herdr**:

```
https://herdr.bysir.top/          default session (old behaviour, types `herdr`)
https://herdr.bysir.top/work      types `herdr --session work` — created if it doesn't exist
https://herdr.bysir.top/scratch   another one, unrelated to the above
```

`herdr --session <name>` is herdr's own **named persistent session**: its own server process, its own workspaces / tabs / panes. So bookmark `/work` and `/scratch` and two browser tabs are two working contexts that survive closing the browser (the session is persistent; disconnecting the page only disconnects a client).

Things to know:

- **The outbox and the pane list follow the URL.** A named session has its own socket (`~/.config/herdr/sessions/<name>/herdr.sock`, the one `herdr session list --json` reports), so every request from the page carries the session name. This is the one genuinely dangerous corner of the feature — using the default session's socket to post into a pane picked on a `/work` page would **silently deliver your words into another herdr**, with nothing looking wrong on either screen. That is why the server **does not fall back** on an invalid name; it errors out instead of quietly using the default session.
- **Names are `[A-Za-z0-9._-]`, must start alphanumeric, 40 characters max** — the name is interpolated into a command line typed into a login shell, and into a socket path. Invalid names are reported on the page rather than silently redirected to some other session.
- **`HERDR_WEB_ONCONNECT` does not apply to session URLs** (including when set to the empty "type nothing" value): naming a session in the address bar is more specific than a global default. `/` keeps the old behaviour.
- **The current session name is shown at the left of the top bar** (and in Settings → Terminal, together with that session's socket path). The default session shows no label — no label means default.
- One process watches at most 16 sessions at a time (each carries an agent-status subscription). Beyond that it says so; a restart clears it.
- "Add to Home Screen" stores the manifest `start_url` (`/`), so the home screen icon opens the default session. For an icon that goes straight to `/work`, use a browser bookmark.
- `herdr session list` / `stop` / `delete` manage these sessions from a terminal; herdr-web only opens and attaches.

## Local shell only

To reach another machine, ssh from inside herdr — herdr does that itself, so this layer does not implement host management or key storage (which would drag in key files on disk, `ssh-keygen`, `~/.ssh` scanning, ssh_config import), and along with it the whole "the browser can touch your private keys" attack surface disappears.

## Outbox (voice compose)

The strip with a textarea at the bottom of the page is the outbox; the ✎ in the top bar toggles it and it is **on by default**. You dictate or type in there, fix what came out wrong, then hand the whole paragraph to one of herdr's panes. It shares **one dock** with the soft key bar (one border, one width — see [The bottom dock](#the-bottom-dock)).

Why a separate box instead of talking straight into the terminal: a terminal is a byte stream with no selection semantics, so an IME can only pour characters into it. "**Select the words you got wrong and say them again**" needs a real editable field — a text model, a selection, and an IME commit that replaces the selection. xterm.js's hidden textarea does not count; it only turns keys into bytes and sends them.

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

**How images actually work**: herdr's socket API has no concept of an image — text is all you can post. But claude and codex both read image files off disk (verified: both described a 320×200 test image, red left half, blue right half; codex even prints `Viewed Image`). So "upload" means: store it under `~/.herdr-web/uploads/` on the machine running herdr, then hand over the **absolute path** as text and let the agent open it.

**Uploading does not require the outbox**: bind a key to `act:img` (Settings → Soft keys, the "Web actions" preset group ships a 🖼 one; position and label are yours to change), and **the whole page accepts a paste** (`⌘V` or long-press paste — if the clipboard holds an image it is uploaded directly). Where the path lands depends on whether the outbox is open: open, it is appended to your draft (so you can keep dictating and post the lot); closed, it is **typed straight into the terminal**, i.e. into the current pane's input line for you. Most of the time you just want to throw a screenshot at the agent, and opening the outbox for that is not worth it.

The entry point lives on the soft key bar rather than the top bar: the top bar already carries eight buttons on a tablet, and the soft key bar is by definition "a row of actions you arrange yourself" — whether this key exists, where it sits and what it is called all belong to you.

Pasting is handled by a **capture-phase** listener on `window`: it has to beat xterm's hidden textarea, otherwise an image-only clipboard makes xterm paste an empty string into the terminal. A paste that lands inside the outbox textarea is let through and handled there (that is where it can be inserted at the cursor).

Phone photos are downscaled to a 2400px long edge in the browser first, and iPhone HEIC is converted to PNG/JPEG (agents cannot read HEIC). The server identifies the type by **magic number** and accepts only png / jpg / gif / webp, so renaming the extension or lying about content-type does not get through. 25 MB cap. Uploads are never garbage collected; clear `~/.herdr-web/uploads/` yourself when it piles up.

The status line always says where this paragraph is going; `⟳` means it is following focus, and hovering shows the polling interval in use.

### Polling, not push

herdr has an `events.subscribe` channel, but a working agent produces screen-refresh volumes of it, so this uses polling: every `HERDR_WEB_POLL_MS` (500ms default) it asks "which pane has focus, and what is in its input line".

**Measured latency from switching panes to the textarea updating** (same machine, 8 samples):

| Poll interval | Fastest | Median | Slowest |
|---|---|---|---|
| 200ms | 138ms | 318ms | 550ms |
| **500ms (default)** | ~300ms | ~500ms | ~800ms |
| 1200ms | 408ms | 794ms | 818ms |

The floor is the cost of one sync, because every herdr call can land on a ~100ms tick (see "the 100ms wall" in [HERDR-API.md](HERDR-API.md)). To try a different feel temporarily, add `?poll=200&push=400` to the URL; it overrides what the server hands down.

Things worth knowing:

- **The moment the box holds something you wrote, the target is pinned** to the pane you were aiming at; it goes back to following focus when the box is empty. herdr moves focus on its own when agent state changes, and without the lock "what you wrote for A gets posted into B". Auto-pulled content you have not touched does not count as a draft — switching panes there still follows along.
- **"Two-way" only makes sense for panes with a real input line** (claude / codex). An ordinary pane might be running vim or some picker, where characters are **commands**, not text. Also do not type into that pane by hand while it is on — the local→remote direction is essentially fighting a byte stream for the buffer.
- **Posting is refused while the remote side has a picker or confirmation open** (if it cannot be cleared it will not post, otherwise you get "leftovers + new text" submitted together). Press `Esc` in that pane and post again.
- **Nothing is posted when no input line can be recognised on an agent pane** either (a pager, an editor or some full-screen widget is up). "Pull back" also stays quiet then — unrecognised is unrecognised, and it will not fall back to "the last line on screen". Shell panes never have a readable input line and are unaffected; posting works as usual.
- The socket is on **the machine running the herdr server**. For now that is the local one (or whatever `HERDR_WEB_SOCKET` points at).

## Pane list (how you switch panes on a phone)

The ▦ in the top bar (or an `act:panes` key on the soft key bar) opens a list of panes grouped by workspace, one row each. **Tap a row and you are there, zoomed full screen.** Above the list you can filter (tab name / title / path / pane id), show only panes running an agent, and turn "zoom" off (a tap then means "move focus, leave zoom alone").

The panel is **tightened to phone scale**, deliberately, in three places:

- **No title bar.** A whole row for "Pane list" plus an ✕ is 44px of blank on a phone — and what this panel is is obvious from its content. The ✕ moved to the right end of the filter row (which is why `title` on `ui/panel` is optional now: leave it out and the bar is not rendered; panels that need a heading still pass one).
- **No refresh button; the list refreshes itself.** While the panel is open it re-fetches every 4s (never while the tab is hidden), so the state dots, the "minutes ago" column and newly spawned panes are all live. It used to fetch once on open, which meant everything you were looking at was a snapshot — and this is the panel you open to see *who is waiting for you*, where a frozen state is worse than none. A refresh button just hands that job back to the user, from the most mis-tappable spot in the row.
- **Rows start at 36px** (they were 44). A row already holds two lines of text (≈30px), so 44 was padding for its own sake; the whole full-width row is the hit area, 36px is not hard to hit, and a phone screen fits two or three more panes.

**Why it exists.** The soft key bar sends **keys**, and keys can only express **relative** navigation: next tab, one pane to the right. "Zoom `w5:p3`" is not expressible as a key — you have to decompose it into a walk, and every screen along that walk is exactly the unzoomed multi-pane layout that is unreadable on a phone. The scale measured here is 48 panes / 38 tabs / 4 workspaces; one trip is four blind legs of workspace → tab → pane → zoom.

herdr's socket layer is **addressed by pane_id**: `pane.zoom` with a `pane_id` crosses workspace + tab + pane in one call (focus follows across workspace and tab; no `workspace.focus` then `tab.focus` needed — verified). So the UI is simply a row you tap.

**It is an index, not a second interface.** After the tap you are looking at the same herdr terminal — the page is attached to the whole TUI, so when herdr moves focus the picture follows by itself, and every keyboard habit is unchanged. That is deliberate: "take over one pane on mobile and build a graphical pane manager" could be made flashier, at the cost of **two sets of habits** and a second source of truth. So this panel answers "where to", and does not create, rename or delete anything.

On a phone there is a third entrance, and it is the handiest one: **tap the `switch` button in herdr's own mobile top bar** and this list is what opens (on by default — see [Tapping herdr's switch opens ours](#tapping-herdrs-switch-opens-ours-on-by-default)).

### One tap has to jump

Tapping a row while the on-screen keyboard was up used to take **two taps: the first only dismissed the keyboard, the second jumped** (seen on a real phone). Two causes stack up:

- All three entrances deliberately **do not let the browser move focus** — the soft key bar calls `preventDefault` on mousedown, and the touch layer swallows `touchstart` entirely (otherwise a swipe turns into a text selection, see [Phones](#phones)). So when the panel floats up, the outbox / terminal input is still focused and the keyboard still owns half the screen.
- That one tap therefore **first** takes focus away: `--vvh` follows visualViewport, the panel reflows, and the row under your finger has moved — so the browser dispatches the click somewhere else, or not at all.

Three things are plugged now:

- **Opening the panel blurs the focused input** (the web has no "hide keyboard" API; blur *is* hiding the keyboard). The two file-browsing surfaces (directory panel / viewer) are opened by tapping in the terminal too, so they dismiss it on open as well.
- **The filter box no longer autofocuses on touch** — the test changed from "phone portrait (< 440px)" to "is there a fine pointer", because tablets used to autofocus too, which pops the keyboard straight back up.
- **A row commits on `pointerup`, not on `click`**: touch and pen pointer events have implicit capture, so whichever row got the `pointerdown` also gets the `pointerup`, however much the layout moved in between. Mouse and keyboard still go through `click` (on the desktop a click is never lost), and a finger that travels more than 10px counts as scrolling the list, not a tap. Rows have to identify the element themselves rather than lean on the global fallback below: a lost click is one failure, a click **dispatched to the neighbouring row** is the other, and jumping to the wrong pane is worse than nothing happening.
- **The pane id is captured at `pointerdown`** and used on release, instead of the `p.id` of the current render: the list re-fetches every 4s, and after a re-render the same DOM node carries another pane's handlers — which lands you on a pane you never tapped.
- **While the panel is open the order is frozen**: "priority" sorts by state bucket, and on a machine with a dozen agents states change every few seconds, so the third row you were aiming at is somebody else by the time your finger lands (half of the "tapping does not reach the right pane" report was this). Only the order freezes — state dots, the "waiting" chip and the time column keep updating; new panes are appended; changing the sort or the filter, or reopening the panel, re-sorts.

Buttons elsewhere (every panel's ✕, the toggles, the tabs) rely on the global fallback — see [The first tap only dismisses the keyboard](#the-first-tap-only-dismisses-the-keyboard).

### Sorting (switchable)

The button at the top cycles it; the choice is remembered locally:

| Order | Rule |
|---|---|
| **Priority** (default) | By how much it wants your eyes: **waiting on you > finished > running > idle > not an agent**, and within a tier by most recently changed |
| **Grouped** | By workspace, then the original tab / pane order — the same thing you see inside herdr |

Status dot colours: **red = waiting on you, green = finished, yellow = running**, grey for idle. Running is yellow rather than green to match herdr's own agents column; green is reserved for "finished" (the universal convention: good news). Only idle gets no colour — a column where every dot is coloured has no emphasis left.

`Running` gets its own tier. It started merged with `idle` (the reasoning: neither needs you, and there is no objective answer for which comes first, so let "recently changed" decide). In practice that was wrong: the one yellow dot that is actually running gets buried under a dozen idle ones, and it is precisely the row you most want to spot. `Waiting` and `Finished` additionally carry a small label on the row; the other states only have the dot — a label on every row is no emphasis at all.

**Within a tier the order comes from `state_change_seq`** (herdr's global counter, bumped on every agent state change), not from time. Because there is **not a single timestamp in herdr's API**: `agent.list` only gives that counter, and events carry no time either. The counter is always right, so the ordering is always right.

### The "3 minutes ago" column

Times are **stamped by herdr-web itself** as it watches state changes (`internal/agentwatch`: subscribe to `pane.agent_status_changed`, record `time.Now()` on arrival). Consequences:

- **The column is empty on a first run** and fills in as changes happen. Empty is the truth — that change happened before anyone was watching, and inventing a time would be much worse than leaving it blank. A line under the list says so.
- **Stamps are keyed by `terminal_id`** (`~/.herdr-web/agent-seen.json`), so restarting herdr-web (an upgrade, a config change) does not lose them. It cannot be keyed by `pane_id`: that is a positional number inside herdr, reassigned to someone else as soon as a pane opens or closes, and you would end up attributing one pane's history to another. After a herdr restart every terminal id is new, so old records simply do not match — and only currently-live terminals are written back, so the file never grows fat.
- **State is not persisted**, only time. Persisting state means that after a restart, comparing against the old state records "changed while we were down" as "changed just now".
- When the subscription is not connected (no herdr server running), a line under the list says so, otherwise an empty column looks broken.
- Display is compact (`3m` `2h` `4d`; under 45 seconds is "just now"), full timestamp in the `title`. That column is a few dozen pixels wide on a phone — "3 minutes ago" does not fit.

A few details:

- **Pane ids are shown on phones too.** When a tab is split into two panes, both rows carry the identical tab label and cwd (seen in the wild); the id is the only thing that tells them apart.
- The second line is the **agent's own session title** (Claude Code's "image recognition" and the like), falling back to cwd — a shell pane's title is just `user@host:path`, which is worth less than the path.
- **Focus is not stolen after a jump** on phones (that would pop the system keyboard, and you mostly jumped there to look); on wide screens it is.
- The post target takes care of itself: "follow herdr's current pane" moves along. With a local draft the target stays pinned to the original pane — words written for A should not go to B just because you looked at B.
- The "zoom" toggle is remembered locally. On by default on phones: a tiled multi-pane layout is unreadable there, so arriving without zooming is the same as not arriving.
- `zoomed` is a property of **the whole tab**, not of a pane (herdr always zooms the focused one). So "this tab only has one pane" comes back as `zoomed:false`, which is not a failure — the UI says so explicitly.

## Notices: a card when an agent changes state (top right + badge)

When an agent stops to wait for you (or has just finished), a card appears in the top right **carrying what it said**, and a badge with the count lights up on the ▦ in the top bar. Tapping the card jumps to that pane (zoomed — the same action and the same "zoom" toggle as tapping a row in the pane list).

**Why this exists.** Only one of herdr's panes is visible in the page (on a phone, only the zoomed one), while a dozen agents can be running at once. Finding "the one waiting for me" used to mean opening the pane list and scanning it — so an agent stuck on a y/n for half an hour was routine.

**The card has to carry the actual text**, not just "something changed": with only "something changed" you still have to jump over to find out whether it needs you, which is the same as no notice at all. herdr's API has no "what did the agent last say" field (same story as "no input line content field"), so this text is **scraped off the screen**.

### When it fires

| Change | Fires? | Why |
|---|---|---|
| → `blocked` (waiting on you) | **Yes**, and it does not auto-dismiss | It really is sitting there waiting. A card that floats away by itself puts you back to "no idea who is waiting" |
| → `done` (finished) | Yes, dismissed after 12s by default | An explicit "your turn" from herdr, whatever it came from |
| `working` → `idle` (finished) | Yes, same | `idle` is *resting*, so it only counts as "finished" when it came from `working` |
| → `working` | No | That is almost always what you just posted — an echo |
| `blocked` → `idle` | No | You just answered and it is about to start; reporting "finished" would be a lie |
| Posted something and hit **Esc to cancel** | No | See below — herdr sees a clean `working → idle`, so it is told apart by content |

**`done` fires no matter where it came from — requiring `working` first was a bug.** On a real device, poking an agent with a one-line "say hi" gives `idle → done` with **no `working` in between** (herdr's screen detection is conservative and a short task never registers as running). The first version required `working → done`, so short tasks produced no notice at all — every unit test stayed green and only the end-to-end poke found it.

**Nothing new on screen means nothing is announced.** Post a line and hit **Esc**, and herdr reports a clean `working → idle` — identical to finishing. claude leaves **no "interrupted" marker on screen** either (captured on a real device, `testdata/idle-interrupted.txt`: the last `⏺` block is still the previous answer and your line is put back in the composer). So the first version announced a **stale answer** labelled "finished". The test that actually works is "did anything new appear": the scraped text is compared with the last one for that terminal and an identical one is dropped. When a terminal has no previous text (herdr-web just started), the first `→ working` reads one screen to seed the comparison — once per terminal, so the 100ms `working↔idle` flicker cannot make it read repeatedly.

**A state has to hold for 2.5 seconds to count.** claude / codex flicker back to idle while working, and without debouncing one long task produces a dozen false "finished" cards; `pane.read` snapshots also lag by a frame, so reading immediately catches the tail of the previous task. If the state changes again within those 2.5 seconds (`idle → blocked` is a common pair), the **last** one is reported, as a single card.

**State comes from polling with events as an accelerator — not from events alone.** Measured on a real device: herdr only pushes `pane.updated` for panes that are **visible**. Poke an agent sitting in a background workspace and `pane.get` reports `working` after 6s and `done` after 13s, while the event stream's **first event arrives 40 seconds later**:

```
 0.1s  [poll pane.get] done      ← posted "say hi"
 6.2s  [poll pane.get] working
13.2s  [poll pane.get] done      ← finished after 13s
40.8s  [event pane.updated] first event finally arrives
```

Background panes are exactly the ones notices are for (the one you can see needs no notice), so a `pane.list` poll every 3 seconds is the floor and events only make the pane you are looking at near-instant. One `pane.list` is a single socket call of a few dozen KB — far lighter than the outbox's three calls per 500ms while it is open.

**Nothing is replayed when herdr comes back after being down.** Reconciling at that moment finds a screen full of panes whose state differs from before the outage, but those changes happened "sometime in the last half hour"; firing a burst of "just finished" would be inventing timestamps — the same reason the "3 minutes ago" column refuses to invent times for old changes. The cost is that a change inside the 800ms resubscribe window is missed.

### How the text is scraped

Two modes by state — the same screen of text, but "waiting on you" and "finished" live in completely different places:

- **Waiting on you**: the question is in the block at the **bottom** of the screen — a small heading like `☐ Install the service?`, or a `╭ … ╰` box. So it takes everything from the last `☐` / `╭` downwards, stopping at the key hints (`Enter to select · ↑/↓ …`). **Box drawing is stripped, options are kept** (the `❯` in `❯ 1. …` marks the current choice, which is information). When there is no dialog on screen (`agent_status` cannot tell — see [Limits](#limits)) it falls back to the "finished" scraper.
- **Finished**: take the last `⏺` block — but **skip the ones carrying `⎿`**. `⎿` marks tool output (`⏺ Searching for 1 pattern…` followed by a screen of `⎿  $ cd …`), which is the log of it working, not something it said to you. Caught on a real device: at the instant the state flipped to idle, the bottom of the screen happened to be a run of shell commands, and the first version pushed out `cd /private/tmp/…` as "the result". Walking up to the first non-tool block is what gets you "Go side done (tests pass). Writing the frontend now." It stops at the spinner (`✻ Baked for 20s`), the recap (`※ recap: …`) or the `────` / `❯` around the input line. Agents like codex with no `⏺` at all degrade to the last few lines.

Anything over 12 lines / 600 characters is truncated — keeping the beginning, since the first sentence of an answer carries the most — with a trailing `…`. **If nothing can be scraped it reports the state alone rather than manufacturing a sentence**; inventing content is far worse than leaving it out.

The scraper is adapted from `extractResult` in [herdr-sight](../herdr-sight) (which only has the "finished" case, because it is about collecting results from a finished task; here the case that most needs a notice is "waiting on you"). `internal/agentwatch/testdata/` holds **real captured screens**, and changing this code means running `go test ./internal/agentwatch/` — every rule was chosen against the actual shape of those screens, and changing one from imagination breaks the scrape silently (the symptom is a card containing a lone `❯` or a piece of the status bar, with nothing in the logs).

### The cards

- **Tap = jump there** (zoomed; same action and same "zoom" toggle as a pane list row), and the card dismisses itself afterwards — you already went, leaving it up only blocks the view.
- At most **3** stack in the corner; the rest collapse into one line: "N more · open the pane list".
- **How long they stay is a setting** (5s / 12s (default) / 30s / 1min / stay put). "Waiting on you" ignores it and always stays.
- **"Waiting on you" sorts to the top**, then by recency. Not purely by time, because the two kinds have different lifetimes: "finished" leaves after a dozen seconds while "waiting on you" stays, so time ordering lets a fresh "finished" push down the one that is actually waiting — the only one that needs you to do anything. When the stack is full, "finished" cards are dropped first.
- The whole stack gets out of the way while a panel is open: those overlays share the same corner.

### The badge (how many are still unread)

The ▦ in the top bar carries a **number**: how many notices you have not looked at. The `act:panes` key on the soft key bar carries the same one — on a phone the top bar collapses the moment the keyboard is up, which is exactly when you are talking to an agent and most need to know another one is waiting.

A number rather than a dot: a dot only says "something is there", while *how many* is actionable — two agents waiting and five agents waiting are different decisions. Over 9 it reads `9+`.

**What counts as seen:**

| Action | Badge |
|---|---|
| **Tap a card to jump** | Everything unread for that pane clears (the number drops). **Per pane, not per notice** — you are looking at that agent's current screen, which includes what it said earlier |
| Tap a system notification | Same |
| **Open the pane list** | Everything counts as seen (that is where these changes are meant to be read, in one scan) |
| Dismiss a single card (×) | **Not** seen — that only means it was in the way |

With two agents waiting, tapping into one takes the badge from 2 to 1 rather than clearing it; it goes away when you have been into both.

**A refresh does not lose it** (`localStorage`): on a phone, a badge that vanishes on reload makes the whole feature pointless. What is stored is a **watermark** (seq — everything below it is seen) plus **the handful above it you have already seen**: a watermark alone cannot express "read #7, not #6". Once nothing is unread the watermark moves up and that list is cleared, so it stays a few entries long. Named sessions track their own (that is a different herdr).

**It can be turned off** if it bothers you: Settings → Terminal, "badge on the panel icon". That only stops drawing the badge; the cards in the corner still appear. Stored locally, per device. To turn the whole feature off, that is `HERDR_WEB_NOTICE_MS=0` on the server side.

**Opening the page lights the badge but replays no cards**: those changes may be half an hour old, and showing them as if they just happened is inventing time.

### System notifications

Settings → Terminal → "System notifications" (**you have to tap it yourself** — browsers only hand out the permission prompt inside a user gesture). After that, new notices go out as browser notifications **while you are not looking at the page**; tapping one brings the page back to the front and jumps to that pane.

- **The test is "are you looking at this page", not `document.hidden`.** On macOS, switching to another app only unfocuses Chrome — the tab still counts as visible and `hidden` stays false, so a `hidden`-only test never fires in the most common case (that was the first version, and the report was "system notifications simply never show up"). The check is `document.hidden || !document.hasFocus()`. There is a "notify me even while I am looking at this page" switch for people who want both.
- **A "Test it" button** ignores both the switch and the focus test and fires one right away. Where it is stuck (permission? focus mode? iOS not installed to the home screen?) cannot be guessed — one tap answers it, and the reason comes back as a toast.
- **One notification per agent** (the tag is the `terminal_id`), replacing the previous one instead of piling up in the notification centre.
- **On phones**: Android Chrome works in an ordinary tab. **iPhone / iPad must "Add to Home Screen"** and open it from there (a page in a Safari tab cannot get notification permission; iOS 16.4+).
- Turning the switch on registers a `sw.js` (**and only then**). Both Android Chrome and iOS require `ServiceWorkerRegistration.showNotification()` — the `new Notification()` constructor is unavailable there. That worker **caches nothing** (it does not even listen for `fetch`); it only handles notification clicks. A worker that intercepts requests on a terminal page buys nothing and costs you "I changed it and nothing happened" debugging sessions.
- **Close the page and they stop.** Real "even with the browser closed" needs Web Push (VAPID keys, stored subscriptions, the server pushing) — a whole other stack, not built.
- Over plain http (not https, not localhost) browsers refuse the permission; the switch greys out and says why.

### Limits

- `agent_status` cannot tell that a dialog is open (measured: the same picker reported `idle` once and `blocked` another time, see [HERDR-API.md](HERDR-API.md)). So occasionally a question is announced as "finished" — the card still carries the question, only the state label is wrong; the jump is right.
- The scraping rules follow claude's current UI. After a redesign they may extract something odd; state and jumping still work, and the fix is a fresh capture in testdata plus a rule adjustment.
- To turn the whole thing off: `HERDR_WEB_NOTICE_MS=0` (the frontend stops polling and stops drawing the badge).

## File browsing (looking at what the agent generated)

The agent says "the plot is at `/tmp/plot-3.png`" — **tap that path and look at it**.

The 📁 in the top bar (or an `act:files` key on the soft key bar) is the fallback directory browser, but it is not the main entry point. The path in the terminal is.

### "The image is not under the current workspace"

Not solved, because it should never have been a problem. Three routes cover the ground:

| Situation | Route |
|---|---|
| The path is on screen (the vast majority) | **Tap it.** Absolute paths open directly; `./out/a.png` resolves against **that pane's cwd** (which `pane.list` provides) |
| The path is not visible, or you want to look around | The 📁 panel. It starts from "every pane's cwd + the upload directory + home + temp + recently visited", and `..` walks all the way to `/` |
| The file is somewhere nobody would guess (`/var/folders/xx/T/…`) | The box at the top of the panel: **paste an absolute path and open it** |

So **there is no boundary by default**: anyone who can open this page already has a login shell (`/pty`) and can `cat` anything — an allowlist would not stop them, it would only get in the way daily (agents write to `/tmp`, `/var/folders/…` and `~/Downloads` constantly). If you want a boundary, set `HERDR_WEB_FILE_ROOTS` (that one is a real jail); to remove the feature entirely, `HERDR_WEB_FILES=0`.

### What it can show

- **Images**: png / jpg / gif / webp, identified by **magic number** (a renamed extension does not fool it). Tap to toggle between "fit" and "actual size". The top right can copy the path, open the containing directory, open it in a new tab (where a long press saves it to the camera roll) and **hand it to the agent** (insert the absolute path into the outbox, or type it into the terminal — exactly the same model as uploading).
- **Text / code**: shown as-is, truncated past 512 KB with a note.
- **Anything else**: download only.

### The link route: `/_f/<ticket>`

Images are served over a short-lived link that carries **no cookie**. It has to be: cookie-authenticated requests on `/api/*` require a custom header (the third CSRF layer), and `<img src>`, "open in new tab" and iOS "long press to save" **cannot set headers** — through `/api` they would all be 403.

The ticket is a capability, not an identity: **bound to one absolute path**, expiring in 15 minutes, signed with a key generated at process start and **kept in memory only** (a restart invalidates every ticket, and no long-lived secret lands on disk). The trade-off, stated plainly: the ticket is in the URL, so it enters browser history and shows up in screenshots, and whoever holds that string can read that **one** file for 15 minutes.

### Four hard rules on that route

None is optional, because what comes out is **a file the agent wrote**:

1. **Never serve content as `text/html`.** Same-origin HTML is a springboard that can call `/api/herdr/say` (the cookie is HttpOnly, but it does not need to read it — the browser attaches it): the agent writes an html file, you open it, and herdr is theirs. Only the four image types confirmed by magic number are served inline; everything else is `application/octet-stream` + `attachment`. **SVG does not count as an image** (a scriptable "image") and lands in the attachment tier.
2. **Regular files only.** Opening `/dev/zero` is an infinite stream and `/dev/rdisk0` is worse. Devices / sockets / pipes are still listed in a directory (a listing should tell the truth), they just cannot be opened.
3. **A per-response CSP `sandbox`**, overriding the global one. If either of the first two ever breaks, this layer is still standing.
4. **With `FILE_ROOTS` set, the prefix check runs after `EvalSymlinks`** and compares against `root + separator`. Drop either half and it silently passes (a symlink can point outside, and `/home/user` would admit `/home/user2`).

### Path detection pitfalls (all in `web/src/term/paths.ts`)

- **Paths get broken by wrapping.** In an 80-column pane a long path spans two rows, so logical lines (the `isWrapped` chain) are reassembled before matching.
- **Reassembly cannot use `translateToString`.** With CJK text its character count does not match the cell count, so mapping an index back to coordinates shifts the whole line — the underline lands on half a word and tapping picks up a different span. Cells are read one by one instead.
- **CJK punctuation has to terminate a path.** Measured: the agent writes 「生成好了 /tmp/a.png。相对的 …」 with no space between the path and the text — without stopping at `。`, the whole `a.png。相对的` is taken as a path, which opens to "no such file" while the underline looks perfectly fine. CJK characters themselves are still allowed (`/tmp/图表.png` is a legal filename).
- **A bare relative path must carry an extension.** Counting slashes is not enough: `2026/08/21` has two, and "two segments is enough" would draw a link on it. Same for `and/or` and `100/200`. Rooted paths (`/usr/local/bin`) are exempt.
- **A path already truncated by `…` gets no link.** Unrecognisable is unrecognisable; do not guess a shorter one and then report "no such file".

## Settings panel

The ⚙ at the right end of the top bar holds everything, in four pages: **Terminal** (font size / light-dark, kitty protocol / Option as Meta / copy on select / synchronized output, tapping herdr's switch opens the pane list, fullscreen when the keyboard comes up, the badge on the panel icon, system notifications, how long notice cards stay, plus a line of backend environment), **Top bar** (next section), **Soft keys** (the one after), and **Devices** (who has paired, sign out, kick).

The font size / light-dark controls at the top of the Terminal page are the same actions as the matching icons in the top bar, not a second copy of the state — and those icons can be dragged off the bar, in which case this page is where they still live.

This used to be three independent little panels with three icons in the top bar — which is crowded on a tablet, and "settings" split three ways means finding anything depends on remembering which icon is which. **The ⚙ that used to sit in the bottom right of the soft key bar (a shortcut to the Soft keys page) is gone too**: it lived permanently at the end of the key row competing for space (especially in portrait), while editing keys is a thing you do once — going through the top bar is not a burden.

## Top bar (drag the buttons you want)

**Which icons sit in the top bar, and in what order, is configuration** — edit it under Settings → Top bar: the bar is on top, the buttons not on it are below, press and drag one up to add it, drag it down (or hit its ✕) to remove it, drag within the bar to reorder. A chip in the library can also just be **tapped** to append it — "add one" should not require learning to drag first.

Same shape as the soft key bar (**library below, bar above, drag into it**) and the same gesture code (`web/src/lib/chipdrag.ts` — two arrangement editors each with their own drag implementation is how one action ends up with two different feels). On touch it takes a 250ms press to pick a chip up: the page has to scroll, and the chip itself is the handle, so without the press there is no way to tell "scroll the page" from "drag this".

It lives **on the server** (`~/.herdr-web/topbar.json`, see `internal/topbar`), so phone / tablet / desktop share one bar — same reasoning as the soft keys. What is stored is **a list of button ids**, not button definitions: what a button looks like (icon, name) is built in (`web/src/components/topbarItems.tsx`). The server keeps a whitelist that has to match that catalogue exactly, with a test watching both (`TestActionsMatchJS` in `internal/topbar`) — adding a button on only one side fails in a way that is hard to trace: it drags fine in the editor and the save reports "unknown button", or it saves fine and the bar cannot draw it.

**What you can put there**: pane list / files / outbox / soft key bar / system keyboard / upload image / pull clipboard / paste into terminal / smaller font / bigger font / light-dark / fullscreen / settings. The four in the middle used to be soft-key-only (the `act:` ones); they work in either bar now, and the two bars are separate configs.

Three edges:

- **The ⚙ cannot be removed** (no ✕ on its chip, dragging it to the library is refused, and the server puts it back on save). It is the only route back into this configuration, and the configuration travels with you: delete it on the phone and it is gone on the desktop too.
- **A bar that does not fit scrolls sideways** — it never wraps and never hides anything. Font ± and light-dark used to be hidden below 440px by CSS (seven icons do not fit in 393px); that rule is gone, because "I dragged it on and cannot see it" is the hardest behaviour to explain, and sideways scrolling is what the soft key bar has always done on phones.
- **The left end (status dot, session tag, "Connect") is not part of the configuration**: those are not buttons, they are "which herdr this page is attached to and whether it is connected" — the only place that says so, and being able to delete it would just read as a broken page.

## Soft key bar

Phones have no Ctrl key, and herdr's `ctrl+b` prefix depends on one. The keys live **on the server** (`~/.herdr-web/softkeys.json`), so phone / tablet / desktop share one set, edited in Settings → Soft keys.

**One row or two is a setting** (server-side, travelling with the config), not a guess based on "is the second row empty" — an empty second row and "I only want one row" are different things. The two rows **scroll horizontally independently**: a phone fits four or five keys per row, so put the common ones on the first row and the rest on the second; that beats a dozen keys in one long queue, because your finger knows which row it is on and scrolling one does not drag the other. Switching back to one row **appends the second row's keys to the end of the first** (the server computes it the same way; "stored but not shown" is the most annoying state there is).

The editor has two layers, and so does what gets persisted (`{rows, keys, bar}`):

- **`keys` = "My keys"**: the **definitions**, each with an `id`. Adding, renaming, editing the key spec / width / double-tap, and deleting for good all happen here.
- **`bar` = the bar**: each row is a list of **ids** pointing into "My keys".

The bar stores ids rather than whole definitions so that keys on the bar are **chosen from** your library rather than moved into it:

- the same key **can sit on both rows** (an Esc on row one and another on row two), and dragging one up leaves the library copy alone;
- edit a definition once and every reference on the bar changes with it;
- ✕ only removes a reference — the definition stays in the library, ready to be dragged up again. Deleting a definition also clears its references from the bar (with a toast saying how many, otherwise it reads as "a key went missing after saving").

**Built-in presets do not get their own shelf**: sixty-odd keys laid out is longer than the entire page, and they look editable while not being so. There is a single "Load presets" button that pours them all into "My keys" (deduplicated by name + what it does), after which every one of them is yours to edit. That is why the cap on "My keys" (120) is far higher than what fits on the bar — a definition is one line of JSON, it does not cost screen space.

Old configs (where "which row" lived on the key itself as `row` / `off`) are migrated to the two layers on first read, so a bar you already tuned is not lost to an upgrade.

Dragging requires **a 250ms hold** on touch (a mouse only needs 6px of travel). This page has to scroll vertically, and the keys themselves are the drag handles — a finger that lands on a key and moves down is either scrolling the page or dragging that key, and only "did you hold" separates the two. Hard-coding `touch-action: none` on the keys stops the page scrolling (the keys cover it); `pan-y` eats "drag down to the second row" as a scroll. Once picked up, `preventDefault` on `touchmove` blocks scrolling — the finger has not moved during the hold, the browser has not started scrolling yet, so it is still interceptable.

- The "Keys" field takes a **key spec**; space-separated entries fire in sequence — `ctrl+b c` is herdr's prefix plus c, one tap.
- Supports `ctrl+x` `alt+x` `shift+tab`, named keys (`esc tab enter space bs del ins up down left right home end pgup pgdn f1-f12`) and literal text.
- Two equivalent ways to write literal text: `"herdr" enter` and `text:/new enter` (`text:` exists for typing on a tablet — the editor already has `sticky:` / `act:` prefixes, and hunting for quote characters is worse; text with spaces still needs quotes: `text:"git status"`).
- Presets come in 8 groups (Prefix / Tabs / Pane / Workspace / Terminal keys / Text / Claude commands / Web actions); "Load presets" drops all of them into "My keys". The herdr groups are copied from the `[keys]` defaults of `herdr --default-config` — if you changed your keybindings, change these too. "Claude commands" is `/new` `/clear` `/compact` `/usage` `/context` `/model` `/resume` `/cost`, all with Enter, one tap each.
- Every key has a **"double-tap"** checkbox: those keys only fire on the second tap — the first only arms it (the key turns red, the label does not change, so the key does not get wider and shove its neighbours out from under your finger), and it disarms after 3 seconds or when you tap something else. Keys sit close together on this bar, and misfiring "close pane" or "close tab" cannot be undone. `Close pane`, `Close tab`, `Close workspace`, `Detach` and `/clear` ship with it on.
- `Ctrl` / `Alt` are **sticky**: tap once to light it up, then type a letter and the combination is sent, after which it turns itself off. Mobile virtual keyboards produce unreliable `keydown`, so this layer works on the data stream rather than on key events. The spec is `sticky:ctrl` / `sticky:alt`.
- `act:` actions are **handled in the browser** and send no bytes: `act:kbd` shows/hides the system keyboard, `act:img` uploads an image (camera / library; the path goes to your draft or straight into the terminal depending on whether the outbox is open), `act:panes` opens the pane list (previous section), `act:clip` fetches the machine's clipboard into your phone's clipboard, and `act:paste` pastes your phone's clipboard into the terminal (the last two are explained in [Copy and paste on a phone](#copy-and-paste-on-a-phone) — **on a phone these two can only be tapped**, browsers do not let a timer touch the clipboard). The server only accepts this whitelist; a typo is rejected at save time rather than shipped as a key that does nothing when tapped.
  `act:panes` on the bar is deliberate: on a phone the whole top bar collapses the moment the keyboard comes up, so the top-bar entry is unreachable exactly when the soft key bar is right under your thumb.
- Key specs are parsed into bytes **on the server** and handed down; the frontend just sends them. A bad spec is reported at save time, telling you which key and where it stopped making sense. In the response `send` is **the parsed bytes** and `spec` is what you wrote — the editor sends both back and the server **trusts `spec`**. Re-parsing `send` as a spec would turn Tab's `"\t"` into an empty string after trimming and report "the key spec is empty" while the user changed nothing (been there).

## Phones

xterm.js's touch support is essentially "tap to focus the hidden textarea"; the rest is ours. When a program has mouse reporting on (herdr does), touch gestures are taken over entirely by this project:

| Gesture | Behaviour |
|---|---|
| One-finger vertical swipe | Converted to SGR wheel reports by line height — `CSI < 64/65 ; col ; row M` — and sent to the program; with mouse reporting off it scrolls the local scrollback |
| Tap | With mouse reporting, sends `CSI < 0 ; col ; row M/m` (clicking panes and tabs both work) and **does not pop the system keyboard**; without it, focuses the hidden textarea (a tap there does mean "I want to type"). **It lands 320ms later** — see "Double tap" |
| Long press (≈380ms) | **Grab**: press the left button and hold, plus `CSI < 32` motion reports, so moving afterwards is a drag — this is how you resize herdr's pane borders on a phone. Releasing sends the matching `m` |
| Double tap | Show / hide the system keyboard. **The first tap does not leak as a single tap** — everything a single tap does (opening a tapped path, reporting the click to herdr) waits out the double-tap window (320ms) and is cancelled outright when a second tap arrives. The cost is that a single tap lands 320ms later, which is what having a double-tap gesture costs |

Grabbing **only happens on a long press**. "Grab immediately when the finger lands near a border" was tried and failed: agents draw their own boxes (Claude Code puts a rounded frame around each pane) whose vertical edges also run the full height, indistinguishable from herdr's pane borders at the character level — so a swipe along a frame turned into dragging the mouse inside the agent: the finger wanted to scroll, the screen was selecting text. **A swipe is always a swipe**; changing gear requires holding first.

After the hold it **snaps by pixels** to a nearby full-length line (`SNAP_PX = 24`, about the error of one fingertip), not by cells. Also learned the hard way: it used to allow one cell of error, but a tablet 211 columns wide has ~6px cells, so being a dozen pixels off dropped the press inside the pane — the agent got a drag, the screen did nothing, and it felt like "you simply cannot drag panes on a phone".

Only **full-length lines** count (box-drawing characters covering 70% or more of that column / row, at least 6 cells), which keeps out the short rules agents draw (message separators, "2 new messages"). What it cannot keep out is the agent's own outer frame, but since the effect is "the press moves at most 24px", guessing wrong just means this one drag lands on a frame. The cost: in a 2×2 layout the horizontal divider is only half the screen wide, so it does not snap and you have to be accurate.

The finger is allowed to drift 16px during the hold (`HOLD_SLOP`). It was 8px, which was too strict — a finger holding still drifts a dozen pixels anyway, and any drift cancelled the long press, which reads as "long press does nothing". With mouse reporting off (a plain shell) there is no grabbing, and a long press keeps doing nothing.

Verified end to end: in a separate herdr session with a vertical split, long-pressing **3 cells to the right** of the divider and dragging moved it from column 45/46 to 40/41; swiping vertically right on the divider still emitted nothing but wheel reports.

### The first tap only dismisses the keyboard

With the keyboard up, tapping any button in an overlay (the close ✕ on the panels is the one you hit most) used to dismiss the keyboard and nothing else; a second tap was needed. Same cause as [One tap has to jump](#one-tap-has-to-jump) above: the tap takes focus away first → the keyboard hides → visualViewport changes, `--vvh` and the top bar reflow → the element under your finger is no longer there, so the browser dispatches the click somewhere else or not at all. **The touch events arrive as usual; only the click is lost.**

So there is one global fallback (`web/src/lib/tap.ts`, installed once from `main.tsx`): if `pointerdown` and `pointerup` land on the same clickable control, the finger barely moved, and no click arrives within 250ms, it synthesizes one `el.click()`. The edges are all deliberate:

- **Mouse is left alone** (on the desktop a click is never lost, so synthesizing one would double-fire); a finger that travels more than 10px, or a `pointercancel` (a scroll started), is not a tap.
- **Nothing is synthesized if the element has left the document** — that means the tap did work.
- **A late click from the same gesture is swallowed**, or "close" would close twice and the second one would land on the terminal underneath, i.e. a click sent into herdr. The test is a gesture counter, not a time window: the user's own second tap has its own `pointerdown` and is never eaten.
- The synthesized click is recognised by **a flag, not `isTrusted`** — synthetic events are untrusted too, so keying on that would mistake any other synthetic click for ours (a test caught exactly that).

Why at the document level rather than in each button: the bug has nothing to do with which button it is — anything inside an overlay while the keyboard is up hits it, and wiring controls up one at a time always misses the next new panel.

The other half of it is fixed too: **a keyboard you had dismissed no longer pops back up because you switched panes or tapped the font buttons.** That one had two layers:

1. The test was wrong. It used to be "not a phone (< 440px) → hand focus back to the terminal", and tablets and landscape phones are all wider than 440 — on those, focusing the terminal *is* raising the IME. Now focus is only handed back in two cases: **the keyboard was already up** (put it back where it was) or **the machine has a fine pointer** (focusing raises nothing). See `refocusTerm` in `web/src/App.tsx`.
2. And there was **click-through**: the panel closes on `pointerup`, and the browser then replays that tap as **compatibility mouse events on the terminal that just got uncovered**. Two consequences, the second one sneakier:

   - xterm's mousedown handler focuses the hidden textarea — keyboard up again, from a tap that was never aimed at the terminal.
   - **herdr keeps mouse reporting on** (1002/1003/1006), so xterm forwards those mouse events as a real click — and whichever pane the phantom click lands on is where herdr moves focus. That is the "the toast says it switched but the pane is still the old one" report: the jump call and the phantom click race, and when the click lands second it drags focus back to the pane under your finger (hence "sometimes").

   The guard is a one-shot gate (`web/src/term/touch.ts`): a finger lifting outside the terminal opens it, and the next burst of mouse events (mousemove / mousedown / mouseup / click) consumes it — the whole burst is blocked if it landed on the terminal, wasted if it landed elsewhere. **A time window does not work here**: when the main thread is busy those events arrive a second late (measured: a 60ms wait around "close the panel + herdr repaints" came back as 1008ms), so a 700ms window leaks.

   **The gate has to arm on `pointerup`, not on `touchend`** — this is why the bug survived three fixes: the panel is unmounted inside the `pointerup` handler (React flushes discrete events synchronously), and a touch event's target is pinned at `touchstart`; per spec, "if the target element is removed from the document, events will still be targeted at it, and hence won't necessarily bubble up to the window or document". So that `touchend` **structurally never reaches document** and the gate never opened — the earlier versions were effectively not installed. A `pointerup`'s propagation path is computed when dispatch begins (panel still mounted), so unmounting inside the handler does not stop it; touch pointers also have implicit capture, so its target equals the `pointerdown` target and the "inside the terminal?" test is unchanged.

   One more thing worth remembering: **xterm's mousedown listener on `.terminal` does an unconditional `preventDefault()` + `focus()`** (`CoreBrowserTerminal` in `@xterm/xterm`), so "call preventDefault from outside to stop the focus" cannot work — the only option is to keep the event from reaching it, i.e. `stopPropagation` in document's capture phase.

   On top of the gate there is one **stateless** test (Chrome and Safari expose the field): a mouse event inside the terminal whose `sourceCapabilities.firesTouchEvents` is true *is* click-through by definition — a real touch on the terminal gets `preventDefault`ed at `touchstart`, so the browser never synthesizes compatibility mouse events for it.

   The hole did more than raise the keyboard: the phantom click is hit-tested afresh, and it was measured landing exactly on **herdr's own `switch` button in its mobile top bar** — herdr's switcher then opens full screen over the pane you just jumped to (which reads as "the jump did not work"), and `detach` in that list sits directly under where the web panel's rows were, so one more tap disconnects you.

The toast got honest in two ways at the same time:

- `pane.zoom` returns the `focused_pane_id` **herdr itself believes in**, so when it differs from the pane you asked for the message is now "did not reach X: herdr says focus is on Y" instead of claiming success. "The screen still shows the old pane but the toast said it worked" is the hardest kind of bug to chase — you assume the picture is stale when focus really did not move.
- **A jump also succeeds while the terminal's own connection is down or reconnecting** (the jump goes over HTTP, unrelated to the PTY WebSocket) — but the canvas is a frozen old frame, so the toast now adds "the terminal is not connected right now, the picture is stale". Without that line the symptom reads as "tapping does nothing, and tapping again does nothing either".

### Tapping herdr's switch opens ours (on by default)

herdr has a mobile layout of its own: once the terminal gets narrow enough (its `ui.mobile_width_threshold`, 64 columns by default) it collapses to a single column with a two-row status bar, and **flush against the right edge** sits a `switch` button — which opens herdr's own switcher (spaces / tabs / menu, drilled into one level at a time). By default a touch on it is **no longer forwarded to herdr**; it opens the [pane list](#pane-list-how-you-switch-panes-on-a-phone) instead: sorted by state, filterable, one tap crosses workspaces and zooms. Turn it off under ⚙ → Terminal and the button goes back to being herdr's.

The button is located by **flooding outwards from the word along the background colour** (`web/src/term/mobilebar.ts`), not by hard-coded coordinates — at 50 columns the block is columns 41–50 × rows 1–2, at 64 columns it is 55–64; the width follows the layout. Three things measured on a live herdr:

- **The hit area has to be the whole block, not just the six letters.** herdr's own hit area is the whole block: a tap on the row *above* the word (where there is no text at all) opens its panel just the same. Match only the letters and half the area still opens herdr's panel — one button, two behaviours.
- **Once claimed, the mouse report must not go out**, or both panels end up open and you have to dismiss herdr's after jumping.
- **herdr's own switcher has `switch` as its title**, so a block that starts at column 1 is never treated as the button — that title row is one continuous background (with a separate `close` block at the right). Without that guard, "close herdr's panel" would be claimed by us.

The cost, stated plainly: "+ new workspace / + new tab / settings / keybinds / detach" in herdr's panel become unreachable (ours only answers "where to" — the trade-off is in [Pane list](#pane-list-how-you-switch-panes-on-a-phone)). Turn the setting off to get them back, or reach them with herdr's prefix keys from the soft key bar.

Verified end to end on a real herdr narrowed to 64 columns: with the setting off, tapping `switch` brings up herdr's panel (spaces / tabs / menu); with it on, the pane list comes up and **not one byte goes to herdr**; while herdr's own panel is open, tapping its title is not claimed (the block found there starts at column 1, so it is rejected).

### Copy and paste on a phone

The conclusion first: on a phone you want **two soft keys** (Settings → Soft keys → Load presets; **📋 Fetch** and **📥 Paste** in the "Web actions" group, dragged onto the bar). The two reasons below get progressively less intuitive.

**First: herdr copies to the clipboard of the machine running herdr, not your phone's.**

Long-press and drag to select on a phone (that gets translated into a mouse drag for herdr, and with herdr's own `copy_on_select` it is a copy), herdr says "copied 84 chars to clipboard" — and **those 84 characters went into the Mac's clipboard** (`pbpaste` reads them back verbatim). The browser knows nothing about it and there is nowhere on the phone to paste it. It looks like copying failed; it succeeded, just onto another device.

Hence **📋 Fetch** (`act:clip`): tap it, the server reads the machine's clipboard (`pbpaste` / `wl-paste` / `xclip`, see `internal/clip`) and hands it to the page, which writes it into the phone's clipboard. From there, long-press-paste anywhere on the phone gets you that text.

The other direction is **📥 Paste** (`act:paste`): tap it to read the phone's clipboard and send it into the terminal as a **bracketed paste** (so several lines are not treated as one line plus an Enter). Touch has no `⌘V`, and it cannot long-press to raise the terminal's own paste menu either (single-finger gestures are taken over), so this key is the only way in.

**Why this cannot be an automatic sync**: browsers only grant clipboard access inside **a user gesture**, and a timer trying to do it quietly is denied — silently. So each direction costs one tap. That tap is a browser requirement, not a missing feature.

Touch **cannot select text at all** (single-finger gestures are entirely taken over), so the other copy route is **herdr's own COPY mode**: `ctrl+b` prefix to enter, `hjkl` to select, `y` to copy. That goes through **OSC 52** — the program inside the terminal pushes text to the page, and the page writes the system clipboard.

**But the browser may refuse to write, and it used to fail silently.** Two constraints stack: `navigator.clipboard` only exists in a secure context (over plain http on a LAN the object is simply absent), and mobile browsers require the write to happen inside **a user gesture**. Neither COPY mode nor "copy on select" is triggered by a click, so on a phone it was denied — a perfectly good selection on screen, not a word of feedback, and the clipboard still holding whatever it held before.

Now, when the write fails, a **"tap to copy"** strip appears at the bottom: that tap is itself the gesture, and one press puts it in the clipboard. If even `execCommand` is denied, it lays the text out in a box that is **already fully selected**, ready for long-press → "Copy".

There is one more that **bites on the desktop too**: **while the tab is not visible, Chrome leaves that `writeText` promise pending forever** (measured: 26 seconds, neither resolved nor rejected, and the clipboard really had not changed). A plain `await` there means "it failed to write" is never discovered, so that step has a 1.2 second cap and treats a timeout as failure, falling through to the two routes above.

Two related notes:

- **"Copy on select" is a mouse-era setting**; touch has no selection, so turning it on does nothing on a phone.
- To see what that strip looks like on a desktop, add **`?nocopy=1`** to the URL: both clipboard-write routes are forced to fail (a debug parameter, like `?poll=` / `?push=`).

Why all this ceremony: xterm.js only translates `wheel` into mouse reports and ignores touch entirely, so a program like herdr — sitting on the alternate screen (no local scrollback to scroll) with mouse reporting on — responds to neither on a phone and simply cannot be scrolled. Meanwhile taps and long presses land on the hidden textarea, and the browser helpfully pops the keyboard — while in a TUI, nine times out of ten you were only trying to tap a pane.

The approach is to `preventDefault` **unconditionally** on `touchstart` (single-finger gestures), which kills focusing, the long-press bubble, double-tap zoom and the browser's synthesized mouse events in one go, and then classify the gesture ourselves in `touchend` by distance and duration.

**Why swallow it even when there is no mouse reporting**: otherwise the browser synthesizes mouse events, xterm reads them as "press + drag-select", and a swipe turns into selected text with a motionless terminal (guaranteed to happen before herdr is attached, or when the pane runs something that ignores the mouse). Wanting to scroll is far more common than wanting to select on touch, so scrolling wins. The cost: touch can no longer drag-select (use a desktop mouse, or herdr's COPY mode), and a tap no longer focuses the textarea for free — we focus it ourselves in `touchend`.

**Gesture duration comes from the event's own timestamp** (`e.timeStamp`), not `Date.now()` in the handler. When the terminal is busy repainting, both timers and event dispatch get pushed back: a 60ms tap measured 994ms inside the handler and was rejected by "over 500ms is a long press, do nothing" — which reads as "when there is a lot of output, nothing responds anywhere". The event timestamp is taken when the event is created and is immune to handler delay.

**Switching panes does not have to be blind**: ▦ in the top bar or `act:panes` on the soft key bar opens the pane list, and a tap takes you there zoomed (see that section). The key channel can only express relative navigation like "next tab", and every screen along the way is exactly the tiled state you cannot read on a phone.

Focus is not stolen on connect on touch devices, otherwise the keyboard pops up the moment you arrive. To type: double-tap the terminal, or tap the ⌨ at the left end of the soft key bar. Keyboard state follows the textarea's focus/blur, so dismissing the keyboard yourself also unlights the button.

**"Go fullscreen when the keyboard comes up"** (Settings → Terminal, **on by default**): typing on a phone is where height is scarcest — the keyboard eats half the screen and the address bar plus toolbars take another slice, leaving three or four terminal rows. **Putting the keyboard away does not leave fullscreen**, deliberately: flashing in and out of fullscreen on every message is worse than not being fullscreen at all; leaving is the top bar button, once. It rides on the tap that raised the keyboard (`requestFullscreen` needs a user gesture and Chrome's window is about 5s, while the keyboard is up in ~300ms); if the browser refuses anyway it says so **once per device** (stored, not per page load — the switch is on by default and reopening the page is routine on a phone) rather than failing silently. Desktop has no soft keyboard, so the switch may as well not exist there.

**The top bar collapses to one line on phones**: status keeps only the coloured dot (full text moves into `title`), "Connect" disappears once connected, and font size `A−/A+` plus light-dark `◐` move into Settings → Terminal. Seven icons do not fit in 393px, and wrapping to two lines burns ~36px (about three terminal rows) for three things you adjust once.

**When the keyboard comes up, the whole top bar collapses** to an 8px sliver (tap to bring it back). Visible height is down to ~430px at that moment, and nothing in the top bar is of any use then — someone who is typing wants the soft key bar and the outbox; "Connect" was a before-you-connected concern. The collapse is temporary: opening it by hand is for this once, and it returns to normal when the keyboard goes away, with no state left behind (otherwise whether the top bar is there next time you type is a coin flip).

Whether the keyboard is up is decided by **how much visualViewport shrank** (`hooks/useKeyboardUp.ts`, threshold 0.8), not by "is xterm's hidden textarea focused": the most common posture in this project is **dictating into the outbox**, where focus is on the outbox and the terminal knows nothing, while the keyboard still eats half the screen. Both signals are used together (typing into the terminal moves the second, not the first). Failing to detect it costs no correctness — it degrades to "the top bar does not collapse".

**The virtual keyboard no longer covers content**: page height follows `visualViewport`, and the viewport meta carries `interactive-widget=resizes-content`. Reflowing the terminal alone is not enough — iOS **never** shrinks the layout viewport for the keyboard, `height:100%` refers to the unshrunk one, and the keyboard would simply cover the soft key bar and the outbox.

**But not every browser honours this** (measured: in some browsers the page height does not budge and half the outbox stays buried), so the dock down there can be dragged by hand and does not depend on browser behaviour.

### The bottom dock

The outbox and the soft key bar are **one dock** (`web/src/components/Dock.tsx`): one border, one width, adjusted once.

They used to be independent — the outbox could be torn off the bottom by its ⠿ handle into a floating panel (its own position / size / border) and the soft key bar had its own insets and height. Stacked, that is two borders, two widths and two sets of handles, looking like two misaligned layers, and "move this stuff off the IME" had to be done twice. Now the whole block shrinks together:

- **The left and right edges** drag horizontally to move that side's boundary (width). An IME plus its toolbar routinely covers half the screen; shrinking the whole dock into the space that is left beats keeping it full width under the keyboard.
- **The three handles on the top edge of the key area are two-axis**: dragging vertically sets the soft key bar's height (**half the screen max**, overflow scrolls), dragging horizontally moves the left boundary (left handle), the right boundary (right handle) or the whole dock (middle, width unchanged). Each direction has a 3px dead zone, so a purely horizontal drag does not accidentally pin the height. **Double-tap any handle to reset** (width and height together).
- Soft keys **wrap** rather than forming one long horizontally-scrolling queue. Untouched, the height is automatic and capped at two rows (no empty space); once dragged, it is fixed — when the user explicitly asked for "taller", do not helpfully shrink back to content height.
- Content inside the dock wraps by **the dock's own width** (`@container` + `@max-3xl:`), not the viewport's: after shrinking to half the screen the viewport is still as wide as ever, and viewport-based breakpoints would cram the outbox controls together.
- The insets moved to a new storage key (`dockInset`; the old `softkeysInset` is no longer read): the old one only shrank the key row, the new one shrinks the whole dock, and the semantics differ — reusing it would mean opening the page after an upgrade to find the outbox mysteriously half a screen wide.

**Phones in portrait (< 440px) are a different tier**: no handles at all, the dock spans the full width, and the soft key bar becomes **one horizontally-scrolling row** with smaller keys (13→11.5px, 35→28px high).

- On a screen that narrow the handles are a net loss: three of them add 24px (about two terminal rows), and there is no free space at the sides to give away anyway — at that size the IME covers the full width, not half.
- Wrapping keys costs more: every extra row is one less terminal row. Scrolling costs one swipe, and the keys you use are at the front anyway (you ordered them). If you want two rows, turn them on explicitly (see [Soft key bar](#soft-key-bar)); each row scrolls on its own.
- The breakpoint is written in two places and both must change together: `--breakpoint-phone` in `index.css` (for Tailwind's `max-phone:` variant) and `PHONE_MAX` in `hooks/usePhone.ts` (for inline styles and for "render the handles at all" — CSS cannot override those).
- Cross the width threshold (rotate, tablet, desktop) and the handles plus your stored sizes come back on their own. The two tiers do not affect each other.

**The floating outbox is gone.** Moving it away from the IME now has exactly one route: shrink the whole dock horizontally (which is the route actually in use — that tablet's IME covers half the screen). If moving it vertically ever becomes necessary, bring `useFloatBox` back — but do not let it grow its own border again.

**Handles must not touch the screen edge** (`EDGE_SAFE = 14`). Android gesture navigation claims a strip along each side for back/forward; the system takes it first and the page does not even receive `touchstart` — a handle on the edge simply cannot be dragged (measured). So handles inset themselves when they would come closer than that, and the dock's contents take the same padding so controls do not slide under a handle; a dock already shrunk inward needs no inset. The number came from a real device: insetting by the nominal 24dp left a strip that looked misaligned, and the effective swipe region is narrower than the nominal one. The day a side handle stops dragging again, suspect this number first.

**One set for landscape, one for portrait** (`web/src/lib/oriented.ts`). The same dock wants entirely different sizes and positions in the two orientations; sharing one set means re-arranging it on every rotation and clobbering the other one. So the dock's height and insets are stored per orientation, and rotating swaps in that set and then clamps it (**reading** that set, not nudging the current one). Orientation is decided by aspect ratio rather than `screen.orientation` — with desktop windows and tablet split-screen, the aspect ratio is what actually decides the layout. Anything stored by an older version (without the orientation suffix) is migrated to the current orientation on first read, so a dock you already tuned survives the upgrade.

Landscape is far more usable than portrait (enough columns); font size is on the top bar's `A− / A+`; the top bar also has a **fullscreen** button, and losing the address bar and toolbars is worth several terminal rows. iOS Safari does not grant fullscreen to web pages (only to video), so there it suggests "Add to Home Screen" instead — opening from the home screen has no address bar either.

## Verdict: herdr is genuinely usable in a browser

herdr requests these terminal capabilities at startup (captured off a PTY), against what is implemented here:

| Sequence | Purpose | Status |
|---|---|---|
| `CSI ? 1049 h` | Alternate screen | native to xterm.js |
| `CSI ? 1000/1002/1003 h` + `1006` | Mouse click/drag/motion + SGR coordinates | native |
| `CSI ? 2004 h` | Bracketed paste | native |
| `CSI ? 1004 h` | Focus in/out reporting | native |
| `CSI ? 2026 h` | Synchronized output (no tearing) | native, plus a repaint watchdog (below) |
| `OSC 8` | Terminal hyperlinks | native; clicks open in a new tab |
| `OSC 52` | Program writes the system clipboard | ClipboardAddon |
| `OSC 10;? / 11;?` | Query foreground/background colour (to detect light/dark) | xterm.js does not answer — **this project does** |
| `CSI ? 2031 h` | Theme change notification | unsupported by xterm.js — **this project emits** `CSI ? 997 ; 1/2 n` |
| `CSI > 7 u` | kitty keyboard protocol | unsupported by xterm.js — **this project implements the disambiguate subset** |

Those switches live under ⚙ → Terminal. **The "capabilities the program requested" list was removed**: it was a debugging view from the days of implementing the protocols and nobody reads it day to day (the capabilities are still tracked — `DEC 2031` theme notifications need them).

## Keyboard

herdr's shortcuts are almost all `ctrl+b` plus an ordinary key, which legacy encoding can express, so they do not depend on the kitty protocol. What kitty adds is the combinations legacy cannot express; it is on by default (turn it off in Settings → Terminal): `Ctrl+Shift+letter` → `CSI code;6u`, `Ctrl+digit` → `CSI code;5u`, `Ctrl+Enter` / `Shift+Enter` / `Ctrl+Tab`.

**Each herdr session has its own socket**: the default one is `~/.config/herdr/herdr.sock`, and `herdr --session x` is `~/.config/herdr/sessions/x/herdr.sock`. The outbox connects to whichever `HERDR_WEB_SOCKET` names, so using the outbox against a non-default session means pointing that variable at it.

**`Esc` is in there too, and it is the important one**: once a program declares kitty's disambiguate flag (`CSI > 1 u` — herdr and Claude Code both do), Esc must be encoded as `CSI 27 u`. A bare `0x1b` is the prefix of **every** escape sequence, so a program receiving it cannot tell immediately whether this is a real Esc or the start of a sequence; it has to wait for a timeout or drop it — which shows up as "Esc does nothing on the web page" and overlays like `/usage` that will not close. The soft key bar's `Esc` and the one forwarded from the outbox use the same encoding (bytes parsed on the server do not know whether kitty is on, so a lone ESC is re-encoded on the frontend according to the current mode).

Keys the browser keeps for itself: on macOS `⌘W` `⌘T` `⌘N` `Ctrl+Tab`; on Windows/Linux also `Ctrl+W` `Ctrl+T` `Ctrl+N` `Ctrl+Shift+I/J/C`. Installing the page as a PWA gets some of them back.

Copy `⌘C` (or `Ctrl+Shift+C`) · paste `⌘V` · clear `⌘K` · `Option` is Meta by default.

## Code layout

```
cmd/herdr-web/        main: flags, subcommands, listeners, startup banner, interface scoring
internal/
  config/             env vars (viper, env only), paths, deployment shape (TLS tier / exposure / allowlist)
  auth/               pairing codes + device credentials (hashes only) + rate limiting (gate.go)
  acme/               DNS-01 issuance and renewal (only imports the providers in use, see package doc)
  tlsgen/             local CA + short-lived leaf, or a real certificate you supply; both hot-reload
  ctl/                ~/.herdr-web/ctl.sock: the channel between subcommands and the running service
  herdr/              herdr socket client (one connection per call)
  composer/           per-agent input-line scraping + real captured screens in testdata
  agentwatch/         watches agent state changes: stamps times (the pane list's "3 minutes ago")
                      and queues notices (notice.go debounces, extract.go scrapes the screen;
                      testdata holds real captures)
  outbox/             list targets / pull back / clear / post / push draft
  softkeys/           soft key config + key spec parsing (data.go is generated from the old JS version,
                      not retyped; testdata/js-snapshot.json holds that snapshot and the test diffs
                      the first 6 groups against it)
  topbar/             which buttons sit in the top bar (a list of ids + a whitelist). **Separate
                      file, separate endpoint** from the soft keys: sharing one would mean partial
                      update semantics, which is how configuration gets silently dropped
  uploads/            image storage (type by magic number)
  files/              file browsing: starting points / directory listing / type by magic number /
                      short-lived signed links (sign.go). No boundary by default — FILE_ROOTS is
                      the jail; why, and the four "never text/html" rules, are in the package doc
  clip/               read this machine's clipboard (pbpaste / wl-paste / xclip) — herdr's copy lands
                      on **the machine running herdr**, so the phone can only get it from this side
  server/             HTTP routes + PTY/WebSocket + static assets
                      guard.go is the doorman (Host allowlist / Origin / security headers)
                      authapi.go is the pairing and device management endpoint
                      session.go dispatches "one URL, one herdr session" (per session: a socket,
                      an outbox, a status subscription)
                      filesapi.go is the file browsing endpoint plus /_f/, the **cookie-less**
                      byte-serving route
  webui/              embedded frontend build (dist is copied in by make build)
  qr/                 draws the QR code in the terminal at startup
  version/            the single source of the version number (injected by goreleaser ldflags)
  selfupdate/         query GitHub Releases + cache + download verification + in-place binary swap
  service/            install as a launchd / systemd service (plist / unit generation + env snapshot)
assets/               icons (herdr's sheep, caged in a browser window). **Do not hand-edit the svg** —
                      edit assets/make-logo.py and rerun it: the sheep silhouette is an 1800+ character
                      traced path reused from herdr, and one shape has to produce rounded / square
                      variants plus three pngs
web/                  Vite + React + TS + Tailwind v4 + shadcn-style components
  public/             icons and manifest (copied verbatim into dist by Vite, served from /)
  src/term/           xterm.js glue: protocol gap-filling, touch gestures, repaint watchdog
                      (imperative, deliberately not wrapped in React)
                      paths.ts turns file paths in the terminal into tappable links (reassembling
                      wrapped lines, terminating on CJK punctuation, refusing truncated ones —
                      every rule learned the hard way)
                      mobilebar.ts spots the switch button in herdr's mobile top bar
                      (flood-fill by background colour) — the test behind "tap it, get our pane list"
  src/hooks/          useCompose (outbox state machine), useNotices (notice polling + unread badge),
                      useViewportHeight
  src/lib/            api.ts (fetch + CSRF), chipdrag.ts (the "library below, bar above, drag into
                      it" gesture, shared by the top bar and soft key editors), tap.ts (synthesizes
                      the click when the browser loses it)
  src/components/     Dock.tsx is the bottom dock shell (border / width / height shared by the
                      outbox and the soft key bar)
                      Notices.tsx is the stack of cards in the top right
                      FilesPanel.tsx is file browsing (starting points + directories + the paste box)
                      FileViewer.tsx shows one file (image / text), full screen
                      Pairing.tsx is the pairing page (the only thing rendered when unpaired)
                      SettingsPanel.tsx is the settings panel; the top bar editor, the soft key
                      editor and device management are three of its pages
                      TopbarPanel.tsx is the top bar editor; topbarItems.tsx is the single
                      catalogue of which buttons exist (the server whitelist must match it,
                      and a test watches that)
                      QrScan.tsx is the in-page scanner (BarcodeDetector + rear camera)
reference/            the original Python prototype; "verified" in the three companion docs means
                      verified against it
npm/herdr-web/        the npm root package @bysir/herdr-web: a JS shim that finds the right binary
scripts/npm-*.mjs     turn goreleaser output into npm packages / publish them in order
install.sh            the no-node install path (download + mandatory sha256 verification)
.goreleaser.yaml      cross-compile + archive + checksums (darwin / linux only)
.github/workflows/    ci.yml runs on every push; release.yml publishes to GitHub + npm on a tag
```

The CLI is [cobra](https://github.com/spf13/cobra) (`cmd/herdr-web/main.go`): the root command starts the server, and `pair` / `devices` / `revoke` / `unlock` / `version` / `update` / `service` are subcommands, with `--help` and completion scripts for free. **There is exactly one flag**, `-w, --web` (point at a frontend directory during development); everything else is an environment variable — two entry points for one setting means having to specify which one wins, and it is not worth it.

`make test` runs the Go tests plus a frontend typecheck. `make dev` gives frontend hot reload (run the backend separately with `go run ./cmd/herdr-web`; vite proxies `/api` and `/pty` to it).

### Releasing

```bash
make release-dry        # run the whole chain locally: cross-compile → archive → npm packages → npm publish --dry-run
make release V=v0.1.0   # tag and push; GitHub Actions does the rest
```

Once the tag lands, `release.yml` runs `make test` → goreleaser (cross-compile 4 platforms, produce archives and `checksums.txt`, create the GitHub Release) → turn the archives into npm packages → **publish the 4 platform packages first and the root package last**. In the other order there is a window where `npm install` produces a shim with no binary.

**Release created but the npm step failed** (happened once) — rerun the same workflow to publish without recompiling:

```bash
gh workflow run release.yml -f tag=v0.1.0
```

It downloads the archives that were already published, so the re-published binaries are **byte-identical** to the ones in the Release.

**There can only be one publishing workflow; do not split it up.** npm's Trusted Publisher (OIDC) binds one package to one workflow filename, and that filename is `release.yml`; a second workflow that publishes would not match the OIDC claim.

One repository secret is needed: `NPM_TOKEN` (**Automation** type — the other two kinds ask for an interactive 2FA code when publishing from an account with 2FA on, and CI has nobody to type it). Once Trusted Publisher is configured you can drop it, but **publish one release first to confirm OIDC actually works** before deleting it.

Trusted Publisher is configured **per package**, so all 5 (root + 4 platform packages) need it, each pointing at `release.yml` with the Environment name **left empty** (our workflow declares no environment; any value there makes the OIDC claim mismatch). Missing one shows up as the next release failing halfway through the npm step.

**Push the tag to the remote that holds `release.yml`** — that is GitHub. This repository has two remotes (`origin` is a self-hosted git; `github` is GitHub), so `make release` **does not hardcode origin**: it recognises the remote by `github.com` in the push URL and refuses to release if it cannot find one. Pushing to the wrong remote is the worst kind to debug: the tag lands, the command succeeds, and Actions simply never starts — and "never started" looks exactly like "still queued". To override: `make release V=vX.Y.Z RELEASE_REMOTE=xxx`.

Three names must agree, and changing one means changing the other two: `name_template` in `.goreleaser.yaml`, `internal/selfupdate.AssetName` (used by self-update downloads), and `scripts/npm-build.mjs`. A mismatch shows up as `herdr-web update` downloading a 404.

`make release-dry` **restores the working tree** when it finishes: `npm-build.mjs` writes the version into the committed `npm/herdr-web/package.json` (a snapshot number like `0.1.1-next` during a dry run). Without the restore, the `make release` right after it says "the working tree is dirty" when you changed nothing — or that `-next` version gets committed by accident.

Three release-path traps already hit and fixed (all **silent** failures):

- `web/tsconfig.tsbuildinfo` used to be committed. It is `tsc -b`'s incremental cache, rewritten by every `make test` run, after which goreleaser declares `git is in a dirty state` and refuses to release. Build caches never get committed.
- `rm -rf $(WEBDIST)` in `make web` deletes the committed `internal/webui/dist/.gitkeep`. That file is load-bearing: on an empty directory `go:embed all:dist` fails with `cannot embed directory dist: contains no embeddable files`, and a fresh clone cannot even `go build`. So both the `web` and `clean` targets write it back.
- For a few minutes after a first publish, npm's packument read path has not materialized yet (the `version` endpoint and search both find it while the packument 404s). An `npm i` that gets a 404 **silently skips** the optional dependency and installs a shim with no binary. Reinstall a few minutes later; the error message inside the shim tells you to.

**Why the terminal layer is not a React component**: it touches xterm's parser directly, consumes the WebSocket byte by byte, and repaints on rAF — React's render cycle would only be in the way. React holds a ref to mount it and subscribes to a few state callbacks.

### Colours (read this before touching the UI)

All tokens live in `@theme` in `web/src/index.css` (one set for dark, one for light). Components **never write a literal colour**, only these names:

- Four greys: `bg` (canvas / terminal) → `bar` (top bar, dock, overlays) → `ctl` (controls) → `ctl-hi` (control hover); dividers `line` / `line-hi`; text `fg` / `muted` / `faint`. All **pure grey** (S=0) — the old blue-ish slate looked dirty stacked against the terminal's coloured output.
- Green is only an accent: `brand` for text / icons / outlines, and `brand-bg` + `brand-line` + `brand-fg` for the filled primary button. **On / selected states are "pale green fill + green border + green text", not a solid block** — five or six icons in the top bar can be on at once, and solid fills turn the whole bar into colour blocks with nothing standing out. Saturated fills are reserved for the one primary action on screen (post / save / pair) and for sticky modifiers, where "you pressed it" must be unmissable.
- Two radii: controls `rounded-md` (6px), overlays `rounded-card` (12px). Type: 13px body, `text-xs` for everything secondary; stop writing one-off values like `text-[11.5px]`.
- In the terminal only **the greys and the cursor** follow the tokens (`src/term/themes.ts`): background = `bg`, cursor = brand green, selection = translucent green. The six hues (red, yellow, blue, magenta, cyan) are untouched — those are other programs' output colours, and diff red/green and agent highlighting depend on them.
- `accent` is the old name (the original bright blue), kept as an alias of `brand` so nothing silently breaks. Do not use it in new code.

## Traps (already handled; noted so nobody walks back into them)

- **Do not verify touch bugs of the "tap did nothing / the keyboard raised itself / focus got dragged away" family with synthetic events.** This one cost three rounds on the same bug: events from `dispatchEvent` **do not produce compatibility mouse events**, so I hand-fired a `touchend` to stand in — and on a real device that is precisely the one event that is always missing (the overlay is unmounted inside the `pointerup` handler, and a touch event's target is pinned at `touchstart`; once the element leaves the document the event is still targeted at it and **no longer bubbles to document**). In the test the gate was always open and the fix looked good; on the phone it was never installed. **"A finger just lifted somewhere else" can only be detected from `pointerup`** — its propagation path is computed when dispatch begins, so unmounting the overlay inside a handler does not stop it.
- **Before touching the touch/mouse layer, read the actual listeners in `web/node_modules/@xterm/xterm/src/` instead of reasoning about them.** Two that already bit: mousedown on `.terminal` does an **unconditional** `preventDefault() + focus()` (so "preventDefault from outside to stop the focus" cannot work — only `stopPropagation` in document's capture phase can), and the same mouse burst is also **reported to herdr** over SGR, so one phantom click drags herdr's focus to whatever pane sits under your finger, or opens herdr's own `switch` panel on top of the one you jumped to. One hole, two consequences — fix only one of them and it keeps feeling broken.
- **A WebSocket cannot be written concurrently, and a bad write takes the whole process down.** gorilla/websocket panics with `panic: concurrent write to websocket connection`, and that panic happens on a goroutine the handler started — net/http only recovers the handler's own frame, so **the process exits and everybody's terminal drops at once**. A PTY connection has three writers: PTY data, a ping every 25 seconds, and the exit + close on teardown. It blew up in production once, a ping landing on a batch of binary frames (unrelated to "how many browsers are open" — each connection has its own conn; but more connections and more reconnects make a collision likelier). Everything now funnels through `wsWriter`, and the concurrency test in `ws_test.go` reproduces the same panic if you remove the lock. Two things came along: writes got a 10 second timeout (when a phone loses signal, a full TCP buffer leaves `WriteMessage` blocked forever while holding the lock, which stalls the PTY read loop), and the ping goroutine now selects on a done channel (`Ticker.Stop()` does not close the channel, so a stopped goroutine parks on the receive forever and leaks along with its conn — one per reconnect, which a phone produces plenty of).

- **`HERDR_*` makes herdr refuse to start.** If this service was started from inside a herdr pane, the child inherits them and reports `nested herdr is disabled by default`. `dropEnv` in `internal/server/pty.go` strips `HERDR_* / TMUX / ZELLIJ / ITERM_* / CLAUDECODE`.
- **xterm.js 6.0 will "accept a repaint request and not paint"**: with DEC 2026 synchronized output on it accumulates ranges waiting for ESU, and painting happens in rAF, which does not run at all in a background tab. herdr keeps 2026 on permanently and a single frame of a few KB gets split across several writes, so one dropped accumulation leaves a blank patch on screen. The buffer is fine, so the fix is only a repaint: 180ms after the data stream stops, force one; if 2026 is stuck, emit an ESU ourselves. If it happens often, turn synchronized output off in Settings → Terminal.
- **Resizing flashes black, and a "freeze frame" has to cover it.** Most visible when the IME comes up (`visualViewport` changes and everything reflows). The causes stack: xterm's WebGL renderer clears the drawing buffer as soon as `canvas.width` changes, `FitAddon.fit()` actively calls `renderService.clear()` before resizing, and the repaint cannot happen before the next rAF at the earliest (later still with 2026 waiting for ESU); then herdr receives SIGWINCH and clears and redraws on its own. Tens of milliseconds all told. xterm offers no synchronous repaint, so none of that latency can be removed — instead, before resizing, the canvas layers inside `.xterm-screen` are composited into one image laid over the terminal, and it fades out 120ms after the new frame arrives (`onRender`). Two prerequisites: WebGL needs `preserveDrawingBuffer` (or `drawImage` gets an empty picture after compositing), and **if the snapshot comes back empty the freeze frame must be abandoned** (in a background tab rAF never ran and the canvas was never painted; pasting an empty image over the terminal is worse than the flash). Also, if rows and columns did not change, xterm is not touched at all: `visualViewport` fires several times during the keyboard animation, and a pointless resize is a pointless flash.
- **herdr's theme does not follow the browser** unless `[theme] auto_switch = true` in `~/.config/herdr/config.toml`. With it on, toggling light/dark on the page switches herdr's colours too.
- **Never set `HERDR_WEB_SETTLE_MS` to 0** — see [Configuration](#configuration).
- **A reconnect must reset the terminal first.** One WebSocket is one PTY, and the server kills the PTY on disconnect, so every "connect" is **a brand-new login shell** — but the xterm instance is reused and still carries the private modes the previous herdr turned on. The symptom is not just a broken screen after reconnecting but garbage typed into the command line: mouse motion reporting (1003+1006) is still on, so any pointer or stylus movement emits `ESC [ < 35;120;36 M`, zsh's ZLE swallows the unrecognised `ESC [ <` prefix and self-inserts the rest, and the screen fills with `35;120;36M35;115;37M…` (reproduced: `➜  ~ 35;16;5M35;26;8M`). kitty keyboard flags linger the same way, so Esc gets encoded as `CSI 27 u` and shows up as `[27u` in the new shell. `connect()` now calls `term.reset()` before connecting, and clears the kitty flags / capability list / sticky modifiers we track ourselves.
- **The "Connect" button is always clickable, so connecting must tear down the old connection first.** If it does not: the server starts a second login shell, two shells pour output into one xterm, the screen is instantly garbage, and the old PTY stays alive as long as its connection does. The old connection's callbacks have to be detached too — close is asynchronous, and the old connection's `onclose` would set the new connection's state to "disconnected".

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
| `HERDR_WEB_ONCONNECT` | `herdr` | Typed into the PTY on connect (Enter included). **Set it to an empty string to type nothing.** **Session URLs ignore this** (`/work` always types `herdr --session work`, see [One URL, one session](#one-url-one-session-name)) — to always land in a session, bookmark the URL rather than setting this |
| `HERDR_WEB_ONCONNECT_MS` | `250` | How long to wait before typing that line. The wait starts **after the shell's first output** — an rc file touching `stty`, or a completion plugin initialising, **silently swallows** characters typed too early. If the auto-typed line does not land, raise it |
| `HERDR_WEB_DIR` | `~/.herdr-web` | Data directory, in two layers: configuration and files (`softkeys.json` / `tls/` / `uploads/`) at the root, **internal data** (device credentials, passkey public keys) under `data/` — those two are not meant to be hand-edited, and tampering is reported in the terminal. **Keep the path short**: a unix socket (`ctl.sock`) is opened inside it, and beyond ~100 bytes it cannot bind, which breaks the subcommands |
| `HERDR_WEB_FILES` | on | `=0` turns file browsing off: `/api/files/*` and `/_f/` all 404, and the 📁 in the top bar is not drawn (an entry point that opens onto a wall of 404s is worse than no entry point) |
| `HERDR_WEB_FILE_ROOTS` | empty | Comma-separated directories. Set, this is **a real allowlist** (a jail) and only those trees are visible. **Empty means no boundary** — the reasoning is in [File browsing](#file-browsing-looking-at-what-the-agent-generated). `~` is expanded; non-absolute entries are discarded (relative to what? keeping them only makes the prefix check pass somewhere surprising) |

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

**Certificate issuance (tiers C / D) requires `--env-file`.** DNS provider credentials (`CLOUDFLARE_DNS_API_TOKEN`, `ALICLOUD_ACCESS_KEY` and friends) neither carry the `HERDR_WEB_` prefix nor appear in the allowlist above, so **they are not copied from the shell**: however correctly you exported them in `.zshrc`, the installed service still cannot get a certificate — and it only blows up at the first issuance. Keys in `--env-file` go in **wholesale** (and override the current environment), which makes it the only way to hand the token to the service. The file is read at `install` time only and never touched again.

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

**This thing amounts to a shell over HTTP** (the outbox alone can make an agent run commands, even without a PTY), so the door is designed on that premise. The design document and threat model are in [SECURITY.md](SECURITY.md) (Chinese); what follows is only what is **already implemented**:

- **Pair each device once.** A one-time code (40 bits, 5 minutes, single use, memory only) is exchanged for a per-device credential in an `HttpOnly; SameSite=Strict` cookie. The server's `~/.herdr-web/devices.json` **stores only sha256** — the agents on this machine read untrusted content all day, so "the credential file gets read by prompt injection" is a daily risk here, not a theoretical one.
- **Credentials are bound to a device, not an IP.** Remembering trust by IP loses both ways: DHCP hands an address you approved to somebody else (a guest joins the Wi-Fi and is in your shell), and changing your own Wi-Fi means pairing again.
- **No secrets in URLs.** `?pair=` and the legacy `?token=` are exchanged for a cookie and scrubbed with a 302, so browser history, bookmark sync and screenshots stop being leak channels.
- **Revocable.** `herdr-web devices` / `revoke` from the CLI, or Settings → Devices → "Sign out" / "Kick" on the web; the next request gets 401.
- **Only someone at the machine can produce a pairing code** (`herdr-web pair` or the startup banner). No path on the web issues one, not even to an already-paired device. Two reasons: ① a code creates an independent credential that **is not revoked along with its creator** — someone borrows your phone once, pairs their own device, and after you kick the phone theirs is still in: persistence that bypasses revocation; ② the code is printed into a terminal, and that terminal is often a herdr pane, where an agent in the same session can `pane.read` it — so if an outsider could remotely trigger "print a code", "trigger the print + let an injected agent read it" is a complete remote pairing chain with nobody ever touching the machine. Until the second factor at L2 exists, "can see that terminal" is **the only out-of-band factor in the system** and must not be weakened.
- **Refuses to start when exposed without TLS** (it used to be a warning line, and warnings go unread). Self-signed uses a local CA + a 397-day leaf; a changed IP re-issues automatically, but devices trust the CA, so nobody has to click "proceed" again.
- **A Host allowlist** blocks DNS rebinding (IPs always pass, domains must be in `HERDR_WEB_HOSTNAME` or get a 421); **Origin checks** plus `SameSite=Strict` plus a custom header make three layers against CSRF; a cookie-bearing request to `/pty` with no Origin is rejected outright.
- **Rate limiting and lockout**: the first two wrong pairing codes are free, then exponential backoff; 10 failures within 15 minutes bans that IP for 15 minutes (doubling for repeat offenders, capped at 24 hours), and distributed attempts from rotating source IPs trip a global breaker (which only refuses new pairings and leaves existing sessions alone) with a warning printed in the terminal. Only **short-credential guessing** counts as failure — an unrecognised cookie does not, otherwise revoking an old phone would immediately lock you out. **Loopback is never banned by default** (the unlock path is behind the same door), but **declaring `EXPOSED` turns that exemption off automatically** — see the frp section below: everything tunnelled in has source IP 127.0.0.1, and leaving the exemption on would make the entire rate-limiting layer a no-op.
- Security headers (CSP / nosniff / no-referrer / DENY), a cap of 8 concurrent PTYs, and OSC 8 links restricted to `http/https/mailto` (what a terminal displays is up to the program).
- **HSTS is deliberately not sent**: with a self-signed certificate, HSTS would weld the "proceed anyway" route shut, and it cannot be cleared.

- **Passkeys** (second factor). Settings → Devices → passkey → add. The server **stores only the public key**, so an agent on the same machine reading the credential file gains nothing (TOTP's shared secret cannot offer that, which is the main reason for choosing this). Once added: moving to a new device does not require going back to the machine (a synced passkey is on all of your devices), and session credential lifetime can drop from three months to one day. It requires a domain — a bare IP is not a valid WebAuthn identity.

Not built yet (in order of value): audit logging, token rotation with reuse detection, a `panic` disconnect-everything button.

### Reaching it from the internet (frp / tunnels)

> The complete public-access design, the simplification tiers, and the operational traps hit in a real deployment: [DEPLOY.md](DEPLOY.md) (Chinese).

The recommendation is **frp's `type = tcp` plus herdr-web holding a real certificate**: TLS end to end, and the VPS running frps only ever sees ciphertext. frp's https mode decrypts on the VPS, which means that machine can watch your entire terminal.

```bash
HERDR_WEB_EXPOSED=1 HERDR_WEB_TLS_CERT=~/certs/herdr.example.com/fullchain.pem HERDR_WEB_TLS_KEY=~/certs/herdr.example.com/privkey.pem HERDR_WEB_HOSTNAME=herdr.example.com HERDR_WEB_PUBLIC_URL=https://herdr.example.com:17788 ./herdr-web
```

Get the certificate via DNS-01 (`lego` or `certbot`): **nothing needs to reach you, you only need to edit one TXT record**, so even with the A record pointing at a LAN address you get a certificate browsers trust by default — zero warnings on the phone, a secure context (so the clipboard and `OSC 52` behave), and the passkey domain requirement solved in one move.

⚠️ **`HERDR_WEB_EXPOSED=1` has to be declared by you**: behind frp, frpc connects from localhost, so herdr-web sees a listen address of `127.0.0.1` and a source address of `127.0.0.1` on every request — "is this local" is useless. Without the declaration, "exposed but undetectable" and "loopback-without-pairing lets public requests in" are both open at once. The variable covers the first; the second is now **off by default**.

⚠️ **frp's tcp mode cannot see the client's real IP**: as far as herdr-web is concerned every request comes from `127.0.0.1` (same when frpc runs in a container). Two consequences:

1. Per-IP rate limiting counts everyone as the same person. Lockout only blocks new pairings and leaves existing sessions alone, so the worst case is "you cannot pair a new device for fifteen minutes"; `herdr-web unlock` clears it.
2. **The "never ban loopback" exemption must be turned off**, otherwise the whole rate-limiting layer is a no-op — configured, and never once effective. `HERDR_WEB_EXPOSED=1` turns it off automatically, which is the other reason that variable must be declared.

If you want real IPs, enable `transport.proxyProtocolVersion = "v2"` on frpc (herdr-web would also need to parse the PROXY header, which it does not yet), or use http mode so frps adds `X-Forwarded-For` (only then set `HERDR_WEB_TRUST_PROXY=1`). **Never set that variable without a trusted proxy in front** — an attacker just brings their own header.

### Odds and ends

http is not a secure context, so `navigator.clipboard` does not exist: on a phone `OSC 52` stops working and `⌘C` falls back to `execCommand('copy')`. Over HTTPS everything behaves.

Cookies **do not distinguish ports**: another web service on a different port of the same host also receives this cookie (`HttpOnly` only stops JS from reading it, not the browser from sending it). There is no fix — do not run untrusted web services on the same machine.

**The pairing code is printed into a terminal, and if that terminal is a herdr pane, other agents in the same session can `pane.read` it** (this project's own outbox reads panes exactly that way). The window is 5 minutes and single use, and it only exists when you actively ask for a code (which is why remotely triggering one was removed). If that still bothers you, run `herdr-web pair` in a terminal outside herdr.
