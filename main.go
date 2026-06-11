package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"cattan/game"
)

//go:embed web
var webFS embed.FS

type server struct {
	g  *game.Game
	mu sync.Mutex
	// each connected SSE client gets a kick channel; on game change we
	// kick everyone and they re-render their personalized view
	clients map[chan struct{}]bool
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	s := &server{g: game.New(), clients: map[chan struct{}]bool{}}
	go s.fanout()

	staticFS, _ := fs.Sub(webFS, "web")
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("POST /api/join", s.handleJoin)
	mux.HandleFunc("POST /api/action", s.handleAction)
	mux.HandleFunc("GET /api/events", s.handleEvents)

	fmt.Println("Catan server running.")
	fmt.Println("Players on the same wifi can join at:")
	port := *addr
	for _, ip := range lanIPs() {
		fmt.Printf("  http://%s%s\n", ip, port)
	}
	log.Fatal(http.ListenAndServe(*addr, mux))
}

// fanout kicks every connected client whenever the game state changes.
func (s *server) fanout() {
	for range s.g.Changed {
		s.mu.Lock()
		for ch := range s.clients {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
		s.mu.Unlock()
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *server) handleJoin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string `json:"name"`
		Token  string `json:"token"`
		Resume bool   `json:"resume"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	if len(req.Name) > 16 {
		req.Name = req.Name[:16]
	}
	p, err := s.g.Join(req.Name, req.Token, req.Resume)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"token": p.Token, "id": p.ID, "name": p.Name})
}

func (s *server) handleAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
		game.Action
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	if err := s.g.Do(req.Token, req.Action); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	kick := make(chan struct{}, 1)
	s.mu.Lock()
	s.clients[kick] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, kick)
		s.mu.Unlock()
	}()

	send := func() bool {
		data, err := json.Marshal(s.g.ViewFor(token))
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

// lanIPs lists non-loopback IPv4 addresses so the host can share the URL.
func lanIPs() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return []string{"localhost"}
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				if ip4 := ipn.IP.To4(); ip4 != nil {
					out = append(out, ip4.String())
				}
			}
		}
	}
	if len(out) == 0 {
		out = []string{"localhost"}
	}
	return out
}
