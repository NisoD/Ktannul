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
		// which blocks exfiltration and external script injection. Google
		// Fonts is the one external origin the page uses.
		hd.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline'; "+
				"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; "+
				"font-src https://fonts.gstatic.com; "+
				"img-src 'self' data: blob:; connect-src 'self'")
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

// cleanName keeps printable runes, trims space, caps length. The engine
// applies its own (shorter) cap; this is the transport-layer gate.
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
