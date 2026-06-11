package game

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
)

const (
	PhaseLobby  = "lobby"
	PhaseSetup  = "setup"
	PhaseMain   = "main"
	PhaseEnded  = "ended"
	WinPoints   = 10
	MaxRoads    = 15
	MaxSetts    = 5
	MaxCities   = 4
	HandLimit   = 7
	BankPerType = 19
)

var BuildCosts = map[string]map[string]int{
	"road":       {Wood: 1, Brick: 1},
	"settlement": {Wood: 1, Brick: 1, Sheep: 1, Wheat: 1},
	"city":       {Wheat: 2, Ore: 3},
	"dev":        {Sheep: 1, Wheat: 1, Ore: 1},
}

type DevCard struct {
	Type       string `json:"type"` // knight, victory, roadBuilding, yearOfPlenty, monopoly
	BoughtTurn int    `json:"-"`
}

type Player struct {
	ID        int
	Name      string
	Token     string
	Color     string
	Resources map[string]int
	DevCards  []DevCard
	Knights   int
	Roads     int // pieces placed
	Setts     int
	Cities    int
	IsBot     bool
}

func (p *Player) HandSize() int {
	n := 0
	for _, c := range p.Resources {
		n += c
	}
	return n
}

type Building struct {
	Player int    `json:"player"`
	Type   string `json:"type"` // settlement, city
}

type TradeOffer struct {
	From     int            `json:"from"`
	Give     map[string]int `json:"give"`
	Get      map[string]int `json:"get"`
	Accepted []int          `json:"accepted"`
	Rejected []int          `json:"rejected"`
}

type LogEntry struct {
	Text string `json:"text"`
}

// Gain records one hex paying out on the most recent roll (for client
// fly-to-player animations).
type Gain struct {
	Player   int    `json:"player"`
	Resource string `json:"resource"`
	N        int    `json:"n"`
	Hex      int    `json:"hex"`
}

type Game struct {
	Mu  sync.Mutex
	Rng *rand.Rand

	Board   *Board
	Players []*Player
	Phase   string

	// setup
	SetupIdx      int // 0 .. 2*len(players)-1
	SetupStep     string
	LastSetupVert int

	// main turn state
	Turn              int
	TurnCount         int
	DiceA, DiceB      int
	Rolled            bool
	RobberPending     bool
	StealPending      bool
	StealCandidates   []int
	DiscardPending    map[int]int
	PlayedDevThisTurn bool
	FreeRoads         int

	BuildingsV map[int]Building
	RoadsE     map[int]int
	DevDeck    []string
	Bank       map[string]int

	LongestRoadPlayer int
	LongestRoadLen    int
	LargestArmyPlayer int

	Trade      *TradeOffer
	Winner     int
	Log        []LogEntry
	LastGains  []Gain  // production from the most recent roll
	RollCounts [13]int // dice histogram for end-of-game stats
	JoinURL    string  // LAN address shown in the lobby (set at startup)

	Version int
	Changed chan struct{} // signaled (non-blocking) on every state change
}

var playerColors = []string{"#e63946", "#2a6fdb", "#f3a712", "#2a9d44"}

func New() *Game {
	return &Game{
		Rng:               rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())),
		Phase:             PhaseLobby,
		LongestRoadPlayer: -1,
		LargestArmyPlayer: -1,
		Winner:            -1,
		Changed:           make(chan struct{}, 1),
	}
}

func (g *Game) notify() {
	g.Version++
	select {
	case g.Changed <- struct{}{}:
	default:
	}
}

func (g *Game) logf(format string, a ...any) {
	g.Log = append(g.Log, LogEntry{Text: fmt.Sprintf(format, a...)})
	if len(g.Log) > 100 {
		g.Log = g.Log[len(g.Log)-100:]
	}
}

func (g *Game) playerByToken(tok string) *Player {
	for _, p := range g.Players {
		if p.Token == tok {
			return p
		}
	}
	return nil
}

// Join adds a player in the lobby, or resumes an existing player by token.
// With resume=true it never creates a new player.
func (g *Game) Join(name, token string, resume bool) (*Player, error) {
	g.Mu.Lock()
	defer g.Mu.Unlock()
	if p := g.playerByToken(token); p != nil && token != "" {
		return p, nil
	}
	if resume {
		return nil, errors.New("unknown token")
	}
	if g.Phase != PhaseLobby {
		return nil, errors.New("game already started")
	}
	if len(g.Players) >= 4 {
		return nil, errors.New("game is full (4 players)")
	}
	if name == "" {
		return nil, errors.New("name required")
	}
	if r := []rune(name); len(r) > 16 { // rune-safe: never split multibyte chars
		name = string(r[:16])
	}
	p := &Player{
		ID:        len(g.Players),
		Name:      name,
		Token:     newToken(g.Rng),
		Color:     playerColors[len(g.Players)],
		Resources: map[string]int{},
	}
	g.Players = append(g.Players, p)
	g.logf("%s joined", name)
	g.notify()
	return p, nil
}

// ClaimSeat hands out the credentials for an existing human seat — the
// recovery path when a device lost its token (new browser, cleared
// storage, joined via a different hostname). Trusted-LAN tradeoff.
func (g *Game) ClaimSeat(id int) (*Player, error) {
	g.Mu.Lock()
	defer g.Mu.Unlock()
	if g.Phase == PhaseLobby {
		return nil, errors.New("game hasn't started — just join")
	}
	if id < 0 || id >= len(g.Players) {
		return nil, errors.New("no such seat")
	}
	p := g.Players[id]
	if p.IsBot {
		return nil, errors.New("that seat is a bot")
	}
	// Rotate the token so the previous device loses control — prevents a
	// claimed seat from being driven from two places at once.
	p.Token = newToken(g.Rng)
	g.logf("%s's seat was resumed on a new device", p.Name)
	g.notify()
	return p, nil
}

func newToken(rng *rand.Rand) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 24)
	for i := range b {
		b[i] = chars[rng.IntN(len(chars))]
	}
	return string(b)
}

// ---- Action dispatch ----

type Action struct {
	Type     string         `json:"type"`
	Vertex   int            `json:"vertex"`
	Edge     int            `json:"edge"`
	Hex      int            `json:"hex"`
	Player   int            `json:"player"`
	Card     string         `json:"card"`
	Resource string         `json:"resource"`
	R1       string         `json:"r1"`
	R2       string         `json:"r2"`
	Give     map[string]int `json:"give"`
	Get      map[string]int `json:"get"`
	Amounts  map[string]int `json:"amounts"`
}

func (g *Game) Do(token string, a Action) error {
	g.Mu.Lock()
	defer g.Mu.Unlock()
	p := g.playerByToken(token)
	if p == nil {
		return errors.New("unknown player")
	}
	err := g.dispatch(p, a)
	if err == nil {
		g.notify()
	}
	return err
}

func (g *Game) dispatch(p *Player, a Action) error {
	switch a.Type {
	case "start":
		return g.start(p)
	case "newGame":
		return g.newGame(p)
	case "addBot":
		return g.addBot(p)
	case "removeBot":
		return g.removeBot(p)
	case "placeSetupSettlement":
		return g.placeSetupSettlement(p, a.Vertex)
	case "placeSetupRoad":
		return g.placeSetupRoad(p, a.Edge)
	case "roll":
		return g.roll(p)
	case "discard":
		return g.discard(p, a.Amounts)
	case "moveRobber":
		return g.moveRobber(p, a.Hex)
	case "steal":
		return g.steal(p, a.Player)
	case "buildRoad":
		return g.buildRoad(p, a.Edge)
	case "buildSettlement":
		return g.buildSettlement(p, a.Vertex)
	case "buildCity":
		return g.buildCity(p, a.Vertex)
	case "buyDev":
		return g.buyDev(p)
	case "playDev":
		return g.playDev(p, a)
	case "bankTrade":
		return g.bankTrade(p, a.Give, a.Get)
	case "offerTrade":
		return g.offerTrade(p, a.Give, a.Get)
	case "respondTrade":
		return g.respondTrade(p, a.Resource == "accept")
	case "confirmTrade":
		return g.confirmTrade(p, a.Player)
	case "cancelTrade":
		return g.cancelTrade(p)
	case "endTurn":
		return g.endTurn(p)
	}
	return fmt.Errorf("unknown action %q", a.Type)
}

// ---- Lobby ----

func (g *Game) start(p *Player) error {
	if g.Phase != PhaseLobby {
		return errors.New("not in lobby")
	}
	if p.ID != 0 {
		return errors.New("only the host can start")
	}
	if len(g.Players) < 2 {
		return errors.New("need at least 2 players")
	}
	g.beginGame()
	return nil
}

func (g *Game) newGame(p *Player) error {
	if g.Phase != PhaseEnded {
		return errors.New("game not over")
	}
	if p.ID != 0 {
		return errors.New("only the host can start a rematch")
	}
	g.beginGame()
	g.logf("--- new game ---")
	return nil
}

func (g *Game) beginGame() {
	g.Board = GenerateBoard(g.Rng)
	g.BuildingsV = map[int]Building{}
	g.RoadsE = map[int]int{}
	g.Bank = map[string]int{}
	for _, r := range Resources {
		g.Bank[r] = BankPerType
	}
	g.DevDeck = nil
	add := func(t string, n int) {
		for i := 0; i < n; i++ {
			g.DevDeck = append(g.DevDeck, t)
		}
	}
	add("knight", 14)
	add("victory", 5)
	add("roadBuilding", 2)
	add("yearOfPlenty", 2)
	add("monopoly", 2)
	g.Rng.Shuffle(len(g.DevDeck), func(i, j int) { g.DevDeck[i], g.DevDeck[j] = g.DevDeck[j], g.DevDeck[i] })

	for _, pl := range g.Players {
		pl.Resources = map[string]int{}
		pl.DevCards = nil
		pl.Knights = 0
		pl.Roads, pl.Setts, pl.Cities = 0, 0, 0
	}
	g.Phase = PhaseSetup
	g.SetupIdx = 0
	g.SetupStep = "settlement"
	g.LastSetupVert = -1
	g.Turn = 0
	g.TurnCount = 0
	g.DiceA, g.DiceB = 0, 0
	g.Rolled = false
	g.RobberPending = false
	g.StealPending = false
	g.DiscardPending = nil
	g.PlayedDevThisTurn = false
	g.FreeRoads = 0
	g.LongestRoadPlayer = -1
	g.LongestRoadLen = 0
	g.LargestArmyPlayer = -1
	g.Trade = nil
	g.Winner = -1
	g.RollCounts = [13]int{}
	g.logf("game started — place your first settlements")
}

// SetupPlayer returns the player index whose turn it is during setup
// (snake order: 0..n-1 then n-1..0).
func (g *Game) SetupPlayer() int {
	n := len(g.Players)
	if g.SetupIdx < n {
		return g.SetupIdx
	}
	return 2*n - 1 - g.SetupIdx
}

// ---- Setup phase ----

func (g *Game) placeSetupSettlement(p *Player, v int) error {
	if g.Phase != PhaseSetup || g.SetupStep != "settlement" || g.SetupPlayer() != p.ID {
		return errors.New("not your setup settlement turn")
	}
	if v < 0 || v >= len(g.Board.Verts) {
		return errors.New("bad vertex")
	}
	if !g.vertexFree(v) {
		return errors.New("too close to another settlement")
	}
	g.BuildingsV[v] = Building{Player: p.ID, Type: "settlement"}
	p.Setts++
	g.LastSetupVert = v
	g.SetupStep = "road"
	// Second-round settlement: collect starting resources.
	if g.SetupIdx >= len(g.Players) {
		for _, hi := range g.Board.Verts[v].Hexes {
			h := g.Board.Hexes[hi]
			if h.Terrain != Desert && g.Bank[h.Terrain] > 0 {
				g.Bank[h.Terrain]--
				p.Resources[h.Terrain]++
			}
		}
	}
	g.logf("%s placed a settlement", p.Name)
	return nil
}

func (g *Game) placeSetupRoad(p *Player, e int) error {
	if g.Phase != PhaseSetup || g.SetupStep != "road" || g.SetupPlayer() != p.ID {
		return errors.New("not your setup road turn")
	}
	if e < 0 || e >= len(g.Board.Edges) {
		return errors.New("bad edge")
	}
	if _, taken := g.RoadsE[e]; taken {
		return errors.New("edge occupied")
	}
	ed := g.Board.Edges[e]
	if ed.V1 != g.LastSetupVert && ed.V2 != g.LastSetupVert {
		return errors.New("road must touch the settlement you just placed")
	}
	g.RoadsE[e] = p.ID
	p.Roads++
	g.logf("%s placed a road", p.Name)
	g.SetupIdx++
	g.SetupStep = "settlement"
	g.LastSetupVert = -1
	if g.SetupIdx >= 2*len(g.Players) {
		g.Phase = PhaseMain
		g.Turn = 0
		g.TurnCount = 1
		g.logf("setup complete — %s rolls first", g.Players[0].Name)
	}
	return nil
}

// vertexFree: vertex empty and distance rule satisfied.
func (g *Game) vertexFree(v int) bool {
	if _, ok := g.BuildingsV[v]; ok {
		return false
	}
	for _, a := range g.Board.Verts[v].Adj {
		if _, ok := g.BuildingsV[a]; ok {
			return false
		}
	}
	return true
}

// ---- Turn helpers ----

func (g *Game) requireTurn(p *Player) error {
	if g.Phase != PhaseMain {
		return errors.New("game not in play")
	}
	if g.Turn != p.ID {
		return errors.New("not your turn")
	}
	return nil
}

func (g *Game) requireActive(p *Player) error {
	if err := g.requireTurn(p); err != nil {
		return err
	}
	if !g.Rolled {
		return errors.New("roll the dice first")
	}
	if len(g.DiscardPending) > 0 || g.RobberPending || g.StealPending {
		return errors.New("resolve the robber first")
	}
	return nil
}

func (p *Player) has(cost map[string]int) bool {
	for r, n := range cost {
		if p.Resources[r] < n {
			return false
		}
	}
	return true
}

func (g *Game) pay(p *Player, cost map[string]int) {
	for r, n := range cost {
		p.Resources[r] -= n
		g.Bank[r] += n
	}
}

// ---- Dice / robber ----

func (g *Game) roll(p *Player) error {
	if err := g.requireTurn(p); err != nil {
		return err
	}
	if g.Rolled {
		return errors.New("already rolled")
	}
	if g.RobberPending || g.StealPending {
		return errors.New("resolve the robber first")
	}
	g.DiceA = g.Rng.IntN(6) + 1
	g.DiceB = g.Rng.IntN(6) + 1
	g.Rolled = true
	g.LastGains = nil
	total := g.DiceA + g.DiceB
	g.RollCounts[total]++
	g.logf("%s rolled %d", p.Name, total)
	if total == 7 {
		g.DiscardPending = map[int]int{}
		for _, pl := range g.Players {
			if hs := pl.HandSize(); hs > HandLimit {
				g.DiscardPending[pl.ID] = hs / 2
			}
		}
		g.RobberPending = true
		if len(g.DiscardPending) == 0 {
			g.DiscardPending = nil
		}
		return nil
	}
	g.distribute(total)
	g.checkWin()
	return nil
}

// distribute hands out resources for a roll, honoring bank shortages:
// if a resource runs short and more than one player would receive it,
// nobody receives that resource. Per-hex gains are recorded in LastGains
// so the client can animate cards flying to players.
func (g *Game) distribute(total int) {
	type entry struct{ player, n, hex int }
	demand := map[string][]entry{}
	for _, h := range g.Board.Hexes {
		if h.Number != total || h.ID == g.Board.Robber || h.Terrain == Desert {
			continue
		}
		for _, vi := range h.Verts {
			b, ok := g.BuildingsV[vi]
			if !ok {
				continue
			}
			n := 1
			if b.Type == "city" {
				n = 2
			}
			demand[h.Terrain] = append(demand[h.Terrain], entry{b.Player, n, h.ID})
		}
	}
	for res, entries := range demand {
		tot := 0
		players := map[int]bool{}
		for _, e := range entries {
			tot += e.n
			players[e.player] = true
		}
		if tot > g.Bank[res] && len(players) > 1 {
			g.logf("bank is out of %s — no one collects it", res)
			continue
		}
		totals := map[int]int{}
		for _, e := range entries {
			n := e.n
			if n > g.Bank[res] {
				n = g.Bank[res]
			}
			if n == 0 {
				continue
			}
			g.Bank[res] -= n
			g.Players[e.player].Resources[res] += n
			g.LastGains = append(g.LastGains, Gain{Player: e.player, Resource: res, N: n, Hex: e.hex})
			totals[e.player] += n
		}
		for pid, n := range totals {
			g.logf("%s collects %d %s", g.Players[pid].Name, n, res)
		}
	}
}

func (g *Game) discard(p *Player, amounts map[string]int) error {
	need, ok := g.DiscardPending[p.ID]
	if !ok {
		return errors.New("no discard required")
	}
	total := 0
	for r, n := range amounts {
		if n < 0 || !validResource(r) {
			return errors.New("bad discard")
		}
		if p.Resources[r] < n {
			return errors.New("you don't have those cards")
		}
		total += n
	}
	if total != need {
		return fmt.Errorf("must discard exactly %d cards", need)
	}
	for r, n := range amounts {
		p.Resources[r] -= n
		g.Bank[r] += n
	}
	delete(g.DiscardPending, p.ID)
	g.logf("%s discarded %d cards", p.Name, need)
	if len(g.DiscardPending) == 0 {
		g.DiscardPending = nil
	}
	return nil
}

func (g *Game) moveRobber(p *Player, hex int) error {
	if g.Phase != PhaseMain || g.Turn != p.ID || !g.RobberPending {
		return errors.New("you can't move the robber now")
	}
	if len(g.DiscardPending) > 0 {
		return errors.New("waiting for players to discard")
	}
	if hex < 0 || hex >= len(g.Board.Hexes) || hex == g.Board.Robber {
		return errors.New("robber must move to a different hex")
	}
	g.Board.Robber = hex
	g.RobberPending = false
	g.logf("%s moved the robber", p.Name)

	seen := map[int]bool{}
	g.StealCandidates = nil
	for _, vi := range g.Board.Hexes[hex].Verts {
		if b, ok := g.BuildingsV[vi]; ok && b.Player != p.ID && !seen[b.Player] {
			if g.Players[b.Player].HandSize() > 0 {
				seen[b.Player] = true
				g.StealCandidates = append(g.StealCandidates, b.Player)
			}
		}
	}
	if len(g.StealCandidates) > 0 {
		g.StealPending = true
	}
	return nil
}

func (g *Game) steal(p *Player, victim int) error {
	if g.Phase != PhaseMain || g.Turn != p.ID || !g.StealPending {
		return errors.New("nothing to steal")
	}
	valid := false
	for _, c := range g.StealCandidates {
		if c == victim {
			valid = true
		}
	}
	if !valid {
		return errors.New("invalid steal target")
	}
	v := g.Players[victim]
	var pool []string
	for r, n := range v.Resources {
		for i := 0; i < n; i++ {
			pool = append(pool, r)
		}
	}
	r := pool[g.Rng.IntN(len(pool))]
	v.Resources[r]--
	p.Resources[r]++
	g.StealPending = false
	g.StealCandidates = nil
	g.logf("%s stole a card from %s", p.Name, v.Name)
	g.checkWin()
	return nil
}

// ---- Building ----

func (g *Game) canPlaceRoad(p *Player, e int) bool {
	if e < 0 || e >= len(g.Board.Edges) {
		return false
	}
	if _, taken := g.RoadsE[e]; taken {
		return false
	}
	ed := g.Board.Edges[e]
	for _, v := range []int{ed.V1, ed.V2} {
		if b, ok := g.BuildingsV[v]; ok {
			if b.Player == p.ID {
				return true
			}
			continue // opponent building blocks connection through this vertex
		}
		for _, e2 := range g.Board.Verts[v].Edges {
			if owner, ok := g.RoadsE[e2]; ok && e2 != e && owner == p.ID {
				return true
			}
		}
	}
	return false
}

func (g *Game) buildRoad(p *Player, e int) error {
	if err := g.requireActive(p); err != nil {
		return err
	}
	free := g.FreeRoads > 0
	if !free && !p.has(BuildCosts["road"]) {
		return errors.New("need 1 wood + 1 brick")
	}
	if p.Roads >= MaxRoads {
		return errors.New("no road pieces left")
	}
	if !g.canPlaceRoad(p, e) {
		return errors.New("road must connect to your network")
	}
	if free {
		g.FreeRoads--
	} else {
		g.pay(p, BuildCosts["road"])
	}
	g.RoadsE[e] = p.ID
	p.Roads++
	g.logf("%s built a road", p.Name)
	g.updateLongestRoad()
	g.checkWin()
	return nil
}

func (g *Game) canPlaceSettlement(p *Player, v int) bool {
	if v < 0 || v >= len(g.Board.Verts) || !g.vertexFree(v) {
		return false
	}
	for _, e := range g.Board.Verts[v].Edges {
		if owner, ok := g.RoadsE[e]; ok && owner == p.ID {
			return true
		}
	}
	return false
}

func (g *Game) buildSettlement(p *Player, v int) error {
	if err := g.requireActive(p); err != nil {
		return err
	}
	if !p.has(BuildCosts["settlement"]) {
		return errors.New("need wood + brick + sheep + wheat")
	}
	if p.Setts >= MaxSetts {
		return errors.New("no settlement pieces left")
	}
	if !g.canPlaceSettlement(p, v) {
		return errors.New("invalid settlement spot")
	}
	g.pay(p, BuildCosts["settlement"])
	g.BuildingsV[v] = Building{Player: p.ID, Type: "settlement"}
	p.Setts++
	g.logf("%s built a settlement", p.Name)
	g.updateLongestRoad() // a new settlement can break an opponent's road
	g.checkWin()
	return nil
}

func (g *Game) buildCity(p *Player, v int) error {
	if err := g.requireActive(p); err != nil {
		return err
	}
	if !p.has(BuildCosts["city"]) {
		return errors.New("need 2 wheat + 3 ore")
	}
	if p.Cities >= MaxCities {
		return errors.New("no city pieces left")
	}
	b, ok := g.BuildingsV[v]
	if !ok || b.Player != p.ID || b.Type != "settlement" {
		return errors.New("you need a settlement there first")
	}
	g.pay(p, BuildCosts["city"])
	g.BuildingsV[v] = Building{Player: p.ID, Type: "city"}
	p.Setts--
	p.Cities++
	g.logf("%s upgraded to a city", p.Name)
	g.checkWin()
	return nil
}

// ---- Development cards ----

func (g *Game) buyDev(p *Player) error {
	if err := g.requireActive(p); err != nil {
		return err
	}
	if len(g.DevDeck) == 0 {
		return errors.New("development deck is empty")
	}
	if !p.has(BuildCosts["dev"]) {
		return errors.New("need sheep + wheat + ore")
	}
	g.pay(p, BuildCosts["dev"])
	card := g.DevDeck[0]
	g.DevDeck = g.DevDeck[1:]
	p.DevCards = append(p.DevCards, DevCard{Type: card, BoughtTurn: g.TurnCount})
	g.logf("%s bought a development card", p.Name)
	g.checkWin() // victory cards count immediately
	return nil
}

func (g *Game) playDev(p *Player, a Action) error {
	if err := g.requireTurn(p); err != nil {
		return err
	}
	if g.PlayedDevThisTurn {
		return errors.New("only one development card per turn")
	}
	if len(g.DiscardPending) > 0 || g.RobberPending || g.StealPending {
		return errors.New("resolve the robber first")
	}
	idx := -1
	for i, c := range p.DevCards {
		if c.Type == a.Card && c.BoughtTurn != g.TurnCount {
			idx = i
			break
		}
	}
	if idx == -1 {
		return errors.New("no playable card of that type (can't play a card the turn you bought it)")
	}
	switch a.Card {
	case "knight":
		p.Knights++
		g.RobberPending = true
		g.logf("%s played a Knight", p.Name)
		if p.Knights >= 3 {
			cur := g.LargestArmyPlayer
			if cur == -1 || p.Knights > g.Players[cur].Knights {
				if cur != p.ID {
					g.LargestArmyPlayer = p.ID
					g.logf("%s now holds Largest Army", p.Name)
				}
			}
		}
	case "roadBuilding":
		n := MaxRoads - p.Roads
		if n > 2 {
			n = 2
		}
		if n == 0 {
			return errors.New("no road pieces left")
		}
		if !g.Rolled {
			return errors.New("roll before playing Road Building")
		}
		g.FreeRoads = n
		g.logf("%s played Road Building", p.Name)
	case "yearOfPlenty":
		if !g.Rolled {
			return errors.New("roll before playing Year of Plenty")
		}
		if !validResource(a.R1) || !validResource(a.R2) {
			return errors.New("pick two resources")
		}
		need := map[string]int{a.R1: 1}
		need[a.R2]++
		for r, n := range need {
			if g.Bank[r] < n {
				return fmt.Errorf("bank is out of %s", r)
			}
		}
		for r, n := range need {
			g.Bank[r] -= n
			p.Resources[r] += n
		}
		g.logf("%s played Year of Plenty (%s, %s)", p.Name, a.R1, a.R2)
	case "monopoly":
		if !g.Rolled {
			return errors.New("roll before playing Monopoly")
		}
		if !validResource(a.Resource) {
			return errors.New("pick a resource")
		}
		total := 0
		for _, pl := range g.Players {
			if pl.ID == p.ID {
				continue
			}
			n := pl.Resources[a.Resource]
			pl.Resources[a.Resource] = 0
			p.Resources[a.Resource] += n
			total += n
		}
		g.logf("%s played Monopoly and took all %s (%d cards)", p.Name, a.Resource, total)
	default:
		return errors.New("that card can't be played")
	}
	p.DevCards = append(p.DevCards[:idx], p.DevCards[idx+1:]...)
	g.PlayedDevThisTurn = true
	g.checkWin()
	return nil
}

// ---- Trading ----

// BankRate returns the best maritime rate for a player and resource.
func (g *Game) BankRate(p *Player, res string) int {
	rate := 4
	for v, b := range g.BuildingsV {
		if b.Player != p.ID {
			continue
		}
		pi := g.Board.Verts[v].Port
		if pi == -1 {
			continue
		}
		port := g.Board.Ports[pi]
		if port.Resource == "" && rate > 3 {
			rate = 3
		}
		if port.Resource == res {
			rate = 2
		}
	}
	return rate
}

func (g *Game) bankTrade(p *Player, give, get map[string]int) error {
	if err := g.requireActive(p); err != nil {
		return err
	}
	if len(give) != 1 || len(get) != 1 {
		return errors.New("trade one resource type for one other")
	}
	var giveRes, getRes string
	var giveN, getN int
	for r, n := range give {
		giveRes, giveN = r, n
	}
	for r, n := range get {
		getRes, getN = r, n
	}
	if !validResource(giveRes) || !validResource(getRes) || giveRes == getRes {
		return errors.New("invalid trade")
	}
	rate := g.BankRate(p, giveRes)
	if getN < 1 || giveN != rate*getN {
		return fmt.Errorf("rate for %s is %d:1", giveRes, rate)
	}
	if p.Resources[giveRes] < giveN {
		return errors.New("not enough cards")
	}
	if g.Bank[getRes] < getN {
		return fmt.Errorf("bank is out of %s", getRes)
	}
	p.Resources[giveRes] -= giveN
	g.Bank[giveRes] += giveN
	g.Bank[getRes] -= getN
	p.Resources[getRes] += getN
	g.logf("%s traded %d %s for %d %s with the bank", p.Name, giveN, giveRes, getN, getRes)
	return nil
}

func (g *Game) offerTrade(p *Player, give, get map[string]int) error {
	if err := g.requireActive(p); err != nil {
		return err
	}
	if g.Trade != nil {
		return errors.New("a trade is already pending")
	}
	if !validAmounts(give) || !validAmounts(get) || sum(give) == 0 || sum(get) == 0 {
		return errors.New("offer must give and request at least one card")
	}
	for r, n := range give {
		if n > 0 && get[r] > 0 {
			return errors.New("can't trade a resource for itself")
		}
	}
	if !p.has(give) {
		return errors.New("you don't have those cards")
	}
	g.Trade = &TradeOffer{From: p.ID, Give: give, Get: get}
	g.logf("%s offers a trade", p.Name)
	return nil
}

// tradeOpen: a pending trade can only be acted on while the game is in
// normal main-phase play (not during robber resolution or after the end).
func (g *Game) tradeOpen() error {
	if g.Trade == nil {
		return errors.New("no trade pending")
	}
	if g.Phase != PhaseMain {
		return errors.New("game not in play")
	}
	if len(g.DiscardPending) > 0 || g.RobberPending || g.StealPending {
		return errors.New("resolve the robber first")
	}
	return nil
}

func (g *Game) respondTrade(p *Player, accept bool) error {
	if err := g.tradeOpen(); err != nil {
		return err
	}
	if p.ID == g.Trade.From {
		return errors.New("you made this offer")
	}
	for _, id := range append(g.Trade.Accepted, g.Trade.Rejected...) {
		if id == p.ID {
			return errors.New("already responded")
		}
	}
	if accept {
		if !p.has(g.Trade.Get) {
			return errors.New("you don't have the requested cards")
		}
		g.Trade.Accepted = append(g.Trade.Accepted, p.ID)
		g.logf("%s accepts the trade", p.Name)
	} else {
		g.Trade.Rejected = append(g.Trade.Rejected, p.ID)
		g.logf("%s declines the trade", p.Name)
	}
	return nil
}

func (g *Game) confirmTrade(p *Player, with int) error {
	if err := g.tradeOpen(); err != nil {
		return err
	}
	if g.Trade.From != p.ID {
		return errors.New("no trade to confirm")
	}
	ok := false
	for _, id := range g.Trade.Accepted {
		if id == with {
			ok = true
		}
	}
	if !ok {
		return errors.New("that player hasn't accepted")
	}
	other := g.Players[with]
	if !p.has(g.Trade.Give) || !other.has(g.Trade.Get) {
		g.Trade = nil
		return errors.New("cards changed — trade cancelled")
	}
	for r, n := range g.Trade.Give {
		p.Resources[r] -= n
		other.Resources[r] += n
	}
	for r, n := range g.Trade.Get {
		other.Resources[r] -= n
		p.Resources[r] += n
	}
	g.logf("%s traded with %s", p.Name, other.Name)
	g.Trade = nil
	return nil
}

func (g *Game) cancelTrade(p *Player) error {
	if g.Trade == nil || g.Trade.From != p.ID {
		return errors.New("no trade to cancel")
	}
	g.Trade = nil
	g.logf("%s cancelled the trade", p.Name)
	return nil
}

// ---- Turn end / scoring ----

func (g *Game) endTurn(p *Player) error {
	if err := g.requireActive(p); err != nil {
		return err
	}
	if g.Trade != nil {
		g.Trade = nil
	}
	g.Rolled = false
	g.PlayedDevThisTurn = false
	g.FreeRoads = 0
	g.DiceA, g.DiceB = 0, 0
	g.Turn = (g.Turn + 1) % len(g.Players)
	g.TurnCount++
	g.logf("%s's turn", g.Players[g.Turn].Name)
	return nil
}

// Points returns a player's victory points. If private is true, hidden
// victory-point cards are included.
func (g *Game) Points(p *Player, private bool) int {
	pts := p.Setts + 2*p.Cities
	if g.LongestRoadPlayer == p.ID {
		pts += 2
	}
	if g.LargestArmyPlayer == p.ID {
		pts += 2
	}
	if private {
		for _, c := range p.DevCards {
			if c.Type == "victory" {
				pts++
			}
		}
	}
	return pts
}

func (g *Game) checkWin() {
	if g.Phase != PhaseMain {
		return
	}
	p := g.Players[g.Turn]
	if g.Points(p, true) >= WinPoints {
		g.Winner = p.ID
		g.Phase = PhaseEnded
		g.Trade = nil // a dangling offer must not execute post-game
		g.logf("%s wins with %d points!", p.Name, g.Points(p, true))
	}
}

// ---- Longest road ----

func (g *Game) updateLongestRoad() {
	best := make([]int, len(g.Players))
	for _, p := range g.Players {
		best[p.ID] = g.longestRoadFor(p.ID)
	}
	cur := g.LongestRoadPlayer
	// Current holder keeps the card on ties.
	if cur != -1 && best[cur] >= 5 {
		keep := true
		for id, l := range best {
			if id != cur && l > best[cur] {
				keep = false
			}
		}
		if keep {
			g.LongestRoadLen = best[cur]
			return
		}
	}
	// Find a unique maximum >= 5.
	maxLen, who, uniq := 0, -1, true
	for id, l := range best {
		if l > maxLen {
			maxLen, who, uniq = l, id, true
		} else if l == maxLen {
			uniq = false
		}
	}
	if maxLen >= 5 && uniq {
		if who != cur {
			g.LongestRoadPlayer = who
			g.LongestRoadLen = maxLen
			g.logf("%s now holds Longest Road (%d)", g.Players[who].Name, maxLen)
		}
	} else if cur != -1 && best[cur] < 5 {
		g.LongestRoadPlayer = -1
		g.LongestRoadLen = 0
		g.logf("Longest Road is up for grabs")
	}
}

func (g *Game) longestRoadFor(pid int) int {
	used := map[int]bool{}
	best := 0
	starts := map[int]bool{}
	for e, owner := range g.RoadsE {
		if owner == pid {
			starts[g.Board.Edges[e].V1] = true
			starts[g.Board.Edges[e].V2] = true
		}
	}
	for v := range starts {
		if l := g.dfsRoad(pid, v, used); l > best {
			best = l
		}
	}
	return best
}

func (g *Game) dfsRoad(pid, v int, used map[int]bool) int {
	best := 0
	for _, e := range g.Board.Verts[v].Edges {
		if used[e] {
			continue
		}
		owner, ok := g.RoadsE[e]
		if !ok || owner != pid {
			continue
		}
		used[e] = true
		next := g.Board.EdgeOther(e, v)
		l := 1
		// An opponent's building on the far vertex cuts the road there.
		if b, blocked := g.BuildingsV[next]; !blocked || b.Player == pid {
			l += g.dfsRoad(pid, next, used)
		}
		if l > best {
			best = l
		}
		used[e] = false
	}
	return best
}

// ---- misc ----

func validResource(r string) bool {
	for _, x := range Resources {
		if x == r {
			return true
		}
	}
	return false
}

func validAmounts(m map[string]int) bool {
	for r, n := range m {
		if !validResource(r) || n < 0 {
			return false
		}
	}
	return true
}

func sum(m map[string]int) int {
	t := 0
	for _, n := range m {
		t += n
	}
	return t
}
