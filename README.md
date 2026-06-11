# Catan — LAN edition

A Settlers of Catan clone with full base-game rules. Go backend, single-page
mobile-first HTML frontend. Everyone on the same wifi plays from their phone.

## Run

```sh
go run .
```

The server prints the LAN URLs, e.g.

```
Players on the same wifi can join at:
  http://192.168.1.42:8080
```

Each player opens that address on their phone, enters a name, and the first
player (host 👑) starts the game. 2–4 players.

Custom port: `go run . -addr :9000`

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
- `main.go` — HTTP server; actions via `POST /api/action`, realtime state via
  Server-Sent Events (`GET /api/events`); frontend embedded in the binary
- `web/index.html` — the whole UI: SVG board, touch placement, modals

No external dependencies — Go stdlib only. State is in-memory (one game per
server process); players reconnect automatically via a token in localStorage.

## Tests

```sh
go test ./...
```

Includes board-shape invariants and full random-bot game simulations that
check resource conservation and piece limits on every action.
