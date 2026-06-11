package game

import (
	"math"
	"math/rand/v2"
	"sort"
)

// Resource / terrain names. Terrain is named after the resource it yields;
// desert yields nothing.
const (
	Wood   = "wood"
	Brick  = "brick"
	Sheep  = "sheep"
	Wheat  = "wheat"
	Ore    = "ore"
	Desert = "desert"
)

var Resources = []string{Wood, Brick, Sheep, Wheat, Ore}

type Hex struct {
	ID      int     `json:"id"`
	Q, R    int     `json:"-"`
	Terrain string  `json:"terrain"`
	Number  int     `json:"number"` // 0 for desert
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Verts   [6]int  `json:"-"`
}

type Vertex struct {
	ID    int     `json:"id"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Hexes []int   `json:"-"`
	Edges []int   `json:"-"`
	Adj   []int   `json:"-"`
	Port  int     `json:"port"` // -1 = no port
}

type Edge struct {
	ID    int   `json:"id"`
	V1    int   `json:"v1"`
	V2    int   `json:"v2"`
	Hexes []int `json:"-"`
}

type Port struct {
	ID       int    `json:"id"`
	Resource string `json:"resource"` // "" = 3:1 any
	Ratio    int    `json:"ratio"`
	V1       int    `json:"v1"`
	V2       int    `json:"v2"`
}

type Board struct {
	Hexes  []Hex    `json:"hexes"`
	Verts  []Vertex `json:"verts"`
	Edges  []Edge   `json:"edges"`
	Ports  []Port   `json:"ports"`
	Robber int      `json:"robber"`
}

var axialDirs = [6][2]int{{1, 0}, {1, -1}, {0, -1}, {-1, 0}, {-1, 1}, {0, 1}}

// spiralCoords returns the 19 hex axial coordinates in an inward spiral
// (outer ring, middle ring, center) so the classic number-token sequence
// can be laid down in order.
func spiralCoords() [][2]int {
	var out [][2]int
	for radius := 2; radius >= 1; radius-- {
		q, r := axialDirs[4][0]*radius, axialDirs[4][1]*radius
		for i := 0; i < 6; i++ {
			for j := 0; j < radius; j++ {
				out = append(out, [2]int{q, r})
				q += axialDirs[i][0]
				r += axialDirs[i][1]
			}
		}
	}
	out = append(out, [2]int{0, 0})
	return out
}

// Classic token sequence (A..R), placed along the spiral, skipping desert.
var tokenOrder = []int{5, 2, 6, 3, 8, 10, 9, 12, 11, 4, 8, 10, 9, 4, 5, 6, 3, 11}

func hexDist(a, b [2]int) int {
	dq := a[0] - b[0]
	dr := a[1] - b[1]
	return (abs(dq) + abs(dr) + abs(dq+dr)) / 2
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// GenerateBoard builds a randomized standard board: shuffled terrain,
// spiral number tokens, 9 ports around the coast.
func GenerateBoard(rng *rand.Rand) *Board {
	for {
		b := tryGenerate(rng)
		if b != nil {
			return b
		}
	}
}

func tryGenerate(rng *rand.Rand) *Board {
	coords := spiralCoords()
	terrains := []string{
		Wood, Wood, Wood, Wood,
		Sheep, Sheep, Sheep, Sheep,
		Wheat, Wheat, Wheat, Wheat,
		Brick, Brick, Brick,
		Ore, Ore, Ore,
		Desert,
	}
	rng.Shuffle(len(terrains), func(i, j int) { terrains[i], terrains[j] = terrains[j], terrains[i] })

	b := &Board{Robber: -1}
	tok := 0
	for i, c := range coords {
		h := Hex{
			ID:      i,
			Q:       c[0],
			R:       c[1],
			Terrain: terrains[i],
			X:       math.Sqrt(3) * (float64(c[0]) + float64(c[1])/2),
			Y:       1.5 * float64(c[1]),
		}
		if h.Terrain == Desert {
			b.Robber = i
		} else {
			h.Number = tokenOrder[tok]
			tok++
		}
		b.Hexes = append(b.Hexes, h)
	}

	// Reject layouts where 6s/8s end up adjacent (can happen because the
	// desert shifts the token sequence).
	for i := range b.Hexes {
		if b.Hexes[i].Number != 6 && b.Hexes[i].Number != 8 {
			continue
		}
		for j := range b.Hexes {
			if j == i || (b.Hexes[j].Number != 6 && b.Hexes[j].Number != 8) {
				continue
			}
			if hexDist([2]int{b.Hexes[i].Q, b.Hexes[i].R}, [2]int{b.Hexes[j].Q, b.Hexes[j].R}) == 1 {
				return nil
			}
		}
	}

	buildGraph(b)
	placePorts(b, rng)
	return b
}

// buildGraph computes the 54 vertices and 72 edges with full adjacency.
func buildGraph(b *Board) {
	vertKey := map[[2]int]int{}
	edgeKey := map[[2]int]int{}

	cornerOf := func(h *Hex, k int) (float64, float64) {
		ang := math.Pi / 180 * float64(60*k-30)
		return h.X + math.Cos(ang), h.Y + math.Sin(ang)
	}

	for hi := range b.Hexes {
		h := &b.Hexes[hi]
		var ids [6]int
		for k := 0; k < 6; k++ {
			x, y := cornerOf(h, k)
			key := [2]int{int(math.Round(x * 100)), int(math.Round(y * 100))}
			id, ok := vertKey[key]
			if !ok {
				id = len(b.Verts)
				vertKey[key] = id
				b.Verts = append(b.Verts, Vertex{ID: id, X: x, Y: y, Port: -1})
			}
			ids[k] = id
			b.Verts[id].Hexes = append(b.Verts[id].Hexes, hi)
		}
		h.Verts = ids
		for k := 0; k < 6; k++ {
			a, c := ids[k], ids[(k+1)%6]
			lo, hi2 := a, c
			if lo > hi2 {
				lo, hi2 = hi2, lo
			}
			key := [2]int{lo, hi2}
			eid, ok := edgeKey[key]
			if !ok {
				eid = len(b.Edges)
				edgeKey[key] = eid
				b.Edges = append(b.Edges, Edge{ID: eid, V1: lo, V2: hi2})
				b.Verts[a].Edges = append(b.Verts[a].Edges, eid)
				b.Verts[c].Edges = append(b.Verts[c].Edges, eid)
				b.Verts[a].Adj = append(b.Verts[a].Adj, c)
				b.Verts[c].Adj = append(b.Verts[c].Adj, a)
			}
			b.Edges[eid].Hexes = appendUnique(b.Edges[eid].Hexes, hi)
		}
	}
}

func appendUnique(s []int, v int) []int {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

// placePorts assigns 9 ports to coastal edges (edges bordering exactly one
// hex), spaced around the perimeter like the standard board.
func placePorts(b *Board, rng *rand.Rand) {
	var coastal []int
	for _, e := range b.Edges {
		if len(e.Hexes) == 1 {
			coastal = append(coastal, e.ID)
		}
	}
	// Order coastal edges by angle around the board center.
	sort.Slice(coastal, func(i, j int) bool {
		return edgeAngle(b, coastal[i]) < edgeAngle(b, coastal[j])
	})

	types := []string{"", "", "", "", Wood, Brick, Sheep, Wheat, Ore}
	rng.Shuffle(len(types), func(i, j int) { types[i], types[j] = types[j], types[i] })

	slots := []int{0, 3, 7, 10, 13, 17, 20, 23, 27}
	start := rng.IntN(len(coastal))
	for i, slot := range slots {
		e := b.Edges[coastal[(slot+start)%len(coastal)]]
		ratio := 3
		if types[i] != "" {
			ratio = 2
		}
		p := Port{ID: i, Resource: types[i], Ratio: ratio, V1: e.V1, V2: e.V2}
		b.Ports = append(b.Ports, p)
		b.Verts[e.V1].Port = i
		b.Verts[e.V2].Port = i
	}
}

func edgeAngle(b *Board, eid int) float64 {
	e := b.Edges[eid]
	mx := (b.Verts[e.V1].X + b.Verts[e.V2].X) / 2
	my := (b.Verts[e.V1].Y + b.Verts[e.V2].Y) / 2
	return math.Atan2(my, mx)
}

// EdgeOther returns the other endpoint of an edge.
func (b *Board) EdgeOther(eid, v int) int {
	e := b.Edges[eid]
	if e.V1 == v {
		return e.V2
	}
	return e.V1
}
