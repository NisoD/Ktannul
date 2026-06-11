package game

// View is the personalized game state sent to one client.
type View struct {
	Phase   string       `json:"phase"`
	You     *YouView     `json:"you"`
	Players []PlayerView `json:"players"`
	Board   *BoardView   `json:"board,omitempty"`
	Turn    int          `json:"turn"`
	DiceA   int          `json:"diceA"`
	DiceB   int          `json:"diceB"`
	Rolled  bool         `json:"rolled"`
	Winner  int          `json:"winner"`
	Log     []LogEntry   `json:"log"`
	Trade   *TradeOffer  `json:"trade,omitempty"`
	Version int          `json:"version"`

	// Phase prompts
	SetupPlayer   int    `json:"setupPlayer"`
	SetupStep     string `json:"setupStep,omitempty"`
	RobberPending bool   `json:"robberPending"`
	StealPending  bool   `json:"stealPending"`
	StealFrom     []int  `json:"stealFrom,omitempty"`
	MustDiscard   int    `json:"mustDiscard"`
	WaitDiscard   []int  `json:"waitDiscard,omitempty"`
	FreeRoads     int    `json:"freeRoads"`

	// Legal moves for *you* right now
	LegalRoads       []int          `json:"legalRoads,omitempty"`
	LegalSettlements []int          `json:"legalSettlements,omitempty"`
	LegalCities      []int          `json:"legalCities,omitempty"`
	BankRates        map[string]int `json:"bankRates,omitempty"`
	DevDeckLeft      int            `json:"devDeckLeft"`
}

type YouView struct {
	ID        int            `json:"id"`
	Name      string         `json:"name"`
	Color     string         `json:"color"`
	Resources map[string]int `json:"resources"`
	DevCards  []DevCardView  `json:"devCards"`
	Points    int            `json:"points"`
}

type DevCardView struct {
	Type     string `json:"type"`
	Playable bool   `json:"playable"`
}

type PlayerView struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Cards       int    `json:"cards"`
	DevCards    int    `json:"devCards"`
	Knights     int    `json:"knights"`
	Points      int    `json:"points"` // public points only
	LongestRoad bool   `json:"longestRoad"`
	LargestArmy bool   `json:"largestArmy"`
	RoadsLeft   int    `json:"roadsLeft"`
	SettsLeft   int    `json:"settsLeft"`
	CitiesLeft  int    `json:"citiesLeft"`
}

type BoardView struct {
	Hexes     []Hex            `json:"hexes"`
	Verts     []Vertex         `json:"verts"`
	Edges     []Edge           `json:"edges"`
	Ports     []Port           `json:"ports"`
	Robber    int              `json:"robber"`
	Buildings map[int]Building `json:"buildings"`
	Roads     map[int]int      `json:"roads"`
}

// ViewFor builds the state visible to the given token. Caller must NOT
// hold the mutex.
func (g *Game) ViewFor(token string) *View {
	g.Mu.Lock()
	defer g.Mu.Unlock()

	p := g.playerByToken(token)
	v := &View{
		Phase:       g.Phase,
		Turn:        g.Turn,
		DiceA:       g.DiceA,
		DiceB:       g.DiceB,
		Rolled:      g.Rolled,
		Winner:      g.Winner,
		Log:         g.Log,
		Trade:       g.Trade,
		Version:     g.Version,
		SetupPlayer: -1,
		DevDeckLeft: len(g.DevDeck),
	}
	for _, pl := range g.Players {
		v.Players = append(v.Players, PlayerView{
			ID:          pl.ID,
			Name:        pl.Name,
			Color:       pl.Color,
			Cards:       pl.HandSize(),
			DevCards:    len(pl.DevCards),
			Knights:     pl.Knights,
			Points:      g.Points(pl, false),
			LongestRoad: g.LongestRoadPlayer == pl.ID,
			LargestArmy: g.LargestArmyPlayer == pl.ID,
			RoadsLeft:   MaxRoads - pl.Roads,
			SettsLeft:   MaxSetts - pl.Setts,
			CitiesLeft:  MaxCities - pl.Cities,
		})
	}
	if g.Board != nil {
		v.Board = &BoardView{
			Hexes:     g.Board.Hexes,
			Verts:     g.Board.Verts,
			Edges:     g.Board.Edges,
			Ports:     g.Board.Ports,
			Robber:    g.Board.Robber,
			Buildings: g.BuildingsV,
			Roads:     g.RoadsE,
		}
	}
	if g.Phase == PhaseSetup {
		v.SetupPlayer = g.SetupPlayer()
		v.SetupStep = g.SetupStep
	}
	v.RobberPending = g.RobberPending && len(g.DiscardPending) == 0
	v.StealPending = g.StealPending
	v.StealFrom = g.StealCandidates
	for id := range g.DiscardPending {
		v.WaitDiscard = append(v.WaitDiscard, id)
	}
	v.FreeRoads = g.FreeRoads

	if p == nil {
		return v
	}

	v.You = &YouView{
		ID:        p.ID,
		Name:      p.Name,
		Color:     p.Color,
		Resources: p.Resources,
		Points:    g.Points(p, true),
	}
	canPlay := g.Phase == PhaseMain && g.Turn == p.ID && !g.PlayedDevThisTurn &&
		len(g.DiscardPending) == 0 && !g.RobberPending && !g.StealPending
	for _, c := range p.DevCards {
		playable := canPlay && c.Type != "victory" && c.BoughtTurn != g.TurnCount
		if c.Type != "knight" && !g.Rolled {
			playable = false
		}
		v.You.DevCards = append(v.You.DevCards, DevCardView{Type: c.Type, Playable: playable})
	}
	if n, ok := g.DiscardPending[p.ID]; ok {
		v.MustDiscard = n
	}

	// Legal moves
	if g.Phase == PhaseSetup && g.SetupPlayer() == p.ID {
		if g.SetupStep == "settlement" {
			for _, vert := range g.Board.Verts {
				if g.vertexFree(vert.ID) {
					v.LegalSettlements = append(v.LegalSettlements, vert.ID)
				}
			}
		} else {
			for _, e := range g.Board.Verts[g.LastSetupVert].Edges {
				if _, taken := g.RoadsE[e]; !taken {
					v.LegalRoads = append(v.LegalRoads, e)
				}
			}
		}
	}
	if g.Phase == PhaseMain && g.Turn == p.ID && g.Rolled &&
		len(g.DiscardPending) == 0 && !g.RobberPending && !g.StealPending {
		for _, e := range g.Board.Edges {
			if g.canPlaceRoad(p, e.ID) {
				v.LegalRoads = append(v.LegalRoads, e.ID)
			}
		}
		for _, vert := range g.Board.Verts {
			if g.canPlaceSettlement(p, vert.ID) {
				v.LegalSettlements = append(v.LegalSettlements, vert.ID)
			}
		}
		for vid, b := range g.BuildingsV {
			if b.Player == p.ID && b.Type == "settlement" {
				v.LegalCities = append(v.LegalCities, vid)
			}
		}
		v.BankRates = map[string]int{}
		for _, r := range Resources {
			v.BankRates[r] = g.BankRate(p, r)
		}
	}
	return v
}
