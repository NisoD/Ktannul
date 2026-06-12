# Small Mitayshvim — Multiplayer Rooms & Deployment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the single-game LAN server into a room-based multiplayer server ("Small Mitayshvim") with code-based lobbies, security hardening, and Oracle-VM deployment artifacts.

**Architecture:** One Go binary (stdlib only). A `Hub` owns `map[code]*Room`; each `Room` wraps the untouched `game.Game` engine with its own SSE clients and gob snapshot at `data/<code>.gob`. Routes are room-scoped (`/api/r/{code}/...`). Caddy terminates TLS on the VM and proxies to localhost.

**Tech Stack:** Go ≥1.22 (stdlib `net/http` route patterns), vanilla JS SPA (existing `web/index.html`), playwright-core e2e harness at `/tmp/cattan-e2e`, Caddy + systemd on Oracle Always Free VM.

**Spec:** `docs/superpowers/specs/2026-06-13-mitayshvim-multiplayer-design.md`

**File map (end state):**

| File | Responsibility |
|---|---|
| `go.mod` | module `mitayshvim` |
| `main.go` | flags, wiring, bot ticker, janitor ticker, graceful shutdown |
| `code.go` + `code_test.go` | room-code generation + validation |
| `ratelimit.go` + `ratelimit_test.go` | per-IP token bucket |
| `hub.go` + `hub_test.go` | Hub, Room, fanout, restore, expire, saveAll |
| `httpserver.go` + `httpserver_test.go` | routes, handlers, middleware, security headers |
| `web/index.html` | game SPA, room-scoped (modified) |
| `web/landing.html` | create/join page (new) |
| `deploy/Caddyfile`, `deploy/mitayshvim.service`, `deploy/README.md` | deployment |
| `game/*` | UNTOUCHED — single exception: `newToken` switches to crypto/rand (Task 5 Step 4) |

Run all Go commands from the repo root. After the module rename the import path is `mitayshvim/game`.

---

### Task 1: Rename — IP hygiene

**Files:**
- Modify: `go.mod`, `main.go:17`, `web/index.html:7,428`, `README.md`

- [ ] **Step 1: Module + import**

`go.mod` first line → `module mitayshvim`.
`main.go` import `"cattan/game"` → `"mitayshvim/game"`.

- [ ] **Step 2: UI strings**

`web/index.html` line 7: `<title>Catan</title>` → `<title>Small Mitayshvim</title>`
Line 428: `<h1>CATAN<small>SETTLERS · LAN</small></h1>` → `<h1>MITAYSHVIM<small>SMALL · ONLINE</small></h1>`

- [ ] **Step 3: README rewrite**

Replace `README.md` content:

```markdown
# Small Mitayshvim

A hex-based settlement and trading board game for 2–4 players (plus bots).
Go backend (stdlib only) with SSE state push, single-page web frontend with
a Three.js 3D board. Friends join via 6-character room codes.

## Run locally

    go run . -addr :8080 -data ./data

Open http://localhost:8080, create a game, share the room link.

## Test

    go test ./... -race
```

- [ ] **Step 4: Comments mentioning Catan**

`grep -rn -i catan *.go game/*.go` — reword any hits (e.g. "Catan server running." → "Mitayshvim server running."). Do NOT touch `localStorage` keys yet (Task 7 replaces them wholesale).

- [ ] **Step 5: Verify build + tests**

Run: `go build ./... && go test ./...`
Expected: builds, all existing `game` tests PASS.

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "Rename project to Small Mitayshvim (module mitayshvim)"
```

---

### Task 2: Room codes

**Files:**
- Create: `code.go`, `code_test.go`

- [ ] **Step 1: Write failing tests**

`code_test.go`:

```go
package main

import "testing"

func TestNewRoomCode(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		c := newRoomCode()
		if !validCode(c) {
			t.Fatalf("generated invalid code %q", c)
		}
		seen[c] = true
	}
	if len(seen) < 990 { // collisions in 1000 draws from ~1B space ≈ none
		t.Fatalf("suspicious duplicate rate: %d unique of 1000", len(seen))
	}
}

func TestValidCode(t *testing.T) {
	for _, bad := range []string{"", "abc", "ABCDE", "ABCDEFG", "ABC DE", "../../x", "ABCDE0", "ABCDEO"} {
		if validCode(bad) {
			t.Errorf("validCode(%q) = true, want false", bad)
		}
	}
	if !validCode("AB23YZ") {
		t.Errorf("validCode(AB23YZ) = false, want true")
	}
}
```

Note: `O`, `I`, `0`, `1` are excluded from the alphabet, and `validCode` rejects them too so lookups can't be tricked with lookalikes.

- [ ] **Step 2: Run to verify failure**

Run: `go test . -run 'TestNewRoomCode|TestValidCode' -v`
Expected: FAIL — `undefined: newRoomCode`

- [ ] **Step 3: Implement**

`code.go`:

```go
package main

import (
	"crypto/rand"
	"regexp"
)

// 32 chars (no O/I/0/1 — unambiguous when read aloud or typed).
// len 32 divides 256 evenly, so the modulo below has zero bias.
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
const codeLen = 6

var codeRe = regexp.MustCompile(`^[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{6}$`)

func newRoomCode() string {
	b := make([]byte, codeLen)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure means a broken host
	}
	for i := range b {
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(b)
}

func validCode(s string) bool { return codeRe.MatchString(s) }
```

- [ ] **Step 4: Run to verify pass**

Run: `go test . -run 'TestNewRoomCode|TestValidCode' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add code.go code_test.go && git commit -m "Room codes: crypto/rand 6-char unambiguous alphabet"
```

---

### Task 3: Per-IP rate limiter

**Files:**
- Create: `ratelimit.go`, `ratelimit_test.go`

- [ ] **Step 1: Write failing tests**

`ratelimit_test.go`:

```go
package main

import (
	"testing"
	"time"
)

func TestRateLimiterBurstThenDeny(t *testing.T) {
	rl := newRateLimiter(3, 0.001) // 3 burst, negligible refill
	for i := 0; i < 3; i++ {
		if !rl.allow("1.2.3.4") {
			t.Fatalf("call %d: denied within burst", i)
		}
	}
	if rl.allow("1.2.3.4") {
		t.Fatal("4th call allowed, want denied")
	}
	if !rl.allow("5.6.7.8") {
		t.Fatal("different key denied, want allowed")
	}
}

func TestRateLimiterRefill(t *testing.T) {
	rl := newRateLimiter(1, 100) // refills fast: 100 tokens/sec
	if !rl.allow("k") {
		t.Fatal("first call denied")
	}
	if rl.allow("k") {
		t.Fatal("second immediate call allowed")
	}
	time.Sleep(30 * time.Millisecond) // ~3 tokens refilled, capped at burst 1
	if !rl.allow("k") {
		t.Fatal("call after refill denied")
	}
}

func TestRateLimiterSweep(t *testing.T) {
	rl := newRateLimiter(1, 1)
	rl.allow("old")
	rl.sweep(0) // everything older than now is dropped
	rl.mu.Lock()
	n := len(rl.visitors)
	rl.mu.Unlock()
	if n != 0 {
		t.Fatalf("visitors after sweep = %d, want 0", n)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test . -run TestRateLimiter -v`
Expected: FAIL — `undefined: newRateLimiter`

- [ ] **Step 3: Implement**

`ratelimit.go`:

```go
package main

import (
	"sync"
	"time"
)

// rateLimiter is a per-key token bucket: each key starts with `burst`
// tokens and refills at `perSec` tokens per second, capped at burst.
type rateLimiter struct {
	mu       sync.Mutex
	burst    float64
	perSec   float64
	visitors map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(burst int, perSec float64) *rateLimiter {
	return &rateLimiter{burst: float64(burst), perSec: perSec, visitors: map[string]*bucket{}}
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	b, ok := rl.visitors[key]
	if !ok {
		rl.visitors[key] = &bucket{tokens: rl.burst - 1, last: now}
		return true
	}
	b.tokens += now.Sub(b.last).Seconds() * rl.perSec
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep drops keys idle longer than olderThan so the map can't grow forever.
func (rl *rateLimiter) sweep(olderThan time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cutoff := time.Now().Add(-olderThan)
	for k, b := range rl.visitors {
		if b.last.Before(cutoff) {
			delete(rl.visitors, k)
		}
	}
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test . -run TestRateLimiter -v -race`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add ratelimit.go ratelimit_test.go && git commit -m "Per-IP token-bucket rate limiter with idle sweep"
```

---

### Task 4: Hub and Room

**Files:**
- Create: `hub.go`, `hub_test.go`
- Read for reference: `game/persist.go` (Save/Load), `game/game.go:85-149` (Game struct, Changed, Phase)

- [ ] **Step 1: Write failing tests**

`hub_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testHub(t *testing.T) *Hub {
	t.Helper()
	h, err := newHub(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHubCreateGetExpire(t *testing.T) {
	h := testHub(t)
	r, err := h.create()
	if err != nil {
		t.Fatal(err)
	}
	if !validCode(r.Code) {
		t.Fatalf("bad code %q", r.Code)
	}
	if h.get(r.Code) != r {
		t.Fatal("get after create returned different room")
	}
	if h.get("ZZZZZZ") != nil {
		t.Fatal("get of unknown code returned a room")
	}
	if h.get("../../etc") != nil {
		t.Fatal("get of invalid code returned a room")
	}

	// force-idle the room beyond the lobby TTL, then expire
	r.mu.Lock()
	r.lastActive = time.Now().Add(-2 * lobbyTTL)
	r.mu.Unlock()
	h.expire()
	if h.get(r.Code) != nil {
		t.Fatal("expired lobby room still present")
	}
}

func TestHubMaxRooms(t *testing.T) {
	h := testHub(t)
	h.maxRooms = 2
	if _, err := h.create(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.create(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.create(); err == nil {
		t.Fatal("create beyond maxRooms succeeded")
	}
}

func TestHubPersistRestore(t *testing.T) {
	dir := t.TempDir()
	h1, _ := newHub(dir)
	r, _ := h1.create()
	if _, err := r.G.Join("alice", "", false); err != nil {
		t.Fatal(err)
	}
	h1.saveAll()
	if _, err := os.Stat(filepath.Join(dir, r.Code+".gob")); err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}

	h2, _ := newHub(dir)
	if err := h2.restore(); err != nil {
		t.Fatal(err)
	}
	got := h2.get(r.Code)
	if got == nil {
		t.Fatal("room not restored")
	}
	got.G.Mu.Lock()
	n := len(got.G.Players)
	got.G.Mu.Unlock()
	if n != 1 {
		t.Fatalf("restored players = %d, want 1", n)
	}
}

func TestHubExpireDeletesSnapshot(t *testing.T) {
	dir := t.TempDir()
	h, _ := newHub(dir)
	r, _ := h.create()
	h.saveAll()
	r.mu.Lock()
	r.lastActive = time.Now().Add(-2 * gameTTL)
	r.mu.Unlock()
	h.expire()
	if _, err := os.Stat(filepath.Join(dir, r.Code+".gob")); !os.IsNotExist(err) {
		t.Fatal("snapshot survived room expiry")
	}
}

func TestHubConcurrent(t *testing.T) {
	h := testHub(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := h.create()
			if err != nil {
				return // hitting maxRooms is fine
			}
			h.get(r.Code)
			h.expire()
		}()
	}
	wg.Wait()
}

func TestFanoutKicksClientsAndSaves(t *testing.T) {
	h := testHub(t)
	r, _ := h.create()
	kick := make(chan struct{}, 1)
	r.addClient(kick)
	defer r.removeClient(kick)

	if _, err := r.G.Join("bob", "", false); err != nil { // mutation → Changed → fanout
		t.Fatal(err)
	}
	select {
	case <-kick:
	case <-time.After(2 * time.Second):
		t.Fatal("client not kicked after state change")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(h.path(r.Code)); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("snapshot not saved after state change")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
```

(`game.Game.Players` is the exported player slice — verified at `game/game.go:90`.)

- [ ] **Step 2: Run to verify failure**

Run: `go test . -run TestHub -v`
Expected: FAIL — `undefined: Hub`

- [ ] **Step 3: Implement**

`hub.go`:

```go
package main

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mitayshvim/game"
)

const (
	defaultMaxRooms = 200
	lobbyTTL        = 1 * time.Hour
	gameTTL         = 24 * time.Hour
	maxSSEPerRoom   = 10
	maxSSETotal     = 500
)

var errServerFull = errors.New("server is full, try again later")

// Room wraps one game with its SSE clients and idle tracking.
type Room struct {
	Code string
	G    *game.Game

	mu         sync.Mutex
	clients    map[chan struct{}]bool
	lastActive time.Time
	done       chan struct{} // closed on expiry; stops the fanout goroutine
}

func (r *Room) touch() {
	r.mu.Lock()
	r.lastActive = time.Now()
	r.mu.Unlock()
}

func (r *Room) addClient(ch chan struct{}) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.clients) >= maxSSEPerRoom {
		return false
	}
	r.clients[ch] = true
	return true
}

func (r *Room) removeClient(ch chan struct{}) {
	r.mu.Lock()
	delete(r.clients, ch)
	r.mu.Unlock()
}

// fanout saves a snapshot and kicks every SSE client on each state change.
// One goroutine per room; exits when the room expires.
func (r *Room) fanout(statePath string) {
	for {
		select {
		case <-r.done:
			return
		case <-r.G.Changed:
			if err := r.G.Save(statePath); err != nil {
				log.Printf("save room %s: %v", r.Code, err)
			}
			r.mu.Lock()
			for ch := range r.clients {
				select {
				case ch <- struct{}{}:
				default:
				}
			}
			r.mu.Unlock()
		}
	}
}

// Hub owns all rooms and the snapshot directory.
type Hub struct {
	mu       sync.Mutex
	rooms    map[string]*Room
	dataDir  string
	maxRooms int
}

func newHub(dataDir string) (*Hub, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	return &Hub{rooms: map[string]*Room{}, dataDir: dataDir, maxRooms: defaultMaxRooms}, nil
}

func (h *Hub) path(code string) string { return filepath.Join(h.dataDir, code+".gob") }

func (h *Hub) create() (*Room, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.rooms) >= h.maxRooms {
		return nil, errServerFull
	}
	for {
		code := newRoomCode()
		if _, exists := h.rooms[code]; exists {
			continue
		}
		r := h.addLocked(code, game.New())
		return r, nil
	}
}

// addLocked registers a room and starts its fanout. Caller holds h.mu.
func (h *Hub) addLocked(code string, g *game.Game) *Room {
	r := &Room{
		Code:       code,
		G:          g,
		clients:    map[chan struct{}]bool{},
		lastActive: time.Now(),
		done:       make(chan struct{}),
	}
	h.rooms[code] = r
	go r.fanout(h.path(code))
	return r
}

// get returns nil for invalid or unknown codes — uniformly.
func (h *Hub) get(code string) *Room {
	if !validCode(code) {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.rooms[code]
}

// expire removes rooms idle beyond their TTL and deletes their snapshots.
// Run from the janitor ticker; one pass so tests can call it directly.
func (h *Hub) expire() {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	for code, r := range h.rooms {
		r.G.Mu.Lock()
		inLobby := r.G.Phase == game.PhaseLobby
		r.G.Mu.Unlock()
		ttl := gameTTL
		if inLobby {
			ttl = lobbyTTL
		}
		r.mu.Lock()
		idle := now.Sub(r.lastActive)
		r.mu.Unlock()
		if idle <= ttl {
			continue
		}
		close(r.done)
		delete(h.rooms, code)
		if err := os.Remove(h.path(code)); err != nil && !os.IsNotExist(err) {
			log.Printf("remove snapshot %s: %v", code, err)
		}
	}
}

// restore loads every readable snapshot in dataDir as a room.
func (h *Hub) restore() error {
	entries, err := os.ReadDir(h.dataDir)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range entries {
		code := strings.TrimSuffix(e.Name(), ".gob")
		if !strings.HasSuffix(e.Name(), ".gob") || !validCode(code) {
			continue
		}
		g, err := game.Load(filepath.Join(h.dataDir, e.Name()))
		if err != nil {
			log.Printf("skip unreadable snapshot %s: %v", e.Name(), err)
			continue
		}
		h.addLocked(code, g)
	}
	return nil
}

// saveAll snapshots every room; used at graceful shutdown.
func (h *Hub) saveAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for code, r := range h.rooms {
		if err := r.G.Save(h.path(code)); err != nil {
			log.Printf("save room %s: %v", code, err)
		}
	}
}

// snapshot returns the current rooms; used by the bot ticker without
// holding the hub lock while stepping bots.
func (h *Hub) snapshot() []*Room {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*Room, 0, len(h.rooms))
	for _, r := range h.rooms {
		out = append(out, r)
	}
	return out
}
```

Check `game/game.go` for the exact exported names of `Phase`/`PhaseLobby`/`Players` and adjust if they differ (they exist — `view.go:106` references `g.Phase == PhaseLobby`).

- [ ] **Step 4: Run to verify pass**

Run: `go test . -run 'TestHub|TestFanout' -v -race`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add hub.go hub_test.go && git commit -m "Hub + Room: per-room games, fanout, snapshots, TTL expiry"
```

---

### Task 5: HTTP server — routes, middleware, hardening

**Files:**
- Create: `httpserver.go`, `httpserver_test.go`
- Reference: current `main.go:106-205` (handlers being moved/adapted)

- [ ] **Step 1: Write failing tests**

`httpserver_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func testServer(t *testing.T) *server {
	t.Helper()
	h, err := newHub(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return newServer(h, nil) // nil fsys: API tests don't serve files
}

func do(t *testing.T, s *server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "9.9.9.9:1234"
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)
	return w
}

func TestCreateAndJoinRoom(t *testing.T) {
	s := testServer(t)
	w := do(t, s, "POST", "/api/rooms", "")
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	code := s.hub.snapshot()[0].Code
	w = do(t, s, "POST", "/api/r/"+code+"/join", `{"name":"alice"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "token") {
		t.Fatalf("join: %d %s", w.Code, w.Body)
	}
}

func TestUnknownAndInvalidRoomUniform404(t *testing.T) {
	s := testServer(t)
	a := do(t, s, "POST", "/api/r/ZZZZZZ/join", `{"name":"x"}`)
	b := do(t, s, "POST", "/api/r/BADCO1/join", `{"name":"x"}`) // invalid char '1'
	if a.Code != 404 || b.Code != 404 {
		t.Fatalf("want uniform 404, got %d and %d", a.Code, b.Code)
	}
	if a.Body.String() != b.Body.String() {
		t.Fatal("404 bodies differ — leaks which codes exist")
	}
}

func TestOversizedBodyRejected(t *testing.T) {
	s := testServer(t)
	do(t, s, "POST", "/api/rooms", "")
	code := s.hub.snapshot()[0].Code
	big := `{"name":"` + strings.Repeat("a", 8192) + `"}`
	w := do(t, s, "POST", "/api/r/"+code+"/join", big)
	if w.Code != 400 {
		t.Fatalf("oversized body: %d, want 400", w.Code)
	}
}

func TestNameSanitized(t *testing.T) {
	s := testServer(t)
	do(t, s, "POST", "/api/rooms", "")
	code := s.hub.snapshot()[0].Code
	w := do(t, s, "POST", "/api/r/"+code+"/join", `{"name":"a b\nc xxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}`)
	if w.Code != 200 {
		t.Fatalf("join: %d %s", w.Code, w.Body)
	}
	var resp struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(resp.Name, "\n\r\t") {
		t.Fatalf("control chars survived sanitization: %q", resp.Name)
	}
	if utf8.RuneCountInString(resp.Name) > maxNameRunes {
		t.Fatalf("name not truncated: %q", resp.Name)
	}
}

func TestCreateRoomRateLimited(t *testing.T) {
	s := testServer(t)
	denied := false
	for i := 0; i < 10; i++ {
		if w := do(t, s, "POST", "/api/rooms", ""); w.Code == 429 {
			denied = true
			break
		}
	}
	if !denied {
		t.Fatal("10 rapid room creations from one IP never hit 429")
	}
}

func TestSecurityHeadersAndHealthz(t *testing.T) {
	s := testServer(t)
	w := do(t, s, "GET", "/healthz", "")
	if w.Code != 200 {
		t.Fatalf("healthz: %d", w.Code)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff header")
	}
	if w.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("missing CSP header")
	}
}

func TestClientIPProxyTrust(t *testing.T) {
	// X-Forwarded-For honored only from loopback (the local Caddy)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	if ip := clientIP(req); ip != "203.0.113.7" {
		t.Fatalf("loopback proxy: got %q", ip)
	}
	req.RemoteAddr = "198.51.100.2:5555" // direct external — header is a lie
	if ip := clientIP(req); ip != "198.51.100.2" {
		t.Fatalf("external spoof: got %q", ip)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test . -run 'TestCreate|TestUnknown|TestOversized|TestName|TestSecurity|TestClientIP' -v`
Expected: FAIL — `undefined: newServer`

- [ ] **Step 3: Implement**

`httpserver.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"mitayshvim/game"
)

const maxBodyBytes = 4096
const maxNameRunes = 24

type server struct {
	hub  *Hub
	fsys fs.FS // web assets (disk or embedded); may be nil in tests

	createRL *rateLimiter // room creation: 3 burst, ~1 per 5 min
	apiRL    *rateLimiter // room-scoped API: 20 burst, 5/sec
	logRL    *rateLimiter // clientlog: 5 burst, 1 per 10 sec

	sseMu    sync.Mutex
	sseTotal int
}

func newServer(hub *Hub, fsys fs.FS) *server {
	return &server{
		hub:      hub,
		fsys:     fsys,
		createRL: newRateLimiter(3, 1.0/300),
		apiRL:    newRateLimiter(20, 5),
		logRL:    newRateLimiter(5, 0.1),
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		io.WriteString(w, "ok")
	})
	mux.HandleFunc("POST /api/rooms", s.handleCreateRoom)
	mux.HandleFunc("POST /api/r/{code}/join", s.withRoom(s.handleJoin))
	mux.HandleFunc("POST /api/r/{code}/action", s.withRoom(s.handleAction))
	mux.HandleFunc("GET /api/r/{code}/events", s.withRoom(s.handleEvents))
	mux.HandleFunc("POST /api/clientlog", s.handleClientLog)
	if s.fsys != nil {
		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, s.fsys, "landing.html")
		})
		mux.HandleFunc("GET /r/{code}", func(w http.ResponseWriter, r *http.Request) {
			if s.hub.get(r.PathValue("code")) == nil {
				http.Redirect(w, r, "/?missing=1", http.StatusFound)
				return
			}
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, s.fsys, "index.html")
		})
		assets := http.FileServerFS(s.fsys)
		for _, p := range []string{"GET /vendor/", "GET /board3d/"} {
			mux.Handle(p, noCache(assets))
		}
	}
	return securityHeaders(mux)
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("X-Frame-Options", "DENY")
		// 'unsafe-inline' is required: the SPA is a single file with inline
		// script/styles. connect/img/script sources are still locked to self,
		// which blocks exfiltration and external script injection.
		hd.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'")
		h.ServeHTTP(w, r)
	})
}

// clientIP trusts X-Forwarded-For only when the direct peer is loopback —
// i.e. the local Caddy proxy. Anyone else gets judged by RemoteAddr.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	return host
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *server) handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	if !s.createRL.allow(clientIP(r)) {
		writeJSON(w, 429, map[string]string{"error": "too many games created, wait a few minutes"})
		return
	}
	room, err := s.hub.create()
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"code": room.Code})
}

func (s *server) withRoom(h func(*Room, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.apiRL.allow(clientIP(r)) {
			writeJSON(w, 429, map[string]string{"error": "slow down"})
			return
		}
		room := s.hub.get(r.PathValue("code"))
		if room == nil {
			writeJSON(w, 404, map[string]string{"error": "room not found"})
			return
		}
		h(room, w, r)
	}
}

// cleanName keeps printable runes, trims space, caps length.
func cleanName(s string) string {
	s = strings.TrimSpace(s)
	out := make([]rune, 0, maxNameRunes)
	for _, r := range s {
		if !unicode.IsPrint(r) {
			continue
		}
		out = append(out, r)
		if len(out) == maxNameRunes {
			break
		}
	}
	return string(out)
}

func (s *server) handleJoin(room *Room, w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req struct {
		Name   string `json:"name"`
		Token  string `json:"token"`
		Resume bool   `json:"resume"`
		Claim  *int   `json:"claim"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	req.Name = cleanName(req.Name)
	var p *game.Player
	var err error
	if req.Claim != nil {
		p, err = room.G.ClaimSeat(*req.Claim)
	} else {
		p, err = room.G.Join(req.Name, req.Token, req.Resume)
	}
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	room.touch()
	writeJSON(w, 200, map[string]any{"token": p.Token, "id": p.ID, "name": p.Name})
}

func (s *server) handleAction(room *Room, w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req struct {
		Token string `json:"token"`
		game.Action
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	if err := room.G.Do(req.Token, req.Action); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	room.touch()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *server) sseAcquire() bool {
	s.sseMu.Lock()
	defer s.sseMu.Unlock()
	if s.sseTotal >= maxSSETotal {
		return false
	}
	s.sseTotal++
	return true
}

func (s *server) sseRelease() {
	s.sseMu.Lock()
	s.sseTotal--
	s.sseMu.Unlock()
}

func (s *server) handleEvents(room *Room, w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	if !s.sseAcquire() {
		writeJSON(w, 503, map[string]string{"error": "server busy"})
		return
	}
	defer s.sseRelease()

	kick := make(chan struct{}, 1)
	if !room.addClient(kick) {
		writeJSON(w, 503, map[string]string{"error": "room is full"})
		return
	}
	defer room.removeClient(kick)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	send := func() bool {
		data, err := room.G.ViewJSON(token)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	if !send() {
		return
	}
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-kick:
			if !send() {
				return
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

// handleClientLog accepts browser error reports. Unauthenticated, so it is
// rate-limited and sanitized before reaching the server log.
func (s *server) handleClientLog(w http.ResponseWriter, r *http.Request) {
	if !s.logRL.allow(clientIP(r)) {
		w.WriteHeader(429)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	clean := strings.Map(func(c rune) rune {
		if c == '\n' || c == '\r' || !unicode.IsPrint(c) {
			return ' '
		}
		return c
	}, string(body))
	log.Printf("CLIENT-ERROR [%s] %s", clientIP(r), clean)
	w.WriteHeader(204)
}
```

- [ ] **Step 4: Player tokens → crypto/rand (engine change, threat-model #3)**

`game/game.go:227` generates player tokens from the game's `math/rand/v2`
PCG — predictable if an attacker recovers the generator state from observed
outputs (dice rolls leak it). Replace with crypto/rand:

```go
// game/game.go — add imports:
import (
	crand "crypto/rand"
	"encoding/hex"
)

// replace func newToken(rng *rand.Rand) string { ... } with:
func newToken() string {
	b := make([]byte, 16) // 128-bit
	if _, err := crand.Read(b); err != nil {
		panic(err) // crypto/rand failure means a broken host
	}
	return hex.EncodeToString(b)
}
```

Update both call sites (`game/game.go:193` and `:221`): `newToken(g.Rng)` → `newToken()`.

Run: `go test ./game/ -race`
Expected: PASS (token format unchanged: opaque string).

- [ ] **Step 5: Run to verify pass**

Run: `go test ./... -v -race`
Expected: all PASS (main.go still compiles with old handlers — if duplicate symbols collide (`writeJSON` exists in `main.go`), delete the old copies from `main.go` now; full main.go rewrite is Task 6).

- [ ] **Step 6: Commit**

```bash
git add httpserver.go httpserver_test.go main.go game/game.go && git commit -m "Room-scoped HTTP API with rate limits, body caps, security headers; crypto/rand player tokens"
```

---

### Task 6: main.go rewrite — wiring + graceful shutdown

**Files:**
- Modify: `main.go` (full replacement)

- [ ] **Step 1: Replace main.go**

```go
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//go:embed web
var webFS embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	dataDir := flag.String("data", "data", "directory for game snapshots")
	flag.Parse()

	hub, err := newHub(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	if err := hub.restore(); err != nil {
		log.Printf("restore: %v", err)
	}

	// Serve the UI from ./web when present (live-editable without a
	// rebuild); fall back to the copy embedded in the binary.
	var fsys fs.FS
	if _, err := os.Stat("web/index.html"); err == nil {
		fsys = os.DirFS("web")
	} else {
		fsys, _ = fs.Sub(webFS, "web")
	}
	s := newServer(hub, fsys)

	go func() { // paced bot play across all rooms
		for range time.Tick(800 * time.Millisecond) {
			for _, r := range hub.snapshot() {
				r.G.BotStep()
			}
		}
	}()
	go func() { // janitor: expire idle rooms, sweep limiter maps
		for range time.Tick(5 * time.Minute) {
			hub.expire()
			s.createRL.sweep(time.Hour)
			s.apiRL.sweep(time.Hour)
			s.logRL.sweep(time.Hour)
		}
	}()

	srv := &http.Server{
		Addr:              *addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("Mitayshvim server listening on %s", *addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	log.Println("shutting down: snapshotting rooms")
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		srv.Close() // SSE streams never end on their own — cut them
	}
	hub.saveAll()
}
```

Delete from the old `main.go`: `server` struct, `fanout`, `handleJoin`, `handleAction`, `handleEvents`, `writeJSON`, `lanIPs`, the `JoinURL` assignment, the clientlog handler. (`game.Game.JoinURL` field stays in the engine — unset means `omitempty` drops it from views; engine untouched.)

- [ ] **Step 2: Build, vet, test**

Run: `gofmt -l . && go vet ./... && go test ./... -race`
Expected: no gofmt output, vet clean, all PASS

- [ ] **Step 3: Smoke test**

Run: `go run . -addr 127.0.0.1:8090 -data /tmp/mv-smoke &` then
`curl -s -X POST localhost:8090/api/rooms` → `{"code":"XXXXXX"}`;
`curl -s -X POST localhost:8090/api/r/<code>/join -d '{"name":"al"}'` → token JSON;
`curl -s localhost:8090/healthz` → `ok`. Kill with SIGTERM, confirm "snapshotting rooms" in log and `/tmp/mv-smoke/<code>.gob` exists.

- [ ] **Step 4: Commit**

```bash
git add main.go && git commit -m "Wire hub into main: bot ticker, janitor, graceful shutdown"
```

---

### Task 7: Client — room scoping

**Files:**
- Modify: `web/index.html`

- [ ] **Step 1: Room detection at script top**

Immediately at the start of the main inline `<script>` (before `seats` is loaded, currently around line 530), add:

```js
const ROOM = (location.pathname.match(/^\/r\/([A-Z2-9]{6})$/) || [])[1];
if (!ROOM) location.replace('/');
const API = '/api/r/' + ROOM;
```

- [ ] **Step 2: Room-scoped API calls**

| Line (pre-edit) | Old | New |
|---|---|---|
| 554 | `fetch('/api/join', …)` | `fetch(API + '/join', …)` |
| 730 | `fetch('/api/join', …)` | `fetch(API + '/join', …)` |
| 1653 | `fetch('/api/join', …)` | `fetch(API + '/join', …)` |
| 571 | `fetch('/api/action', …)` | `fetch(API + '/action', …)` |
| 587 | `fetch('/api/action', …)` | `fetch(API + '/action', …)` |
| 566 | `new EventSource('/api/events?token='…)` | `new EventSource(API + '/events?token='…)` |

`/api/clientlog` (line 501) stays global — unchanged.

- [ ] **Step 3: Per-room localStorage keys**

Lines 532/535/626/649/1650/1657: replace `'catan_seats'` → `'mv_seats:' + ROOM`, `'catan_mute'` → `'mv_mute'` (mute is global, not per room), `'catan_token'` → delete that removeItem line entirely (legacy cleanup for a key that no longer exists).

- [ ] **Step 4: Share URL client-side**

Lines 743–753: replace every `S.joinURL` with a local constant. Above that block add `const SHARE_URL = location.origin + '/r/' + ROOM;` and use `SHARE_URL` for the QR (`qr.addData(SHARE_URL)`) and the visible link (`el('joinUrl').textContent = SHARE_URL`). Change the surrounding `if (S.joinURL && …)` condition to gate on lobby phase instead: `if (S.phase === 'lobby' && typeof qrcode === 'function')` — check the actual phase field name used elsewhere in `render()` and reuse it. Also display the bare room code prominently next to the QR: add `<div id="roomCode"></div>` near `joinUrl` in the markup and set `el('roomCode').textContent = 'Room: ' + ROOM;`.

- [ ] **Step 5: Absolute asset URLs (relative paths break under /r/{code})**

| Line | Old | New |
|---|---|---|
| 12 | `src="vendor/qrcode.js?v=2"` | `src="/vendor/qrcode.js?v=2"` |
| 13 | `"three":"./vendor/three.module.min.js?v=2"` | `"three":"/vendor/three.module.min.js?v=2"` |
| 16 | `import('./board3d/board3d.js?v=2')` | `import('/board3d/board3d.js?v=2')` |

Also `grep -n "'\./\|\"\./" web/index.html web/board3d/board3d.js` for any other relative fetches/imports and make them absolute.

- [ ] **Step 6: Manual verify**

Run server (`go run . -addr 127.0.0.1:8090 -data /tmp/mv-smoke`), `curl -X POST localhost:8090/api/rooms`, open `http://localhost:8090/r/<code>` in browser: 3D board loads, join works, second browser tab same URL joins same game, QR shows room URL. Opening `/r/AAAAAA` (nonexistent) redirects to `/`.

- [ ] **Step 7: Commit**

```bash
git add web/index.html && git commit -m "Client: room-scoped API paths, per-room storage, client-side share URL"
```

---

### Task 8: Landing page

**Files:**
- Create: `web/landing.html`

- [ ] **Step 1: Write the page**

```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Small Mitayshvim</title>
<style>
  body { margin:0; min-height:100vh; display:flex; align-items:center; justify-content:center;
         background:#0b2239; color:#f1ede3; font-family:system-ui, sans-serif; }
  .card { background:#13314f; border-radius:16px; padding:32px 28px; width:min(92vw,380px);
          box-shadow:0 8px 40px rgba(0,0,0,.4); text-align:center; }
  h1 { margin:0 0 4px; letter-spacing:.08em; }
  h1 small { display:block; font-size:.45em; opacity:.7; letter-spacing:.3em; }
  button { width:100%; padding:14px; margin-top:18px; font-size:1.05em; border:0; border-radius:10px;
           background:#f3a712; color:#222; font-weight:700; cursor:pointer; }
  button:active { transform:scale(.98); }
  .or { margin:18px 0 8px; opacity:.6; }
  input { width:100%; box-sizing:border-box; padding:12px; font-size:1.2em; text-align:center;
          letter-spacing:.35em; text-transform:uppercase; border-radius:10px; border:1px solid #2a6fdb;
          background:#0b2239; color:#f1ede3; }
  .err { color:#ff9d9d; min-height:1.2em; margin-top:10px; font-size:.9em; }
</style>
</head>
<body>
<div class="card">
  <h1>MITAYSHVIM<small>SMALL · ONLINE</small></h1>
  <button id="create">Create game</button>
  <div class="or">— or join with a code —</div>
  <input id="code" maxlength="6" autocomplete="off" spellcheck="false" placeholder="ABC123">
  <div class="err" id="err"></div>
</div>
<script>
const err = (m) => document.getElementById('err').textContent = m;
if (new URLSearchParams(location.search).has('missing')) err('That game no longer exists.');
document.getElementById('create').onclick = async () => {
  err('');
  try {
    const r = await fetch('/api/rooms', { method: 'POST' });
    const d = await r.json();
    if (!r.ok) return err(d.error || 'could not create game');
    location.href = '/r/' + d.code;
  } catch (e) { err('network error'); }
};
const inp = document.getElementById('code');
inp.addEventListener('input', () => {
  const v = inp.value.toUpperCase().replace(/[^A-Z2-9]/g, '');
  inp.value = v;
  if (v.length === 6) location.href = '/r/' + v;
});
</script>
</body>
</html>
```

- [ ] **Step 2: Manual verify**

`http://localhost:8090/` → card renders, Create navigates into a fresh room, typing a valid code navigates, bad room redirects back with "no longer exists" message.

- [ ] **Step 3: Commit**

```bash
git add web/landing.html && git commit -m "Landing page: create game / join by code"
```

---

### Task 9: XSS audit

**Files:**
- Modify: `web/index.html` (only if violations found)

- [ ] **Step 1: Audit name rendering**

Run: `grep -n 'innerHTML' web/index.html` — for every hit that interpolates server data (`S.players[…].name`, log entries, trade text), confirm the interpolated value is escaped or switch to `textContent`. The SPA builds large HTML strings; player names inside template literals are the dangerous case. Add an escape helper if needed:

```js
const esc = (s) => String(s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
```

and wrap every `${…name…}` in `${esc(name)}`. Server already caps/sanitizes names (Task 5), this is defense in depth.

- [ ] **Step 2: Verify with hostile name**

Join via curl: `curl -X POST localhost:8090/api/r/<code>/join -d '{"name":"<img src=x onerror=alert(1)>"}'` then open the room in a browser. Expected: name renders as literal text, no alert, no broken layout.

- [ ] **Step 3: Commit**

```bash
git add web/index.html && git commit -m "Escape server-sourced strings in HTML templates"
```

---

### Task 10: E2E — room isolation

**Files:**
- Create: `/tmp/cattan-e2e/rooms.test.cjs` (harness conventions: playwright-core, `channel:'chrome'`, `waitUntil:'load'` — SSE keeps the network busy so `networkidle` times out)

- [ ] **Step 1: Write the test**

```js
const { chromium } = require('playwright-core');

const BASE = 'http://localhost:8091';

(async () => {
  const browser = await chromium.launch({ channel: 'chrome' });
  const ctxA = await browser.newContext();
  const ctxB = await browser.newContext();

  // create two rooms via API
  const mk = async () => (await (await fetch(BASE + '/api/rooms', { method: 'POST' })).json()).code;
  const roomA = await mk(), roomB = await mk();
  if (roomA === roomB) throw new Error('duplicate room codes');

  const pageA = await ctxA.newPage();
  await pageA.goto(`${BASE}/r/${roomA}`, { waitUntil: 'load' });
  await pageA.fill('#addNameInput', 'Alice');
  await pageA.keyboard.press('Enter');

  const pageB = await ctxB.newPage();
  await pageB.goto(`${BASE}/r/${roomB}`, { waitUntil: 'load' });
  await pageB.fill('#addNameInput', 'Bob');
  await pageB.keyboard.press('Enter');

  // isolation: Alice must not appear in room B and vice versa
  await pageA.waitForSelector('text=Alice', { timeout: 5000 });
  await pageB.waitForSelector('text=Bob', { timeout: 5000 });
  if (await pageB.locator('text=Alice').count()) throw new Error('LEAK: Alice visible in room B');
  if (await pageA.locator('text=Bob').count()) throw new Error('LEAK: Bob visible in room A');

  // reconnect: reload keeps the seat (per-room localStorage)
  await pageA.reload({ waitUntil: 'load' });
  await pageA.waitForSelector('text=Alice', { timeout: 5000 });

  // unknown room redirects to landing
  const pageC = await ctxA.newPage();
  await pageC.goto(`${BASE}/r/ZZZZZZ`, { waitUntil: 'load' });
  if (!pageC.url().endsWith('/?missing=1')) throw new Error('no redirect for unknown room: ' + pageC.url());

  await browser.close();
  console.log('ROOMS E2E PASS');
})().catch((e) => { console.error(e); process.exit(1); });
```

Adjust the two selectors (`#addNameInput`, `text=Alice`) against the real DOM if the join flow differs — check `web/index.html:1649` (`addNameInput` exists).

- [ ] **Step 2: Run it**

```bash
go run . -addr 127.0.0.1:8091 -data /tmp/mv-e2e &
node /tmp/cattan-e2e/rooms.test.cjs
kill %1
```
Expected: `ROOMS E2E PASS`. Test servers stay on :8090–:8094, never :8080.

- [ ] **Step 3: Copy test into repo for safekeeping**

```bash
mkdir -p e2e && cp /tmp/cattan-e2e/rooms.test.cjs e2e/
git add e2e/rooms.test.cjs && git commit -m "E2E: two-room isolation, reconnect, unknown-room redirect"
```

---

### Task 11: Deploy artifacts — Oracle VM

**Files:**
- Create: `deploy/Caddyfile`, `deploy/mitayshvim.service`, `deploy/README.md`

- [ ] **Step 1: Caddyfile**

```
# deploy/Caddyfile — replace the hostname with your DNS name (e.g. DuckDNS)
mitayshvim.duckdns.org {
	encode gzip
	reverse_proxy 127.0.0.1:8080
}
```

- [ ] **Step 2: systemd unit**

```ini
# deploy/mitayshvim.service
[Unit]
Description=Small Mitayshvim game server
After=network.target

[Service]
User=mitayshvim
WorkingDirectory=/opt/mitayshvim
ExecStart=/opt/mitayshvim/mitayshvim -addr 127.0.0.1:8080 -data /opt/mitayshvim/data
Restart=on-failure
TimeoutStopSec=15

# hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/mitayshvim/data
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

(SIGTERM on stop triggers the graceful shutdown + saveAll from Task 6.)

- [ ] **Step 3: deploy/README.md**

```markdown
# Deploying to Oracle Cloud Always Free

## 1. VM
- Create an Ampere A1 instance (VM.Standard.A1.Flex, 1 OCPU / 6GB is plenty), Ubuntu 24.04.
- In the subnet's Security List add ingress rules for TCP 80 and 443 (0.0.0.0/0). 22 is there by default.

## 2. OS firewall gotcha
Oracle's Ubuntu images ship iptables REJECT rules beyond the security list:

    sudo iptables -I INPUT 5 -p tcp --dport 80 -j ACCEPT
    sudo iptables -I INPUT 5 -p tcp --dport 443 -j ACCEPT
    sudo netfilter-persistent save

## 3. DNS
Free hostname: https://www.duckdns.org → point mitayshvim.duckdns.org at the VM's public IP.

## 4. Caddy (TLS + reverse proxy)
    sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https curl
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list
    sudo apt update && sudo apt install caddy
    sudo cp deploy/Caddyfile /etc/caddy/Caddyfile   # edit hostname first
    sudo systemctl reload caddy

## 5. App
On your machine:

    GOOS=linux GOARCH=arm64 go build -o mitayshvim .
    scp mitayshvim deploy/mitayshvim.service ubuntu@<vm-ip>:~

On the VM:

    sudo useradd -r -s /usr/sbin/nologin mitayshvim
    sudo mkdir -p /opt/mitayshvim/data
    sudo mv ~/mitayshvim /opt/mitayshvim/
    sudo chown -R mitayshvim:mitayshvim /opt/mitayshvim
    sudo mv ~/mitayshvim.service /etc/systemd/system/
    sudo systemctl daemon-reload
    sudo systemctl enable --now mitayshvim

## 6. Verify
    curl https://mitayshvim.duckdns.org/healthz   # → ok
Create a game in the browser, join from a phone (not on wifi) via the QR.

## 7. Redeploy
    GOOS=linux GOARCH=arm64 go build -o mitayshvim . && scp mitayshvim ubuntu@<vm-ip>:~
    ssh ubuntu@<vm-ip> 'sudo systemctl stop mitayshvim && sudo mv ~/mitayshvim /opt/mitayshvim/ && sudo chown mitayshvim:mitayshvim /opt/mitayshvim/mitayshvim && sudo systemctl start mitayshvim'
Running games survive: stop snapshots all rooms, start restores them.
```

- [ ] **Step 4: Commit**

```bash
git add deploy/ && git commit -m "Deploy artifacts: Caddyfile, systemd unit, Oracle VM runbook"
```

---

### Task 12: Final verification + adversarial security review

- [ ] **Step 1: Full check**

Run: `gofmt -l . && go vet ./... && go test ./... -race -count=1`
Expected: clean, all PASS.

- [ ] **Step 2: Abuse smoke (against :8090 dev server)**

```bash
# rate limit: 30 rapid creates → expect 429s after the burst
for i in $(seq 30); do curl -s -o /dev/null -w '%{http_code} ' -X POST localhost:8090/api/rooms; done; echo
# oversized body → 400
head -c 10000 /dev/zero | tr '\0' 'a' | curl -s -o /dev/null -w '%{http_code}\n' -X POST localhost:8090/api/r/AAAAAA/join -d @-
# code probing → uniform 404
curl -s localhost:8090/api/r/ZZZZZZ/join -X POST -d '{}'; curl -s localhost:8090/api/r/AB23YZ/join -X POST -d '{}'
```

- [ ] **Step 3: Security review**

Run the `security-review` skill on the full diff (`git diff <pre-plan-commit>..HEAD`). Fix every finding, commit fixes, re-run.

- [ ] **Step 4: Verify spec threat-model coverage**

Walk the 9-row threat table in the spec; for each control, point to the line of code or test that implements it. Any gap → fix before deploy.
