# PeerWatch — Architecture Overview

PeerWatch is a serverless, peer-to-peer CLI application for synchronized video
watching. One person hosts a video file; others join using a connection token.
There is no central server — peers transfer video chunks directly to each other,
BitTorrent-style, and play back in sync via mpv.

## How It Works (High Level)

```mermaid
flowchart TD
    subgraph Host["Host Node"]
        HF["Local Video File"] --> HC["Chunker & Manifest Builder"]
        HC --> HS["Host Chunk Store"]
        HS --> HHTTP["Local HTTP Server"]
        HHTTP -->|Range Requests| Hmpv["mpv Player (Active)"]
        HSync["Sync Manager"] -->|Broadcasting State & Time| HSwarm["Host TCP Swarm"]
        
        HS -->|Read Chunks| HSwarm
        Hmpv -->|Query Position| HSync
    end

    subgraph Connection["Network Channel"]
        Proto["TCP Wire Protocol<br/>• HANDSHAKE<br/>• MANIFEST<br/>• BITFIELD<br/>• REQUEST<br/>• PIECE<br/>• SYNC<br/>• PEER_LIST<br/>• KEEPALIVE"]
    end

    subgraph Peer["Peer Node"]
        PSwarm["Peer TCP Swarm"] -->|Receives Chunks| PStore["Peer Sparse Store"]
        PStore -->|Feeds Data| PHTTP["Local HTTP Server"]
        PHTTP -->|Range Requests| Pmpv["mpv Player (Active)"]
        PHTTP -->|Urgent Demand| PSched["Download Scheduler"]
        PTracker["Availability Tracker"] -->|Rarest-first Prioritization| PSched
        PSched -->|Batch Requests| PSwarm
        PStore -->|Bitfield Updates| PSwarm
    end

    HSwarm <--> Connection
    Connection <--> PSwarm

    classDef hostStyle fill:#1e3a8a,stroke:#3b82f6,stroke-width:1px,color:#fff;
    classDef peerStyle fill:#064e3b,stroke:#10b981,stroke-width:1px,color:#fff;
    classDef connStyle fill:#334155,stroke:#94a3b8,stroke-width:1px,color:#fff;
    classDef playerStyle fill:#701a75,stroke:#d946ef,stroke-width:1px,color:#fff;
    classDef httpStyle fill:#7c2d12,stroke:#f97316,stroke-width:1px,color:#fff;
    
    class Host,HF,HC,HS,HSwarm,HSync hostStyle;
    class Peer,Pmpv,PHTTP,PSched,PTracker,PSwarm,PStore peerStyle;
    class Connection,Proto connStyle;
    class Hmpv,Pmpv playerStyle;
    class HHTTP,PHTTP httpStyle;
```

1. **Host** runs `./peerwatch start movie.mp4`
   - Reads the file, computes SHA-256 hashes per chunk (512KB each)
   - Starts a TCP listener
   - Prints a connection token (base64-encoded host address + metadata)

2. **Peer** runs `./peerwatch join <token>`
   - Decodes the token to get the host's IP:port
   - Connects via TCP, receives the manifest (file metadata + chunk hashes)
   - Creates a sparse file and starts downloading chunks
   - Connects to all other peers (full mesh)

3. **Chunk Transfer**
   - Peers request chunks in batches (`RequestMsg` with multiple indices)
   - The responder sends back individual `PieceMsg` for each chunk
   - Every chunk is verified against its SHA-256 hash before being stored

4. **Availability Tracking**
   - Instead of per-chunk HAVE announcements, each peer broadcasts its full
     bitfield every ~1 second — simpler, fewer messages, self-correcting

5. **Playback**
   - A local HTTP server serves the video chunks to mpv via Range requests
   - The host broadcasts the playback position every 2s; peers adjust their playback speed or seek to match and synchronize.

## Network Topology

Full mesh — every peer connects to every other peer directly.

```mermaid id="p2n4qx"
flowchart LR
    Host["Host"] <-->|"TCP"| B["Peer B"]
    Host <-->|"TCP"| C["Peer C"]
    Host <-->|"TCP"| D["Peer D"]
    
    B <-->|"TCP"| C
    B <-->|"TCP"| D
    C <-->|"TCP"| D

    classDef hostStyle fill:#1e3a8a,stroke:#3b82f6,stroke-width:2px,color:#fff;
    classDef peerStyle fill:#064e3b,stroke:#10b981,stroke-width:1px,color:#fff;
    class Host hostStyle;
    class B,C,D peerStyle;
```

```mermaid id="k7m1za"
graph LR
    T["Total connections:<br/>N*(N-1)/2"]

    N4["For N = 4"]
    C6["4*(4-1)/2 = 6"]

    N4 --> C6
    C6 --> T
```

This is fine for 5-10 peers. Each connection is a single TCP socket with two
goroutines (reader + writer). At 10 peers, that's 18 goroutines — trivial.

## Chunk Strategy

- **Chunk size**: 512 KB fixed (last chunk may be smaller)
- **Identification**: 0-based integer index
- **Integrity**: SHA-256 per chunk, verified on receipt
- **Storage**: Host wraps original file; peers use a sparse file filled at
  correct byte offsets as chunks arrive

The scheduler uses a hybrid strategy:

1. **Playback window first** — sequential chunks near the playback cursor
2. **Rarest-first** — for remaining download capacity, prefer chunks that
   fewest peers have (improves swarm-wide distribution)

## Wire Protocol

Binary, length-prefixed framing over TCP:

```
[4 bytes: length][1 byte: type][payload bytes]
```

10 message types: HANDSHAKE, MANIFEST, BITFIELD, HAVE (reserved), REQUEST
(batch), PIECE, CANCEL, SYNC, PEER_LIST, KEEPALIVE.

See `docs/protocol_sequence.md` for the exact message ordering.
