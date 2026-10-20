# PeerWatch

**A serverless, peer-to-peer watch party.** A group joins a room, one person
proposes a local video, the group votes, and the video is distributed
BitTorrent-style while everyone plays back in sync.

No media server. No accounts. No database. Video blocks, playback state,
votes, and chat flow directly between peers over encrypted WebRTC.

```
   browser  ←──────┐
                   │
   desktop  ←──────┼── full mesh, encrypted WebRTC, no media server
                   │
   CLI      ←──────┘
```

## Status

**Specification complete, implementation not started.** The repository
currently contains a 7-crate Cargo workspace of empty stubs and a 6,000-line
specification. That is deliberate: the protocol was specified before it was
written, because the hard parts (authority, blame, sync, storage asymmetry)
are design problems, not coding problems.

Track progress in [docs/roadmap.md](docs/roadmap.md). M0 is the protocol
schema plus a deterministic multi-peer simulator; M1 is two peers exchanging
blocks across NATs.

## Why it is built this way

Three constraints shaped everything:

1. **A web page cannot open a raw socket.** The only direct peer-to-peer
   transport in a browser is WebRTC. Since browser peers must upload as well
   as download, the *entire* P2P stack runs in the browser — and native peers
   must speak WebRTC too.
2. **A browser cannot play a half-downloaded MKV.** Playback goes through MSE,
   which needs keyframe-aligned fragments. So the canonical wire format is
   fragmented MP4 (H.264/AAC), which mpv also plays natively — one wire
   format, no dual distribution.
3. **A distributed system has no trusted server to ask.** Host changes, kicks,
   and banlists are decided by signed certificates over a frozen roster, and
   every peer verifies them independently.

The full reasoning, including 28 decisions and 32 corrections made during the
rewrite, is in [docs/design/design-log.md](docs/design/design-log.md).

## Documentation

Start at **[docs/README.md](docs/README.md)**. It has the reading order, the
precedence ladder, and the invariants.

| Tier | What it is | Start here |
|---|---|---|
| **Normative** | Wire format, core API, error codes, limits | [spec/protocol.md](docs/spec/protocol.md) |
| **Design** | Rationale, maths, threat model, subsystem behaviour | [design/architecture.md](docs/design/architecture.md) |
| **Guide** | Config, operations, testing, conformance | [guide/config.md](docs/guide/config.md) |

## Repository layout

```
crates/
  pw-core/       pure protocol state machines — no I/O, native + wasm32
  pw-transport/  WebRTC adapters (webrtc-rs native, RTCPeerConnection browser)
  pw-media/      ffmpeg probe/remux/transcode-ahead + fMP4 segmenter
  pw-player/     Player command/event adapter + mpv
  pw-signal/     signaling client and self-hostable relay
  pw-cli/        daemon + control client + optional TUI
  pw-app/        Tauri 2 desktop shell
web/             shared web UI (browser build and Tauri webview)
docs/            specification
scripts/         documentation linters and generators
```

M0 splits these into 13 crates; see
[docs/design/architecture.md §6](docs/design/architecture.md#6-repository-layout)
for why each split is required for a clean WASM build.

## Building

Requires Rust 2024 edition (1.85+).

```sh
cargo build --workspace
cargo test --workspace
```

Documentation checks (no Python dependencies required):

```sh
python3 scripts/lint_invariants.py    # invariants, links, orphans, tiers, §refs
python3 scripts/gen_catalog.py --check # message catalog is current
```

## What this is not

- **Not a streaming service.** There is no catalogue, no accounts, no
  recommendations, no telemetry. Rooms are ephemeral and exist only in the
  peers' memories.
- **Not anonymous.** The relay and TURN see IP addresses. Traffic patterns
  reveal that peers are talking.
- **Not for huge rooms.** Full mesh targets 5–16 peers. Partial-mesh gossip
  is designed-for but not specified.
- **Not a general Byzantine consensus protocol.** A coalition controlling a
  majority of the frozen roster can decide anything. That is the governance
  model, and the docs say so plainly.

PeerWatch shares files among people who each must lawfully possess the
content. The software is a tool; use it accordingly.

## License

MIT — see [LICENSE](LICENSE).
