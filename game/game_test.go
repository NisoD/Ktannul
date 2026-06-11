package game

import (
	"math/rand/v2"
	"testing"
)

func TestBoardInvariants(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 7))
	for i := 0; i < 50; i++ {
		b := GenerateBoard(rng)
		if len(b.Hexes) != 19 || len(b.Verts) != 54 || len(b.Edges) != 72 || len(b.Ports) != 9 {
			t.Fatalf("bad board shape: %d hexes %d verts %d edges %d ports",
				len(b.Hexes), len(b.Verts), len(b.Edges), len(b.Ports))
		}
		numbered, desert := 0, 0
		for _, h := range b.Hexes {
			if h.Terrain == Desert {
				desert++
				if h.ID != b.Robber {
					t.Fatal("robber should start on desert")
				}
			}
			if h.Number > 0 {
				numbered++
			}
		}
		if numbered != 18 || desert != 1 {
			t.Fatalf("numbered=%d desert=%d", numbered, desert)
		}
		// no adjacent 6/8
		for _, h := range b.Hexes {
			if h.Number != 6 && h.Number != 8 {
				continue
			}
			for _, h2 := range b.Hexes {
				if h2.ID != h.ID && (h2.Number == 6 || h2.Number == 8) &&
					hexDist([2]int{h.Q, h.R}, [2]int{h2.Q, h2.R}) == 1 {
					t.Fatal("adjacent red numbers")
				}
			}
		}
		// every vertex has 2-3 edges, every edge 1-2 hexes
		for _, v := range b.Verts {
			if len(v.Edges) < 2 || len(v.Edges) > 3 {
				t.Fatalf("vertex %d has %d edges", v.ID, len(v.Edges))
			}
		}
		for _, e := range b.Edges {
			if len(e.Hexes) < 1 || len(e.Hexes) > 2 {
				t.Fatalf("edge %d touches %d hexes", e.ID, len(e.Hexes))
			}
		}
	}
}

// TestFullGameSimulation plays complete games with random-but-greedy bots
// through the public action API and checks conservation invariants.
func TestFullGameSimulation(t *testing.T) {
	for seed := uint64(1); seed <= 5; seed++ {
		t.Run(string(rune('a'+seed)), func(t *testing.T) {
			simulate(t, seed)
		})
	}
}

func simulate(t *testing.T, seed uint64) {
	g := New()
	g.Rng = rand.New(rand.NewPCG(seed, seed*99))
	rng := rand.New(rand.NewPCG(seed*7, seed))

	names := []string{"ann", "bob", "cat"}
	for _, n := range names {
		if _, err := g.Join(n, "", false); err != nil {
			t.Fatal(err)
		}
	}
	must := func(err error) {
		if err != nil {
			t.Helper()
			t.Fatal(err)
		}
	}
	must(g.Do(g.Players[0].Token, Action{Type: "start"}))

	for step := 0; step < 100000; step++ {
		checkInvariants(t, g)
		if g.Phase == PhaseEnded {
			if g.Winner < 0 {
				t.Fatal("ended without winner")
			}
			if g.Points(g.Players[g.Winner], true) < WinPoints {
				t.Fatalf("winner has %d points", g.Points(g.Players[g.Winner], true))
			}
			return
		}
		botMove(t, g, rng)
	}
	t.Fatalf("seed %d: game did not finish (turn %d, points %v)", seed, g.TurnCount, scores(g))
}

func scores(g *Game) []int {
	var s []int
	for _, p := range g.Players {
		s = append(s, g.Points(p, true))
	}
	return s
}

func checkInvariants(t *testing.T, g *Game) {
	t.Helper()
	if g.Phase == PhaseLobby {
		return
	}
	for _, r := range Resources {
		total := g.Bank[r]
		for _, p := range g.Players {
			if p.Resources[r] < 0 {
				t.Fatalf("%s has negative %s", p.Name, r)
			}
			total += p.Resources[r]
		}
		if total != BankPerType {
			t.Fatalf("resource %s not conserved: %d", r, total)
		}
	}
	for _, p := range g.Players {
		if p.Roads > MaxRoads || p.Setts > MaxSetts || p.Cities > MaxCities {
			t.Fatalf("%s exceeded piece limits", p.Name)
		}
	}
}

func botMove(t *testing.T, g *Game, rng *rand.Rand) {
	t.Helper()
	must := func(err error) {
		if err != nil {
			t.Helper()
			t.Fatalf("phase=%s step=%s: %v", g.Phase, g.SetupStep, err)
		}
	}

	if g.Phase == PhaseSetup {
		p := g.Players[g.SetupPlayer()]
		if g.SetupStep == "settlement" {
			var opts []int
			for _, v := range g.Board.Verts {
				if g.vertexFree(v.ID) {
					opts = append(opts, v.ID)
				}
			}
			must(g.Do(p.Token, Action{Type: "placeSetupSettlement", Vertex: opts[rng.IntN(len(opts))]}))
		} else {
			var opts []int
			for _, e := range g.Board.Verts[g.LastSetupVert].Edges {
				if _, taken := g.RoadsE[e]; !taken {
					opts = append(opts, e)
				}
			}
			must(g.Do(p.Token, Action{Type: "placeSetupRoad", Edge: opts[rng.IntN(len(opts))]}))
		}
		return
	}

	// pending discards (any player)
	for pid, need := range g.DiscardPending {
		p := g.Players[pid]
		amounts := map[string]int{}
		left := need
		for _, r := range Resources {
			n := p.Resources[r]
			if n > left {
				n = left
			}
			amounts[r] = n
			left -= n
		}
		must(g.Do(p.Token, Action{Type: "discard", Amounts: amounts}))
		return
	}

	cur := g.Players[g.Turn]
	if g.RobberPending {
		h := rng.IntN(len(g.Board.Hexes))
		if h == g.Board.Robber {
			h = (h + 1) % len(g.Board.Hexes)
		}
		must(g.Do(cur.Token, Action{Type: "moveRobber", Hex: h}))
		return
	}
	if g.StealPending {
		must(g.Do(cur.Token, Action{Type: "steal", Player: g.StealCandidates[0]}))
		return
	}
	if !g.Rolled {
		must(g.Do(cur.Token, Action{Type: "roll"}))
		return
	}

	// play a dev card occasionally
	if !g.PlayedDevThisTurn && rng.IntN(3) == 0 {
		for _, c := range cur.DevCards {
			if c.BoughtTurn == g.TurnCount || c.Type == "victory" {
				continue
			}
			a := Action{Type: "playDev", Card: c.Type}
			switch c.Type {
			case "yearOfPlenty":
				a.R1, a.R2 = Wheat, Ore
				if g.Bank[Wheat] < 1 || g.Bank[Ore] < 1 {
					continue
				}
			case "monopoly":
				a.Resource = Wheat
			case "roadBuilding":
				if cur.Roads >= MaxRoads {
					continue
				}
			}
			must(g.Do(cur.Token, a))
			return
		}
	}

	// free roads from Road Building
	if g.FreeRoads > 0 {
		for _, e := range g.Board.Edges {
			if g.canPlaceRoad(cur, e.ID) {
				must(g.Do(cur.Token, Action{Type: "buildRoad", Edge: e.ID}))
				return
			}
		}
		must(g.Do(cur.Token, Action{Type: "endTurn"}))
		return
	}

	// build greedily: city > settlement > road > dev
	if cur.has(BuildCosts["city"]) && cur.Cities < MaxCities {
		for v, b := range g.BuildingsV {
			if b.Player == cur.ID && b.Type == "settlement" {
				must(g.Do(cur.Token, Action{Type: "buildCity", Vertex: v}))
				return
			}
		}
	}
	if cur.has(BuildCosts["settlement"]) && cur.Setts < MaxSetts {
		for _, v := range g.Board.Verts {
			if g.canPlaceSettlement(cur, v.ID) {
				must(g.Do(cur.Token, Action{Type: "buildSettlement", Vertex: v.ID}))
				return
			}
		}
	}
	if cur.has(BuildCosts["road"]) && cur.Roads < MaxRoads && rng.IntN(2) == 0 {
		for _, e := range g.Board.Edges {
			if g.canPlaceRoad(cur, e.ID) {
				must(g.Do(cur.Token, Action{Type: "buildRoad", Edge: e.ID}))
				return
			}
		}
	}
	if cur.has(BuildCosts["dev"]) && len(g.DevDeck) > 0 && rng.IntN(2) == 0 {
		must(g.Do(cur.Token, Action{Type: "buyDev"}))
		return
	}

	// 4:1 bank trade to unstick hoarded hands
	for _, give := range Resources {
		rate := g.BankRate(cur, give)
		if cur.Resources[give] >= rate+2 {
			var want string
			for _, w := range []string{Wheat, Ore, Wood, Brick, Sheep} {
				if w != give && cur.Resources[w] == 0 && g.Bank[w] > 0 {
					want = w
					break
				}
			}
			if want != "" {
				must(g.Do(cur.Token, Action{
					Type: "bankTrade",
					Give: map[string]int{give: rate},
					Get:  map[string]int{want: 1},
				}))
				return
			}
		}
	}

	must(g.Do(cur.Token, Action{Type: "endTurn"}))
}
