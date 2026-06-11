# Catan LAN Clone — Design

Date: 2026-06-11
Status: implemented

## Goal

A Settlers of Catan (base game) clone playable by 2–4 people on phones over
the same wifi. Go backend, HTML frontend, no install for players.

## Decisions

- **Transport:** Server-Sent Events for state push + JSON POST for actions.
  Chosen over WebSockets to keep the backend dependency-free (Go stdlib has
  no WebSocket support); Catan is turn-based so one-directional push is
  sufficient and EventSource auto-reconnects on flaky phone wifi.
- **One game per process.** A home-wifi session doesn't need rooms. Restart
  the binary (or "Play again") for a fresh game.
- **Authoritative server.** All rules validated in Go; the client is a dumb
  renderer. The server also computes legal placements per player so the
  client never duplicates rules logic.
- **Personalized views.** Each SSE client receives state filtered for them:
  own hand and dev cards visible, opponents as counts only.
- **Board model:** hexes generated on axial coordinates, vertices/edges
  derived by corner-position dedup into an explicit graph (54 verts, 72
  edges) with adjacency lists. Geometry (unit coordinates) is sent to the
  client, which only scales and draws SVG.
- **Reconnect:** join returns a secret token stored in localStorage; the
  token resumes the same seat at any time.
- **Frontend:** single embedded HTML file, vanilla JS, SVG board,
  tap-to-place interaction driven by server-provided legal-move lists,
  bottom-sheet modals for discard/steal/trade/dev cards.

## Rules scope

Full base game: spiral number tokens, ports, robber (discard/move/steal),
all five dev cards with timing rules, Longest Road with road-breaking,
Largest Army, bank shortage rule, piece limits, maritime + domestic trading,
win at 10 VP. No expansions, no special casing for 5–6 players.

## Testing

- Board invariants (shape, no adjacent 6/8, graph degrees) over many seeds.
- Full-game simulations with greedy random bots through the public action
  API, asserting resource conservation and piece limits after every action,
  and that games terminate with a legitimate winner.
