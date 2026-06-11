package game

import (
	"math/rand/v2"
	"testing"
)

// All-bot games must run to completion through BotStep alone.
func TestBotsFinishGame(t *testing.T) {
	for seed := uint64(1); seed <= 3; seed++ {
		g := New()
		g.Rng = rand.New(rand.NewPCG(seed, seed*31))
		for _, n := range []string{"a", "b", "c"} {
			p, err := g.Join(n, "", false)
			if err != nil {
				t.Fatal(err)
			}
			p.IsBot = true
		}
		if err := g.Do(g.Players[0].Token, Action{Type: "start"}); err != nil {
			t.Fatal(err)
		}
		for step := 0; step < 200000; step++ {
			checkInvariants(t, g)
			if g.Phase == PhaseEnded {
				if g.Winner < 0 || g.Points(g.Players[g.Winner], true) < WinPoints {
					t.Fatal("bad winner")
				}
				break
			}
			if !g.BotStep() {
				t.Fatalf("seed %d: bots stalled in phase %s turn %d", seed, g.Phase, g.Turn)
			}
		}
		if g.Phase != PhaseEnded {
			t.Fatalf("seed %d: bot game never finished (points %v)", seed, scores(g))
		}
	}
}
