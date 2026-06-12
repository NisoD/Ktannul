package game

import (
	"math/rand/v2"
	"testing"
)

func TestBotAcceptsFairScarcityTrade(t *testing.T) {
	g := New()
	g.Rng = rand.New(rand.NewPCG(11, 4))
	human, _ := g.Join("h", "", false)
	bp, _ := g.Join("b", "", false)
	bp.IsBot = true
	g.beginGame()
	g.Phase = PhaseMain
	g.Rolled = true

	// bot drowning in wood, has zero brick — a 1:1 brick-for-wood is good
	bp.Resources[Wood] = 4
	human.Resources[Brick] = 2
	if !g.botWantsTrade(bp, map[string]int{Brick: 1}, map[string]int{Wood: 1}) {
		t.Fatal("bot declined a clearly favorable fair trade")
	}
	// bot should refuse to give away a scarce resource for a surplus one
	bp.Resources[Brick] = 1
	bp.Resources[Wheat] = 0
	if g.botWantsTrade(bp, map[string]int{Wood: 1}, map[string]int{Brick: 1}) {
		t.Fatal("bot accepted trading scarce brick for surplus wood")
	}
}

func TestSoloStart(t *testing.T) {
	g := New()
	g.Rng = rand.New(rand.NewPCG(12, 4))
	h, _ := g.Join("solo", "", false)
	if err := g.Do(h.Token, Action{Type: "soloStart"}); err != nil {
		t.Fatal(err)
	}
	if len(g.Players) != 4 || g.Phase != PhaseSetup {
		t.Fatalf("players=%d phase=%s", len(g.Players), g.Phase)
	}
	bots := 0
	for _, p := range g.Players {
		if p.IsBot {
			bots++
		}
	}
	if bots != 3 {
		t.Fatalf("bots=%d", bots)
	}
}
