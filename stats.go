package main

import (
	"encoding/json"
	"log"
	"os"
	"sync"
)

// Stats holds cumulative, persisted counters for the public metrics page.
// Live game count is computed from the hub at query time, not stored here.
type Stats struct {
	mu            sync.Mutex
	path          string
	PlayersJoined int64 `json:"playersJoined"`
	GamesCreated  int64 `json:"gamesCreated"`
	GamesFinished int64 `json:"gamesFinished"`
}

func loadStats(path string) *Stats {
	s := &Stats{path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		return s // missing file → fresh counters
	}
	if err := json.Unmarshal(b, s); err != nil {
		log.Printf("stats: unreadable %s, starting fresh: %v", path, err)
	}
	return s
}

func (s *Stats) addPlayer()   { s.bump(&s.PlayersJoined) }
func (s *Stats) addGame()     { s.bump(&s.GamesCreated) }
func (s *Stats) addFinished() { s.bump(&s.GamesFinished) }

func (s *Stats) bump(n *int64) {
	s.mu.Lock()
	*n++
	s.mu.Unlock()
}

// snapshot returns a copy safe to marshal without holding the lock.
func (s *Stats) snapshot() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]int64{
		"playersJoined": s.PlayersJoined,
		"gamesCreated":  s.GamesCreated,
		"gamesFinished": s.GamesFinished,
	}
}

// save writes counters atomically (temp + rename) so a crash mid-write can't
// corrupt the file.
func (s *Stats) save() {
	s.mu.Lock()
	b, err := json.Marshal(s)
	s.mu.Unlock()
	if err != nil {
		log.Printf("stats: marshal: %v", err)
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		log.Printf("stats: write: %v", err)
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		log.Printf("stats: rename: %v", err)
	}
}
