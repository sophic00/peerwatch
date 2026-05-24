# Protocol Sequence — Message Ordering

This document defines the exact order of messages exchanged between peers
during connection setup and steady-state operation.

## 1. Peer Joins via Host

When a new peer (B) connects to the host (A), the following sequence occurs
**synchronously on the TCP connection** before the read/write goroutines start:

```mermaid
sequenceDiagram
    autonumber
    
    participant B as Peer B (Joiner)
    participant A as Host A (Room Host)

    rect rgba(30, 58, 138, 0.2)
        note over B,A: Phase 1: Synchronous Handshake & Connection Setup
        B->>A: TCP Connect
        note over B,A: Host A accepts connection
        B->>A: HANDSHAKE {peer_id: B, version: 1}
        A->>B: HANDSHAKE {peer_id: A, version: 1}
    end

    rect rgba(124, 45, 18, 0.2)
        note over B,A: Phase 2: Metadata & Swarm Discovery
        A->>B: MANIFEST {filename, filesize, chunk_size, chunk_count, hashes}
        A->>B: BITFIELD {1111...1} (Host has all chunks)
        A->>B: PEER_LIST {["peer_C:port", "peer_D:port"]}
    end

    rect rgba(6, 78, 59, 0.2)
        note over B,A: Phase 3: Start Duplex Communication Loops
        note over B,A: Duplex read/write loops started asynchronously
        B->>A: BITFIELD {0000...0} (Joiner has no chunks yet)
    end
```

**Order matters.** The host sends HANDSHAKE → MANIFEST → BITFIELD → PEER_LIST
in this exact sequence. The peer reads them synchronously before starting its
read loop. This is why `ConnectToHost()` reads four messages in a row before
calling `peer.Start()`.

## 2. Peer Connects to Another Peer

After receiving the PEER_LIST from the host, peer B connects to each listed
peer. This is a **non-host** handshake — no manifest is sent:

```mermaid
sequenceDiagram
    autonumber
    participant B as Peer B
    participant C as Peer C

    rect rgba(30, 58, 138, 0.2)
        note over B,C: Phase 1: TCP Dial & Non-Host Handshake
        B->>C: TCP Connect
        B->>C: HANDSHAKE {peer_id: B, version: 1}
        C->>B: HANDSHAKE {peer_id: C, version: 1}
    end

    rect rgba(6, 78, 59, 0.2)
        note over B,C: Phase 2: Duplex Loops & Bitfield Exchange
        note over B,C: Duplex read/write loops started
        C->>B: BITFIELD {1100110...0} (Peer C has partial chunks)
        B->>C: BITFIELD {0000000...0} (Peer B has no chunks yet)
    end
```

Peer B already has the manifest from the host. Only bitfields are exchanged.

## 3. Steady-State: Chunk Download

Once connected, chunks are requested and transferred asynchronously:

```mermaid
sequenceDiagram
    autonumber
    participant B as Peer B (Downloader)
    participant A as Host A (Uploader)

    rect rgba(30, 58, 138, 0.2)
        note over B,A: Phase 1: Batched Chunk Scheduling
        B->>A: REQUEST {chunk_indices: [0, 1, 2, 3]} (Batch request from Scheduler)
    end

    rect rgba(6, 78, 59, 0.2)
        note over B,A: Phase 2: Asynchronous Multi-Piece Dispatch
        A->>B: PIECE {chunk: 0, data: 512KB}
        A->>B: PIECE {chunk: 1, data: 512KB}
        A->>B: PIECE {chunk: 2, data: 512KB}
        A->>B: PIECE {chunk: 3, data: 512KB}
        note over B,A: Chunks verified on downloader via SHA-256 hashes
    end
```

- REQUEST is **batched** — multiple chunk indices in one message
- PIECE is **individual** — one chunk per message (they may be large, 512KB)
- Each received chunk is verified against its SHA-256 hash before being stored

## 4. Periodic Bitfield Broadcast

Every ~1 second, each peer sends its current bitfield to ALL connected peers:

```mermaid
sequenceDiagram
    autonumber
    participant B as Peer B (Broadcast)
    participant A as Host A
    participant C as Peer C
    participant D as Peer D

    rect rgba(6, 78, 59, 0.2)
        loop Every ~1 Second (Self-Correcting State Sync)
            B->>A: BITFIELD {0011110...0}
            B->>C: BITFIELD {0011110...0}
            B->>D: BITFIELD {0011110...0}
        end
    end
```

This replaces per-chunk HAVE messages. It's self-correcting — if a bitfield
message is lost, the next one carries the full state.

The receiving peer updates:

1. The sender's `Peer.bitfield` (for `HasChunk` queries)
2. The `Tracker.availability` map (for rarity calculations)

## 5. Playback Sync

Every 2 seconds, the host broadcasts its playback position:

```mermaid
sequenceDiagram
    autonumber
    participant A as Host A
    participant B as Peer B
    participant C as Peer C

    rect rgba(30, 58, 138, 0.2)
        loop Every 2 Seconds (Active Coordinated Drift Control)
            A->>B: SYNC {time: 45.2, state: PLAYING, unix_ms: ...}
            A->>C: SYNC {time: 45.2, state: PLAYING, unix_ms: ...}
            note over B,C: Peers compare position & adjust speed (0.95x - 1.05x) or seek
        end
    end
```

Peers compare their position and adjust:

- `|drift| > 2s` → hard seek
- `drift ∈ [-2, -0.5]` → speed up to 1.05x
- `drift ∈ [0.5, 2]` → slow down to 0.95x
- `|drift| < 0.5` → normal speed

## 6. Peer Disconnection

When a peer disconnects (TCP close or error):

1. The `readLoop` or `writeLoop` exits
2. `Peer.Close()` is called (via `sync.Once` — safe from multiple goroutines)
3. The `done` channel is closed
4. `Swarm.addPeer`'s monitor goroutine detects it and:
   - Removes the peer from `Swarm.peers`
   - Removes the peer from `Tracker.availability`
   - Logs the disconnection

No explicit "goodbye" message is sent. TCP close is the signal.

## Message Summary Table

| Message   | When Sent                          | By Whom     | Payload Size         |
| --------- | ---------------------------------- | ----------- | -------------------- |
| HANDSHAKE | On every new TCP connection        | Both sides  | 17 bytes             |
| MANIFEST  | After handshake (host→joiner only) | Host        | ~36×chunkCount bytes |
| BITFIELD  | After handshake + every 1s         | Everyone    | chunkCount/8 bytes   |
| REQUEST   | When scheduler needs chunks        | Downloaders | 8 + 4×N bytes        |
| PIECE     | In response to REQUEST             | Uploaders   | 4 + chunkSize bytes  |
| SYNC      | Every 2s                           | Host        | 17 bytes             |
| PEER_LIST | After handshake (host→joiner)      | Host        | variable             |
| KEEPALIVE | Every 30s                          | Everyone    | 0 bytes              |
| HAVE      | _Reserved for v2_                  | —           | 8 bytes              |
| CANCEL    | _Not yet implemented_              | —           | 8 bytes              |
