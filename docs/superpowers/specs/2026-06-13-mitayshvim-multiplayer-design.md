# Small Mitayshvim — Multiplayer Rooms & Public Deployment Design

Date: 2026-06-13
Status: Approved (design review in chat, 2026-06-13)

## Goal

Take the existing single-game LAN Catan-style server and turn it into a
publicly deployable, room-based multiplayer game ("Small Mitayshvim") that
friends join via short room codes — with production-grade hardening
(rate limits, input validation, graceful shutdown, health checks) and zero
hosting cost. Non-commercial hobby project.

## Decisions (locked)

| Topic | Decision |
|---|---|
| Scale model | Single node, room-based. One Go binary, in-memory rooms. |
| Transport | Keep SSE (state push) + POST (actions). No WebSocket. |
| Lobby model | Room codes only. No public listing, no matchmaking. |
| Persistence | Per-room gob snapshot in `data/<code>.gob`, restored on boot. |
| Name / IP | Rename everything to **Mitayshvim** (UI title "Small Mitayshvim"). No "Catan"/"Cattan" anywhere — trademark risk. Mechanics are not copyrightable; name and artwork are protected, so we use neither. |
| Deploy | Open item — Oracle Always Free VM or home machine + Cloudflare Tunnel (Fly.io trial already consumed; Fly no longer free). Architecture is host-agnostic: one binary + one data dir behind a TLS-terminating reverse proxy. |
| Dependencies | Go stdlib only. No new modules. |

## Step 0 — Rename (IP hygiene)

- `go.mod` module `cattan` → `mitayshvim`; all imports.
- Binary, UI strings, page title, README → "Small Mitayshvim",
  described as "a hex-based settlement and trading board game".
- `.cattan-state.gob` single file → `data/<code>.gob` per room.
- No references to Catan in code, comments, README, or UI.
- Project directory rename (`~/Learning/Cattan` → `~/Learning/mitayshvim`)
  done by the user at their convenience; code must not depend on the dir name.

## Architecture

One Go binary, stdlib only. The `game` package (rules engine) is untouched
except where noted.

### Components

- **`Hub`** — owns `mu sync.Mutex` and `rooms map[string]*Room`.
  Create, lookup, expire. Enforces global caps.
- **`Room`** — `{Code string, G *game.Game, clients map[chan struct{}]bool,
  mu sync.Mutex, lastActive time.Time}`. The existing `server` methods
  (`handleJoin`, `handleAction`, `handleEvents`, `fanout`) move here nearly
  verbatim, scoped to one room.
- **Bot ticker** — one goroutine ticks every 800ms and calls `BotStep()` on
  every active room (no per-room goroutines to leak).
- **Janitor** — one goroutine sweeps rooms: lobby idle > 1h or game idle
  > 24h → remove room, delete `data/<code>.gob`.
- **Share URL** — derived from the request `Host` header (+ scheme from
  proxy header). `lanIPs()` and startup `JoinURL` are deleted.

### Routes

| Route | Purpose |
|---|---|
| `GET /` | Landing page: create game / enter code |
| `POST /api/rooms` | Create room → `{code}` (rate-limited per IP) |
| `GET /r/{code}` | Game SPA (same `index.html`) |
| `POST /api/r/{code}/join` | Join / claim seat / resume |
| `POST /api/r/{code}/action` | Game action |
| `GET /api/r/{code}/events` | SSE stream (token in query) |
| `POST /api/clientlog` | Client error reporting (hardened, see security) |
| `GET /healthz` | Liveness for proxy / platform checks |

Room codes: 6 chars, crypto/rand, alphabet `A-Z2-9` minus `O/I` (32 chars,
~1.07B combinations). Validated `^[A-Z2-9]{6}$` on every room-scoped route.

### Lifecycle

- SIGTERM/SIGINT → `http.Server.Shutdown` with timeout, snapshot all rooms,
  close SSE streams.
- Boot → scan `data/`, restore every readable `<code>.gob` as a room.

## Security — Threat Model

Red-team attacks and the controls that answer them. All controls are
in-scope for implementation, not optional.

| # | Attack | Control |
|---|---|---|
| 1 | Room-code brute force | crypto/rand 6-char codes (~1B space); per-IP rate limit on join/lookup; uniform 404 for missing vs never-existed; strict code regex on every route |
| 2 | Resource exhaustion (room spam, SSE flood, huge bodies) | Global `MAX_ROOMS`; per-IP room-creation token bucket; per-room SSE cap (~10) and global SSE cap; `http.MaxBytesReader` (4KB) on every POST; `ReadHeaderTimeout` on the server |
| 3 | Token theft (SSE token in query string) | 128-bit crypto/rand tokens; access logs never include query strings; TLS via reverse proxy; token only grants that room's seat |
| 4 | XSS via player name | Audit `index.html`: names rendered with `textContent` only; CSP `default-src 'self'`; `X-Content-Type-Options: nosniff`; server-side name length cap + charset filter |
| 5 | Log injection via `/api/clientlog` | Per-IP rate limit; strip control characters and newlines; 4KB cap (exists) |
| 6 | Path traversal via room code | Filenames built only from regex-validated code; fixed data dir; no other user input reaches the filesystem |
| 7 | Spoofed client-IP headers defeat rate limits | Trust `X-Forwarded-For` only when the request comes from the configured reverse proxy; otherwise use `RemoteAddr` |
| 8 | Acting as another player | Engine already binds every action to the token's seat — kept; constant-time token comparison |
| 9 | Room squatting | Janitor TTLs (1h lobby / 24h game) delete room and snapshot |

**Process guarantee:** after implementation and before any public deploy,
run an adversarial security review of the full diff plus abuse e2e tests
(code brute force, oversized bodies, SSE flood, XSS payload names) in the
existing playwright harness. Fix findings, re-review.

## Persistence

- `data/<code>.gob` written on every state change (existing fanout hook,
  now per room) and at graceful shutdown.
- Restored on boot; unreadable files logged and skipped.
- Deleted when the janitor expires a room.

## Testing

- Engine tests (`game/*_test.go`) untouched — engine API unchanged.
- New hub unit tests: create/join/expire, code uniqueness, concurrent
  access with `-race`.
- Handler tests: rate limits, body-size rejection, code validation,
  uniform 404.
- E2E (playwright harness at /tmp/cattan-e2e conventions): two browsers in
  two different rooms — full isolation; reconnect/resume in a room.
  Test servers on :8090–:8094, `waitUntil:'load'` (SSE blocks networkidle).

## Out of Scope (YAGNI)

- Multi-node / horizontal scaling, Redis, message queues.
- WebSockets, matchmaking, public room lists, accounts, chat.
- Spectators, game history database.

## Deploy (decided at deploy step)

Two free candidates, both: reverse proxy terminates TLS → binary on
localhost port, `data/` on local disk.

1. **Oracle Cloud Always Free VM** — always-on ARM VM, Caddy reverse proxy
   (automatic TLS), systemd unit, real disk. Most ops learning.
2. **Home machine + Cloudflare Tunnel** — free tunnel, Cloudflare edge
   TLS/DDoS, up only while the machine is up.
