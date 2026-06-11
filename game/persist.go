package game

import (
	"bytes"
	"encoding/gob"
	"os"
)

// snapshot mirrors Game's persistent fields (everything except the mutex,
// RNG, and change channel). Encoded with gob so the json:"-" view tags on
// board internals don't strip the graph.
type snapshot struct {
	Board             *Board
	Players           []*Player
	Phase             string
	SetupIdx          int
	SetupStep         string
	LastSetupVert     int
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
	BuildingsV        map[int]Building
	RoadsE            map[int]int
	DevDeck           []string
	Bank              map[string]int
	LongestRoadPlayer int
	LongestRoadLen    int
	LargestArmyPlayer int
	Trade             *TradeOffer
	Winner            int
	Log               []LogEntry
	Version           int
	RollCounts        [13]int
}

// Save writes the full game state atomically so a server restart can
// resume mid-game. Encoding happens under the lock — the snapshot holds
// references to live maps, so encoding outside would race with the game.
func (g *Game) Save(path string) error {
	g.Mu.Lock()
	s := snapshot{
		Board: g.Board, Players: g.Players, Phase: g.Phase,
		SetupIdx: g.SetupIdx, SetupStep: g.SetupStep, LastSetupVert: g.LastSetupVert,
		Turn: g.Turn, TurnCount: g.TurnCount, DiceA: g.DiceA, DiceB: g.DiceB,
		Rolled: g.Rolled, RobberPending: g.RobberPending, StealPending: g.StealPending,
		StealCandidates: g.StealCandidates, DiscardPending: g.DiscardPending,
		PlayedDevThisTurn: g.PlayedDevThisTurn, FreeRoads: g.FreeRoads,
		BuildingsV: g.BuildingsV, RoadsE: g.RoadsE, DevDeck: g.DevDeck, Bank: g.Bank,
		LongestRoadPlayer: g.LongestRoadPlayer, LongestRoadLen: g.LongestRoadLen,
		LargestArmyPlayer: g.LargestArmyPlayer, Trade: g.Trade, Winner: g.Winner,
		Log: g.Log, Version: g.Version, RollCounts: g.RollCounts,
	}
	var buf bytes.Buffer
	err := gob.NewEncoder(&buf).Encode(&s)
	g.Mu.Unlock()
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Load restores a saved game. Returns an error if the file is missing or
// unreadable; callers should fall back to a fresh game.
func Load(path string) (*Game, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var s snapshot
	if err := gob.NewDecoder(f).Decode(&s); err != nil {
		return nil, err
	}
	g := New()
	g.Board, g.Players, g.Phase = s.Board, s.Players, s.Phase
	g.SetupIdx, g.SetupStep, g.LastSetupVert = s.SetupIdx, s.SetupStep, s.LastSetupVert
	g.Turn, g.TurnCount, g.DiceA, g.DiceB = s.Turn, s.TurnCount, s.DiceA, s.DiceB
	g.Rolled, g.RobberPending, g.StealPending = s.Rolled, s.RobberPending, s.StealPending
	g.StealCandidates, g.DiscardPending = s.StealCandidates, s.DiscardPending
	g.PlayedDevThisTurn, g.FreeRoads = s.PlayedDevThisTurn, s.FreeRoads
	g.BuildingsV, g.RoadsE, g.DevDeck, g.Bank = s.BuildingsV, s.RoadsE, s.DevDeck, s.Bank
	g.LongestRoadPlayer, g.LongestRoadLen = s.LongestRoadPlayer, s.LongestRoadLen
	g.LargestArmyPlayer, g.Trade, g.Winner = s.LargestArmyPlayer, s.Trade, s.Winner
	g.Log, g.Version = s.Log, s.Version
	g.RollCounts = s.RollCounts
	return g, nil
}
