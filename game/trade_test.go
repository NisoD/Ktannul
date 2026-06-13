package game

import (
	"math/rand/v2"
	"testing"
)

// mainGame3 sets up a 3-player game in the main phase, everyone holding a
// generous hand so trade validation never blocks the test.
func mainGame3(t *testing.T) (*Game, *Player, *Player, *Player) {
	t.Helper()
	g := New()
	g.Rng = rand.New(rand.NewPCG(7, 7))
	a, _ := g.Join("a", "", false)
	b, _ := g.Join("b", "", false)
	c, _ := g.Join("c", "", false)
	g.beginGame()
	g.Phase = PhaseMain
	g.Rolled = true
	for _, p := range g.Players {
		for _, r := range []string{Wood, Brick, Sheep, Wheat, Ore} {
			p.Resources[r] = 5
		}
	}
	return g, a, b, c
}

func TestTradeAutoClosesWhenAllDecline(t *testing.T) {
	g, a, b, c := mainGame3(t)
	if err := g.offerTrade(a, map[string]int{Wood: 1}, map[string]int{Ore: 1}); err != nil {
		t.Fatal(err)
	}
	if g.Trade == nil {
		t.Fatal("offer did not create a pending trade")
	}
	if err := g.respondTrade(b, false); err != nil {
		t.Fatal(err)
	}
	if g.Trade == nil {
		t.Fatal("trade closed after only one decline")
	}
	if err := g.respondTrade(c, false); err != nil {
		t.Fatal(err)
	}
	if g.Trade != nil {
		t.Fatal("trade did not auto-close after every other player declined")
	}
}

func TestTradeStaysOpenIfSomeoneAccepts(t *testing.T) {
	g, a, b, c := mainGame3(t)
	if err := g.offerTrade(a, map[string]int{Wood: 1}, map[string]int{Ore: 1}); err != nil {
		t.Fatal(err)
	}
	if err := g.respondTrade(b, true); err != nil {
		t.Fatal(err)
	}
	if err := g.respondTrade(c, false); err != nil {
		t.Fatal(err)
	}
	if g.Trade == nil {
		t.Fatal("trade closed even though one player accepted — offerer should still choose")
	}
}

func TestEmojiReaction(t *testing.T) {
	g, a, b, _ := mainGame3(t)
	if err := g.react(a, "🎲"); err != nil {
		t.Fatal(err)
	}
	if g.LastEmoji == nil || g.LastEmoji.Seat != a.ID || g.LastEmoji.Emoji != "🎲" || g.LastEmoji.Seq != 1 {
		t.Fatalf("first reaction: %+v", g.LastEmoji)
	}
	if err := g.react(b, "🔥"); err != nil {
		t.Fatal(err)
	}
	if g.LastEmoji.Seq != 2 || g.LastEmoji.Seat != b.ID {
		t.Fatalf("second reaction seq/seat wrong: %+v", g.LastEmoji)
	}
	if err := g.react(a, "<script>"); err == nil {
		t.Fatal("non-allowlisted reaction accepted")
	}
}
