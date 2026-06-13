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
	t.Cleanup(h.stopAll) // stop fanout goroutines before TempDir removal
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
	for range 20 {
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
