package main

import (
	"encoding/json"
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
	for range 10 {
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
