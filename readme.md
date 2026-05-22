# PeerWatch

Serverless peer-to-peer video watch party. One person hosts a video file, others join with a token. No central server — peers transfer chunks directly to each other, BitTorrent-style, and play back in sync via [mpv](https://mpv.io/).

```
Host                               Peers
┌──────────────┐                   ┌──────────────┐
│ ./peerwatch  │  ← TCP mesh →     │ ./peerwatch  │
│ start mov.mp4│                   │ join <token> │
│              │                   │              │
│ has all      │                   │ downloads    │
│ chunks ✓     │                   │ chunks...    │
└──────────────┘                   └──────────────┘
```

## Features

- **Zero dependencies** — single Go binary, stdlib only
- **No server** — direct peer-to-peer over TCP
- **Full mesh** — every peer connects to every other peer (optimized for 5-10 people)
- **Chunk-based transfer** — 512KB chunks with SHA-256 integrity verification
- **Batch requests** — request multiple chunks in a single message
- **Periodic bitfield** — peers broadcast availability every ~1s (self-correcting)
- **Format agnostic** — supports `.mp4`, `.mkv`, `.avi`, `.webm` (mpv handles decoding)

## Usage

### Host a room

```bash
./peerwatch start movie.mp4
```

This prints a connection token (base64-encoded host address + metadata).

### Join a room

```bash
./peerwatch join <token>
```

### Options

```bash
# Host on a specific port (default: 9876)
./peerwatch start -port 8080 movie.mp4

# Save downloaded video to a specific directory
./peerwatch join -out ~/Videos <token>
```

## Build

```bash
go build -o peerwatch .
```

## Test

```bash
go test ./... -v
```

## Project Structure

```
peerwatch/
├── main.go                          # CLI entry point
├── cmd/
│   ├── start.go                     # "peerwatch start" command
│   └── join.go                      # "peerwatch join" command
├── internal/
│   ├── chunk/
│   │   ├── chunker.go               # Fixed-size file chunking
│   │   ├── manifest.go              # SHA-256 manifest builder
│   │   ├── store.go                 # Chunk storage + bitfield tracking
│   │   └── store_test.go
│   ├── peer/
│   │   ├── peer.go                  # Peer connection (read/write loops)
│   │   ├── swarm.go                 # Full-mesh manager
│   │   ├── tracker.go               # Chunk availability tracker
│   │   └── peer_test.go
│   ├── protocol/
│   │   ├── message.go               # 10 wire protocol message types
│   │   ├── codec.go                 # Binary encode/decode
│   │   ├── handler.go               # Handler dispatch interface
│   │   └── codec_test.go
│   ├── scheduler/
│   │   ├── scheduler.go             # Download orchestration loop
│   │   ├── strategy.go              # Chunk priority + peer scoring
│   │   ├── scheduler_test.go
│   │   └── strategy_test.go
│   └── token/
│       ├── token.go                 # Connection token (base64url)
│       └── token_test.go
└── docs/
    ├── architecture.md              # High-level design & topology
    ├── types.md                     # Struct reference & ownership graph
    └── protocol_sequence.md         # Message ordering & sequences
```

## Documentation

- **[Architecture Overview](docs/architecture.md)** — how PeerWatch works, network topology, chunk strategy
- **[Type Reference](docs/types.md)** — every struct, its fields, ownership graph, invariants
- **[Protocol Sequence](docs/protocol_sequence.md)** — exact message ordering for connections, transfers, sync

## Requirements

- **Go 1.26+**
- **Linux** (other platforms untested)
- **mpv** (for video playback, Phase 4+)

## License

See [LICENSE](LICENSE).
