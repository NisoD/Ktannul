package game

import (
	"math/rand/v2"
	"testing"
)

// Regression: map zero-value bug let player ID 0 build roads and
// settlements anywhere (empty RoadsE lookups returned 0 == player 0).
func TestPlacementRequiresConnection(t *testing.T) {
	g := New()
	g.Rng = rand.New(rand.NewPCG(3, 3))
	p0, _ := g.Join("zero", "", false)
	p1, _ := g.Join("one", "", false)
	g.beginGame()
	g.Phase = PhaseMain
	g.Rolled = true

	// Empty board: nobody can place a road or settlement anywhere.
	for _, e := range g.Board.Edges {
		if g.canPlaceRoad(p0, e.ID) {
			t.Fatalf("player 0 can place road on edge %d with no network", e.ID)
		}
	}
	for _, v := range g.Board.Verts {
		if g.canPlaceSettlement(p0, v.ID) {
			t.Fatalf("player 0 can place settlement on vertex %d with no roads", v.ID)
		}
	}

	// Opponent road must not enable player 0.
	e0 := g.Board.Edges[0]
	g.RoadsE[e0.ID] = p1.ID
	if g.canPlaceSettlement(p0, e0.V1) || g.canPlaceSettlement(p0, e0.V2) {
		t.Fatal("player 0 can settle on an opponent's road")
	}
	for _, e := range g.Board.Verts[e0.V1].Edges {
		if e != e0.ID && g.canPlaceRoad(p0, e) {
			t.Fatal("player 0 can branch off an opponent's road")
		}
	}

	// The owner CAN use their own road.
	if !g.canPlaceSettlement(p1, e0.V1) {
		t.Fatal("player 1 cannot settle on their own road end")
	}
	ok := false
	for _, e := range g.Board.Verts[e0.V1].Edges {
		if e != e0.ID && g.canPlaceRoad(p1, e) {
			ok = true
		}
	}
	if !ok {
		t.Fatal("player 1 cannot extend their own road")
	}
}
