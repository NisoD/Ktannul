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
	seatConns  map[int]int // seat ID → live SSE connection count
	lastActive time.Time
	done       chan struct{} // closed on expiry; stops the fanout goroutine
	stopOnce   sync.Once
}

// stop ends the room's fanout goroutine exactly once (idempotent: both
// expiry and hub shutdown may call it).
func (r *Room) stop() {
	r.stopOnce.Do(func() { close(r.done) })
}

// seatConnect/seatDisconnect track live presence per seat so an actively
// connected player can't have their seat claimed out from under them.
func (r *Room) seatConnect(id int) {
	r.mu.Lock()
	r.seatConns[id]++
	r.mu.Unlock()
}

func (r *Room) seatDisconnect(id int) {
	r.mu.Lock()
	if r.seatConns[id] > 0 {
		r.seatConns[id]--
		if r.seatConns[id] == 0 {
			delete(r.seatConns, id)
		}
	}
	r.mu.Unlock()
}

func (r *Room) seatLive(id int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seatConns[id] > 0
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
	wg       sync.WaitGroup // tracks fanout goroutines
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
		return h.addLocked(code, game.New()), nil
	}
}

// addLocked registers a room and starts its fanout. Caller holds h.mu.
func (h *Hub) addLocked(code string, g *game.Game) *Room {
	r := &Room{
		Code:       code,
		G:          g,
		clients:    map[chan struct{}]bool{},
		seatConns:  map[int]int{},
		lastActive: time.Now(),
		done:       make(chan struct{}),
	}
	h.rooms[code] = r
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		r.fanout(h.path(code))
	}()
	return r
}

// stopAll ends every room's fanout and waits for the goroutines to exit.
// After it returns, no goroutine will touch the data dir — used at graceful
// shutdown and in tests to avoid leaking goroutines.
func (h *Hub) stopAll() {
	h.mu.Lock()
	for _, r := range h.rooms {
		r.stop()
	}
	h.mu.Unlock()
	h.wg.Wait()
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
		r.stop()
		delete(h.rooms, code)
		// Known benign race: if fanout is mid-Save when we remove, the file
		// can reappear and the room resurrects on next boot — where it just
		// idles past its TTL and gets expired again. Self-healing, not worth
		// a sync barrier.
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
