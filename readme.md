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

## Implementation Status

### ✅ Phase 1 — Foundation

- [x] Binary wire protocol (10 message types, length-prefixed framing)
- [x] Binary codec (encode/decode with 16MB cap)
- [x] File chunking (512KB fixed-size chunks)
- [x] SHA-256 manifest generation
- [x] Chunk store with sparse file support + bitfield tracking
- [x] `WaitForChunk` blocking mechanism for HTTP range requests
- [x] Base64url connection token codec
- [x] CLI scaffolding (`start` / `join` subcommands)

### ✅ Phase 2 — P2P Networking

- [x] Peer connection with read/write goroutine loops
- [x] Full-mesh swarm manager (accept, handshake, manifest exchange)
- [x] Host flow: accept → handshake → manifest → bitfield → peer list
- [x] Join flow: connect → handshake → receive manifest → join mesh
- [x] Periodic bitfield broadcast (replaces per-chunk HAVE messages)
- [x] Batch chunk REQUEST / individual PIECE response
- [x] Chunk availability tracker (rarity queries)
- [x] Download speed estimation (exponential moving average)
- [x] Integration test: host + peer localhost transfer with SHA-256 verification

### 🔲 Phase 3 — Scheduler

- [ ] Playback-window-first chunk priority
- [ ] Rarest-first scheduling for remaining capacity
- [ ] Per-peer request pipelining (concurrent in-flight limit)
- [ ] Peer selection (prefer faster peers)

### 🔲 Phase 4 — Video Playback

- [ ] Local HTTP server serving video via Range requests
- [ ] mpv launch via Unix socket IPC
- [ ] Blocking reads for chunks not yet downloaded

### 🔲 Phase 5 — Playback Sync

- [ ] Host broadcasts playback position every 2s
- [ ] Peer drift detection and correction (speed adjust / hard seek)
- [ ] Pause/resume synchronization

### 🔲 Phase 6 — Polish

- [ ] Keepalive messages (30s interval)
- [ ] Graceful reconnection on peer drop
- [ ] Download progress display
- [ ] Cancel in-flight requests on peer disconnect

## Requirements

- **Go 1.26+**
- **Linux** (other platforms untested)
- **mpv** (for video playback, Phase 4+)

## License

See [LICENSE](LICENSE).
