package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"cattan/game"
)

//go:embed web
var webFS embed.FS

type server struct {
	g         *game.Game
	statePath string
	mu        sync.Mutex
	// each connected SSE client gets a kick channel; on game change we
	// kick everyone and they re-render their personalized view
	clients map[chan struct{}]bool
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	statePath := flag.String("state", ".cattan-state.gob", "file for game state persistence")
	flag.Parse()

	g, err := game.Load(*statePath)
	if err != nil {
		g = game.New()
	} else {
		fmt.Println("Restored saved game state.")
	}
	s := &server{g: g, statePath: *statePath, clients: map[chan struct{}]bool{}}
	go s.fanout()
	go func() { // paced bot play
		for range time.Tick(800 * time.Millisecond) {
			s.g.BotStep()
		}
	}()

	// Serve the UI from ./web when present (live-editable without a
	// rebuild); fall back to the copy embedded in the binary.
	var static http.Handler
	if _, err := os.Stat("web/index.html"); err == nil {
		static = http.FileServer(http.Dir("web"))
	} else {
		staticFS, _ := fs.Sub(webFS, "web")
		static = http.FileServer(http.FS(staticFS))
	}
	mux := http.NewServeMux()
	// no-cache: browsers revalidate on every load (304s keep it fast).
	// Without this a regular refresh can mix cached old JS with new files.
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		static.ServeHTTP(w, r)
	}))
	// client-side JS errors are POSTed here so they show up in the server
	// log — debugging phones/odd browsers without devtools access
	mux.HandleFunc("POST /api/clientlog", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
		log.Printf("CLIENT-ERROR [%s] %s", r.RemoteAddr, body)
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /api/join", s.handleJoin)
	mux.HandleFunc("POST /api/action", s.handleAction)
	mux.HandleFunc("GET /api/events", s.handleEvents)

	fmt.Println("Catan server running.")
	fmt.Println("Players on the same wifi can join at:")
	ips := lanIPs()
	for _, ip := range ips {
		fmt.Printf("  http://%s%s\n", ip, *addr)
	}
	g.JoinURL = fmt.Sprintf("http://%s%s", ips[0], *addr) // shown + QR-coded in the lobby
	log.Fatal(http.ListenAndServe(*addr, mux))
}

// fanout kicks every connected client whenever the game state changes,
// and checkpoints the game to disk so restarts resume mid-game.
func (s *server) fanout() {
	for range s.g.Changed {
		if err := s.g.Save(s.statePath); err != nil {
			log.Printf("save state: %v", err)
		}
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
		Claim  *int   `json:"claim"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	var p *game.Player
	var err error
	if req.Claim != nil {
		p, err = s.g.ClaimSeat(*req.Claim)
	} else {
		p, err = s.g.Join(req.Name, req.Token, req.Resume)
	}
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
		data, err := s.g.ViewJSON(token)
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
