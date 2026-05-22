# Type Reference — Structs and Relationships

This document maps every core struct, what it holds, and how they connect.

## Package `internal/chunk`

### `Chunker`
Reads fixed-size chunks from a file by index.

| Field       | Type      | Description                        |
|-------------|-----------|------------------------------------|
| `file`      | `*os.File`| Open file handle (caller owns it)  |
| `fileSize`  | `int64`   | Total file size in bytes           |
| `chunkSize` | `int64`   | Configured chunk size (default 512KB) |

Created by `NewChunker(file, chunkSize)`. Used internally by `BuildManifest`.

---

### `Manifest`
Table of contents for a shared video file. Computed once by the host, sent to
all peers.

| Field        | Type          | Description                       |
|--------------|---------------|-----------------------------------|
| `FileName`   | `string`      | Base name of the video file       |
| `FileSize`   | `int64`       | Total file size in bytes          |
| `ChunkSize`  | `int64`       | Chunk size (normally 524288)      |
| `ChunkCount` | `int`         | Total number of chunks            |
| `Chunks`     | `[]ChunkMeta` | Per-chunk metadata (size + hash)  |

### `ChunkMeta`
| Field  | Type       | Description       |
|--------|------------|-------------------|
| `Size` | `int64`    | Actual chunk size |
| `Hash` | `[32]byte` | SHA-256 digest    |

---

### `Store`
Manages chunk storage on disk and tracks availability via a bitfield.

| Field      | Type                    | Description                            |
|------------|-------------------------|----------------------------------------|
| `file`     | `*os.File`              | Video file (original for host, sparse for peer) |
| `manifest` | `*Manifest`             | Reference to the manifest              |
| `bitfield` | `[]byte`                | 1 bit per chunk (1=have, 0=missing)    |
| `count`    | `int`                   | Number of chunks available locally     |
| `isHost`   | `bool`                  | True if wrapping the original file     |
| `waiters`  | `map[int]chan struct{}`  | Per-chunk channels for blocking reads  |

**Who holds a Store?**
- `Swarm` holds a `*Store` (set at creation for host, via `SetStore` for peers)
- The CLI commands (`cmd/start.go`, `cmd/join.go`) create and own the Store

---

## Package `internal/peer`

### `Peer`
Represents a single remote peer connection. One instance per TCP connection.

| Field       | Type                   | Description                              |
|-------------|------------------------|------------------------------------------|
| `ID`        | `[16]byte`             | Random UUID for this peer                |
| `Addr`      | `string`               | Remote address (ip:port)                 |
| `conn`      | `net.Conn`             | The TCP connection                       |
| `outCh`     | `chan protocol.Message` | Buffered channel (cap=64) for writes     |
| `bitfield`  | `[]byte`               | What chunks this peer has                |
| `speed`     | `float64`              | Download speed (bytes/sec, EMA α=0.3)   |
| `inFlight`  | `map[uint32]time.Time` | Chunks requested but not yet received    |
| `done`      | `chan struct{}`         | Closed on disconnect                     |
| `closeOnce` | `sync.Once`            | Ensures Close() is safe from any goroutine |

Each Peer runs **2 goroutines** on a single TCP connection (full duplex):
- `readLoop` — reads messages from TCP, dispatches to handler
- `writeLoop` — reads from `outCh`, writes to TCP (serializes all writes)

---

### `Swarm`
Manages the full mesh of all peer connections.

| Field                | Type                              | Description                          |
|----------------------|-----------------------------------|--------------------------------------|
| `selfID`             | `[16]byte`                        | This node's peer ID                  |
| `isHost`             | `bool`                            | True if this is the room host        |
| `store`              | `*chunk.Store`                    | Local chunk storage                  |
| `manifest`           | `*chunk.Manifest`                 | Video file manifest                  |
| `tracker`            | `*Tracker`                        | Chunk availability across all peers  |
| `peers`              | `map[[16]byte]*Peer`              | Connected peers by ID                |
| `listener`           | `net.Listener`                    | TCP listener (host only)             |
| `hostAddr`           | `string`                          | Host address we connected to (joiner only) |
| `OnPieceReceived`    | `func(index uint32, data []byte)` | Callback when a chunk arrives        |
| `OnManifest`         | `func(manifest *chunk.Manifest)`  | Callback when joiner receives manifest |
| `OnSyncReceived`     | `func(msg *protocol.SyncMsg)`     | Callback when joiner receives SYNC   |
| `OnPeerDisconnected` | `func(peerID [16]byte, inFlightChunks []uint32)` | Callback when peer disconnects |
| `done`               | `chan struct{}`                    | Closed on shutdown                   |

**Invariants:**
- Host swarm: `store` and `manifest` must be non-nil at creation
- Peer swarm: `store` and `manifest` may be nil initially (set after connecting)
- `Listen()` — host only
- `ConnectToHost()` — peer only
- `SetStore()` — peer only

---

### `Tracker`
Aggregates chunk availability across all peers.

| Field          | Type                            | Description                         |
|----------------|---------------------------------|-------------------------------------|
| `chunkCount`   | `int`                           | Total number of chunks              |
| `availability` | `[]map[[16]byte]struct{}`       | Per-chunk set of peer IDs who have it |

**Who holds a Tracker?**
- `Swarm` holds one `*Tracker`
- Updated whenever a `BITFIELD` message is received from any peer
- Queried by the scheduler for rarity (rarest-first) and peer selection

---

## Package `internal/protocol`

### Message Types
All implement the `Message` interface (`Type() byte`).

| Struct         | Type ID | Key Fields                          |
|----------------|---------|-------------------------------------|
| `HandshakeMsg` | `0x01`  | `PeerID [16]byte`, `Version uint8`  |
| `ManifestMsg`  | `0x02`  | `FileName`, `FileSize`, `ChunkSize`, `ChunkCount`, `Chunks []ChunkInfo` |
| `BitfieldMsg`  | `0x03`  | `Bitfield []byte`                   |
| `HaveMsg`      | `0x04`  | `ChunkIndex uint32` *(unused in v1)* |
| `RequestMsg`   | `0x05`  | `ChunkIndices []uint32` *(batch)*   |
| `PieceMsg`     | `0x06`  | `ChunkIndex uint32`, `Data []byte`  |
| `CancelMsg`    | `0x07`  | `ChunkIndex uint32`                 |
| `SyncMsg`      | `0x08`  | `PlaybackTime float64`, `State uint8`, `UnixMs int64` |
| `PeerListMsg`  | `0x09`  | `Addrs []string`                    |
| `KeepaliveMsg` | `0x0A`  | *(empty)*                           |

---

## Package `internal/token`

### `Token`
Connection info encoded as a compact base64url string.

| Field        | Type     | JSON key | Description               |
|--------------|----------|----------|---------------------------|
| `Host`       | `string` | `h`      | Host address (ip:port)    |
| `RoomID`     | `string` | `r`      | Random room identifier    |
| `FileName`   | `string` | `f`      | Video file name           |
| `FileSize`   | `int64`  | `s`      | File size in bytes        |
| `ChunkCount` | `int`    | `c`      | Total number of chunks    |

---

## Package `internal/scheduler`

### `Scheduler`
Orchestrates chunk downloads across the swarm using priority queues and rate throttling.

| Field | Type | Description |
|---|---|---|
| `store` | `*chunk.Store` | Reference to local chunk storage |
| `swarm` | `*peer.Swarm` | Reference to the peer swarm |
| `cursor` | `int` | Current playback index (tells scheduler where sequential demands are) |
| `inFlight` | `map[int]struct{}` | Map of chunk indices currently in-flight globally |
| `urgent` | `map[int]struct{}` | Set of chunk indices needed immediately by player read blocks |

---

## Package `internal/player`

### `Player`
Controls the local `mpv` process using its JSON-RPC socket IPC interface.

| Field | Type | Description |
|---|---|---|
| `cmd` | `*exec.Cmd` | The mpv subprocess |
| `ipcPath` | `string` | Path to mpv's IPC socket file |
| `conn` | `net.Conn` | Connection to the mpv socket |
| `pending` | `map[uint64]chan ipcResponse` | Waiters for RPC command responses |

### `Server`
A local HTTP server that streams video content to `mpv` via Range requests.

| Field | Type | Description |
|---|---|---|
| `store` | `*chunk.Store` | Chunk storage where video chunks are loaded |
| `scheduler` | `*scheduler.Scheduler` | The download scheduler to escalate missing chunks |
| `listener` | `net.Listener` | The TCP localhost listener |
| `port` | `int` | Randomly assigned localhost port |

---

## Package `internal/sync`

### `SyncManager`
Coordinates playback seek, play, and pause events to keep peers in sync with the room host.

| Field | Type | Description |
|---|---|---|
| `swarm` | `*peer.Swarm` | The peer mesh swarm |
| `player` | `*player.Player` | The local mpv controller |
| `isHost` | `bool` | True if this node is the host (who broadcasts SYNC) |

---

## Ownership Graph

```
cmd/start.go or cmd/join.go
  ├── creates chunk.Store
  ├── creates peer.Swarm
  │     ├── holds *chunk.Store (reference)
  │     ├── holds *chunk.Manifest (reference)
  │     ├── holds *peer.Tracker
  │     │     └── per-chunk availability maps
  │     └── holds map of *peer.Peer
  │           ├── holds net.Conn
  │           ├── holds outCh (write queue)
  │           └── holds bitfield + inFlight state
  ├── creates scheduler.Scheduler (joiner only)
  │     ├── holds *chunk.Store (reference)
  │     └── holds *peer.Swarm (reference)
  ├── creates player.Server (local HTTP stream server)
  │     ├── holds *chunk.Store
  │     └── holds *scheduler.Scheduler (or nil for host)
  ├── creates player.Player (mpv process controller)
  └── creates sync.SyncManager
        ├── holds *peer.Swarm
        └── holds *player.Player
```

The CLI layer (`cmd/start.go` or `cmd/join.go`) owns the Store, Swarm, Scheduler, Player Server, and Player Controller, coordinating lifetimes across all modules. Peers in the Swarm own their TCP read/write goroutines and connections.
