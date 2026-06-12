package game

import "errors"

var botNames = []string{"Bot Erik", "Bot Astrid", "Bot Sven", "Bot Freya"}

func (g *Game) addBot(p *Player) error {
	if g.Phase != PhaseLobby {
		return errors.New("bots can only join in the lobby")
	}
	if p.ID != 0 {
		return errors.New("only the host can add bots")
	}
	if len(g.Players) >= 4 {
		return errors.New("game is full (4 players)")
	}
	b := &Player{
		ID:        len(g.Players),
		Name:      botNames[len(g.Players)-1],
		Token:     newToken(),
		Color:     playerColors[len(g.Players)],
		Resources: map[string]int{},
		IsBot:     true,
	}
	g.Players = append(g.Players, b)
	g.logf("%s joined", b.Name)
	return nil
}

func (g *Game) removeBot(p *Player) error {
	if g.Phase != PhaseLobby {
		return errors.New("not in lobby")
	}
	if p.ID != 0 {
		return errors.New("only the host can remove bots")
	}
	idx := -1
	for i := len(g.Players) - 1; i >= 0; i-- {
		if g.Players[i].IsBot {
			idx = i
			break
		}
	}
	if idx == -1 {
		return errors.New("no bot to remove")
	}
	g.logf("%s left", g.Players[idx].Name)
	g.Players = append(g.Players[:idx], g.Players[idx+1:]...)
	// Player IDs double as slice indexes — reindex (lobby only, so no
	// board state references them yet).
	for i, pl := range g.Players {
		pl.ID = i
		pl.Color = playerColors[i]
	}
	return nil
}

// BotStep performs at most one pending bot action (discard, trade response,
// setup placement, or a move on the bot's own turn). The server calls this
// on a timer so bot play is paced for humans to follow. Returns true if a
// bot acted.
func (g *Game) BotStep() bool {
	g.Mu.Lock()
	defer g.Mu.Unlock()
	if g.botStepLocked() {
		g.notify()
		return true
	}
	return false
}

func (g *Game) botStepLocked() bool {
	if g.Phase != PhaseSetup && g.Phase != PhaseMain {
		return false
	}

	// Discards owed by any bot come first — they block everyone.
	for pid, need := range g.DiscardPending {
		p := g.Players[pid]
		if !p.IsBot {
			continue
		}
		return g.discard(p, greedyDiscard(p, need)) == nil
	}

	// Respond to a pending trade offer from someone else.
	if g.Trade != nil && g.Phase == PhaseMain {
		for _, p := range g.Players {
			if !p.IsBot || p.ID == g.Trade.From || tradeResponded(g.Trade, p.ID) {
				continue
			}
			return g.respondTrade(p, g.botWantsTrade(p, g.Trade.Give, g.Trade.Get)) == nil
		}
		// A bot's own offer: confirm the first accepter, or withdraw once
		// everyone has answered.
		if from := g.Players[g.Trade.From]; from.IsBot {
			if len(g.Trade.Accepted) > 0 {
				return g.confirmTrade(from, g.Trade.Accepted[0]) == nil
			}
			if len(g.Trade.Accepted)+len(g.Trade.Rejected) >= len(g.Players)-1 {
				return g.cancelTrade(from) == nil
			}
			return false // humans still deciding
		}
	}

	if g.Phase == PhaseSetup {
		p := g.Players[g.SetupPlayer()]
		if !p.IsBot {
			return false
		}
		if g.SetupStep == "settlement" {
			return g.placeSetupSettlement(p, g.bestFreeVertex()) == nil
		}
		for _, e := range g.Board.Verts[g.LastSetupVert].Edges {
			if _, taken := g.RoadsE[e]; !taken {
				return g.placeSetupRoad(p, e) == nil
			}
		}
		return false
	}

	p := g.Players[g.Turn]
	if !p.IsBot {
		return false
	}
	if g.RobberPending {
		return g.moveRobber(p, g.bestRobberHex(p)) == nil
	}
	if g.StealPending {
		victim := g.StealCandidates[0]
		for _, c := range g.StealCandidates {
			if g.Players[c].HandSize() > g.Players[victim].HandSize() {
				victim = c
			}
		}
		return g.steal(p, victim) == nil
	}
	if !g.Rolled {
		// Unblock own production with a knight before rolling.
		if !g.PlayedDevThisTurn && g.robberOnOwnHex(p) && g.hasPlayable(p, "knight") {
			if g.playDev(p, Action{Type: "playDev", Card: "knight"}) == nil {
				return true
			}
		}
		return g.roll(p) == nil
	}

	if g.FreeRoads > 0 {
		for _, e := range g.Board.Edges {
			if g.canPlaceRoad(p, e.ID) {
				return g.buildRoad(p, e.ID) == nil
			}
		}
		return g.endTurn(p) == nil
	}

	// Build priority: city > settlement > expand with a road > dev card.
	if p.has(BuildCosts["city"]) && p.Cities < MaxCities {
		for v, b := range g.BuildingsV {
			if b.Player == p.ID && b.Type == "settlement" {
				return g.buildCity(p, v) == nil
			}
		}
	}
	if p.has(BuildCosts["settlement"]) && p.Setts < MaxSetts {
		if v := g.bestSettlementVertex(p); v >= 0 {
			return g.buildSettlement(p, v) == nil
		}
	}
	if p.has(BuildCosts["road"]) && p.Roads < MaxRoads && g.bestSettlementVertex(p) < 0 {
		for _, e := range g.Board.Edges {
			if g.canPlaceRoad(p, e.ID) {
				return g.buildRoad(p, e.ID) == nil
			}
		}
	}
	if !g.PlayedDevThisTurn {
		if g.hasPlayable(p, "roadBuilding") && p.Roads < MaxRoads {
			if g.playDev(p, Action{Type: "playDev", Card: "roadBuilding"}) == nil {
				return true
			}
		}
		if g.hasPlayable(p, "yearOfPlenty") && g.Bank[Wheat] > 0 && g.Bank[Ore] > 0 {
			if g.playDev(p, Action{Type: "playDev", Card: "yearOfPlenty", R1: Wheat, R2: Ore}) == nil {
				return true
			}
		}
		if g.hasPlayable(p, "monopoly") && p.Knights >= 2 {
			if g.playDev(p, Action{Type: "playDev", Card: "monopoly", Resource: Wheat}) == nil {
				return true
			}
		}
	}
	if p.has(BuildCosts["dev"]) && len(g.DevDeck) > 0 {
		return g.buyDev(p) == nil
	}

	// Stuck but holding a surplus? Offer the table a trade before falling
	// back to the bank's awful rates. One offer per turn.
	if p.LastOfferTurn != g.TurnCount && g.Trade == nil {
		if give, get := g.botTradeIdea(p); give != "" {
			p.LastOfferTurn = g.TurnCount
			if g.offerTrade(p, map[string]int{give: 1}, map[string]int{get: 1}) == nil {
				return true
			}
		}
	}

	// Maritime trade out of a hoarded pile toward whatever is missing.
	for _, give := range Resources {
		rate := g.BankRate(p, give)
		if p.Resources[give] < rate+1 {
			continue
		}
		for _, want := range []string{Ore, Wheat, Wood, Brick, Sheep} {
			if want != give && p.Resources[want] == 0 && g.Bank[want] > 0 {
				if g.bankTrade(p, map[string]int{give: rate}, map[string]int{want: 1}) == nil {
					return true
				}
			}
		}
	}

	return g.endTurn(p) == nil
}

// ---- heuristics ----

func hexPip(h Hex) int {
	if h.Number == 0 {
		return 0
	}
	return 6 - abs(7-h.Number)
}

func (g *Game) vertexPips(v int) int {
	s := 0
	for _, hi := range g.Board.Verts[v].Hexes {
		s += hexPip(g.Board.Hexes[hi])
	}
	return s
}

func (g *Game) bestFreeVertex() int {
	best, bestScore := -1, -1
	for _, v := range g.Board.Verts {
		if !g.vertexFree(v.ID) {
			continue
		}
		if s := g.vertexPips(v.ID); s > bestScore {
			best, bestScore = v.ID, s
		}
	}
	return best
}

func (g *Game) bestSettlementVertex(p *Player) int {
	best, bestScore := -1, -1
	for _, v := range g.Board.Verts {
		if !g.canPlaceSettlement(p, v.ID) {
			continue
		}
		if s := g.vertexPips(v.ID); s > bestScore {
			best, bestScore = v.ID, s
		}
	}
	return best
}

// bestRobberHex targets the most productive hex with opponent buildings
// and none of the bot's own.
func (g *Game) bestRobberHex(p *Player) int {
	best, bestScore := -1, -1
	for _, h := range g.Board.Hexes {
		if h.ID == g.Board.Robber {
			continue
		}
		opponents, mine := 0, false
		for _, v := range h.Verts {
			if b, ok := g.BuildingsV[v]; ok {
				if b.Player == p.ID {
					mine = true
				} else {
					opponents++
				}
			}
		}
		if mine {
			continue
		}
		if s := hexPip(h) * opponents; s > bestScore {
			best, bestScore = h.ID, s
		}
	}
	if best == -1 { // everything touches us; just avoid the current spot
		for _, h := range g.Board.Hexes {
			if h.ID != g.Board.Robber {
				return h.ID
			}
		}
	}
	return best
}

func (g *Game) robberOnOwnHex(p *Player) bool {
	for _, v := range g.Board.Hexes[g.Board.Robber].Verts {
		if b, ok := g.BuildingsV[v]; ok && b.Player == p.ID {
			return true
		}
	}
	return false
}

func (g *Game) hasPlayable(p *Player, card string) bool {
	for _, c := range p.DevCards {
		if c.Type == card && c.BoughtTurn != g.TurnCount {
			return true
		}
	}
	return false
}

// botWantsTrade scores a deal by scarcity: a resource is worth more the
// fewer you hold. give = what the bot would receive, get = what it pays.
func (g *Game) botWantsTrade(p *Player, give, get map[string]int) bool {
	if !p.has(get) {
		return false
	}
	worth := func(have int) int {
		if have > 3 {
			have = 3
		}
		return 4 - have
	}
	recv, pay := 0, 0
	for r, n := range give {
		recv += n * worth(p.Resources[r])
	}
	for r, n := range get {
		pay += n * worth(p.Resources[r]-n)
	}
	return recv > pay
}

// botTradeIdea proposes 1 surplus card for 1 missing card when the bot
// holds a pile of something and lacks a build ingredient.
func (g *Game) botTradeIdea(p *Player) (give, get string) {
	needed := map[string]bool{}
	for _, cost := range []map[string]int{BuildCosts["settlement"], BuildCosts["city"], BuildCosts["road"]} {
		for r, n := range cost {
			if p.Resources[r] < n {
				needed[r] = true
			}
		}
	}
	bestGive, bestN := "", 2 // require at least 3 of the surplus resource
	for _, r := range Resources {
		if !needed[r] && p.Resources[r] > bestN {
			bestGive, bestN = r, p.Resources[r]
		}
	}
	if bestGive == "" {
		return "", ""
	}
	for _, r := range Resources {
		if needed[r] && p.Resources[r] == 0 {
			return bestGive, r
		}
	}
	return "", ""
}

func tradeResponded(t *TradeOffer, pid int) bool {
	for _, id := range t.Accepted {
		if id == pid {
			return true
		}
	}
	for _, id := range t.Rejected {
		if id == pid {
			return true
		}
	}
	return false
}

// greedyDiscard dumps from the largest piles first.
func greedyDiscard(p *Player, need int) map[string]int {
	amounts := map[string]int{}
	left := need
	for left > 0 {
		biggest, n := "", 0
		for _, r := range Resources {
			if rem := p.Resources[r] - amounts[r]; rem > n {
				biggest, n = r, rem
			}
		}
		if biggest == "" {
			break
		}
		amounts[biggest]++
		left--
	}
	return amounts
}
