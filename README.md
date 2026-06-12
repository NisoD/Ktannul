# Small Mitayshvim

A hex-based settlement and trading board game for 2–4 players (plus bots).
Go backend (stdlib only), single-page web frontend with a Three.js 3D board.
Friends join via 6-character room codes.

## Run

```sh
go run . -addr :8080 -data ./data
```

Open http://localhost:8080, create a game, share the room link or QR code.

## Rules implemented

- Random standard board: 19 hexes, classic spiral number tokens (no adjacent
  6/8), 9 ports (4× 3:1, one 2:1 per resource), robber starts on the desert
- Snake-order setup; second settlement grants its starting resources
- Dice roll resource production (cities ×2), robber blocks production, bank
  shortage rule (19 cards per resource)
- Rolling 7: discard half over 7 cards, move robber, steal a random card
- Building costs and piece limits (15 roads, 5 settlements, 4 cities),
  distance rule, road connectivity (opponent buildings block)
- Development cards (14 knights, 5 VP, 2 each Road Building / Year of
  Plenty / Monopoly): one per turn, not on the turn bought, knight playable
  before rolling
- Longest Road (≥5, roads broken by opponent settlements, holder keeps ties)
  and Largest Army (≥3 knights)
- Trading: bank 4:1, ports 3:1 and 2:1, player-to-player offers with
  accept/decline and offerer confirmation
- Win at 10 victory points (hidden VP cards count)

## Architecture

- `game/board.go` — board generation: hex/vertex/edge graph, ports
- `game/game.go` — rules engine; every action validated server-side
- `game/view.go` — per-player state (your hand is private) + legal-move hints
- `hub.go` — game rooms: create/join by code, snapshots, idle expiry
- `httpserver.go` — room-scoped HTTP API, rate limits, security headers
- `main.go` — wiring, bot ticker, janitor, graceful shutdown
- `web/landing.html` — create/join page
- `web/index.html` — the whole game UI: 3D board, touch placement, modals

No external dependencies — Go stdlib only. Each room snapshots to
`data/<code>.gob` and survives restarts; players reconnect automatically via
a token in localStorage.

## Tests

```sh
go test ./... -race
```

Includes board-shape invariants and full random-bot game simulations that
check resource conservation and piece limits on every action.
