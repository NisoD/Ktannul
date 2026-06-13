package game

import (
	"math/rand/v2"
	"sync"
	"testing"
)

func newTestGame(t *testing.T, seed uint64, names ...string) *Game {
	t.Helper()
	g := New()
	g.Rng = rand.New(rand.NewPCG(seed, seed*13))
	for _, n := range names {
		if _, err := g.Join(n, "", false); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func TestTradeGating(t *testing.T) {
	g := newTestGame(t, 5, "a", "b")
	g.beginGame()
	g.Phase = PhaseMain
	g.Rolled = true
	p0, p1 := g.Players[0], g.Players[1]
	p0.Resources[Brick] = 2
	g.Bank[Brick] -= 2
	p1.Resources[Wood] = 2
	g.Bank[Wood] -= 2

	// like-for-like rejected
	if err := g.offerTrade(p0, map[string]int{Brick: 1}, map[string]int{Brick: 1}); err == nil {
		t.Fatal("like-for-like trade accepted")
	}
	if err := g.offerTrade(p0, map[string]int{Brick: 1}, map[string]int{Wood: 1}); err != nil {
		t.Fatal(err)
	}
	if err := g.respondTrade(p1, true); err != nil {
		t.Fatal(err)
	}
	// robber pending blocks confirmation
	g.RobberPending = true
	if err := g.confirmTrade(p0, 1); err == nil {
		t.Fatal("trade confirmed during robber resolution")
	}
	g.RobberPending = false
	// game over blocks confirmation
	g.Phase = PhaseEnded
	if err := g.confirmTrade(p0, 1); err == nil {
		t.Fatal("trade confirmed after game ended")
	}
	g.Phase = PhaseMain
	if err := g.confirmTrade(p0, 1); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveBotBehindHuman(t *testing.T) {
	g := newTestGame(t, 6, "host")
	host := g.Players[0]
	if err := g.addBot(host); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Join("late", "", false); err != nil {
		t.Fatal(err)
	}
	if err := g.removeBot(host); err != nil {
		t.Fatalf("cannot remove bot behind a human: %v", err)
	}
	if len(g.Players) != 2 {
		t.Fatalf("got %d players", len(g.Players))
	}
	for i, p := range g.Players {
		if p.ID != i || p.IsBot {
			t.Fatalf("bad reindex: seat %d -> id %d bot=%v", i, p.ID, p.IsBot)
		}
	}
}

func TestJoinNameRuneTruncation(t *testing.T) {
	g := newTestGame(t, 7)
	p, err := g.Join("aaaaaaaaaaaaaa🎲🎲🎲", "", false)
	if err != nil {
		t.Fatal(err)
	}
	r := []rune(p.Name)
	if len(r) != 16 || string(r[14:]) != "🎲🎲" {
		t.Fatalf("bad truncation: %q", p.Name)
	}
}

func TestClaimSeatRules(t *testing.T) {
	g := newTestGame(t, 8, "a", "b")
	if _, err := g.ClaimSeat(0, nil); err == nil {
		t.Fatal("claim allowed in lobby")
	}
	g.beginGame()
	old := g.Players[0].Token
	p, err := g.ClaimSeat(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Token == old {
		t.Fatal("token not rotated on claim")
	}
}

// Concurrent views/saves during a running bot game must be race-free
// (run with -race).
func TestConcurrentViewAndSave(t *testing.T) {
	g := newTestGame(t, 9, "a", "b", "c")
	for _, p := range g.Players {
		p.IsBot = true
	}
	if err := g.Do(g.Players[0].Token, Action{Type: "start"}); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for _, tok := range []string{g.Players[0].Token, g.Players[1].Token, ""} {
		wg.Add(1)
		go func(tok string) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if _, err := g.ViewJSON(tok); err != nil {
						t.Error(err)
						return
					}
				}
			}
		}(tok)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = g.Save(t.TempDir() + "/s.gob")
			}
		}
	}()
	for i := 0; i < 3000 && g.Phase != PhaseEnded; i++ {
		g.BotStep()
	}
	close(stop)
	wg.Wait()
}
