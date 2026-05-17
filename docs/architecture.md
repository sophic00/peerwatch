# PeerWatch — Architecture Overview

PeerWatch is a serverless, peer-to-peer CLI application for synchronized video
watching. One person hosts a video file; others join using a connection token.
There is no central server — peers transfer video chunks directly to each other,
BitTorrent-style, and play back in sync via mpv.

## How It Works (High Level)

```mermaid
graph TD
    A["Host<br/>video.mp4<br/>all chunks ✓<br/>mpv playing"]
    B["Peer B<br/>.partial file<br/>downloading…<br/>mpv playing"]
    C["Peer C<br/>.partial<br/>downloading"]

    A <-->|TCP mesh| B
    A <-->|TCP mesh| C
    B <-->|TCP mesh| C
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

5. **Playback** (Phase 4-5, not yet implemented)
   - A local HTTP server serves the video to mpv via Range requests
   - The host broadcasts playback position every 2s; peers adjust to match

## Network Topology

Full mesh — every peer connects to every other peer directly.

```mermaid id="p2n4qx"
graph TD
    A["Host"]
    B["Peer B"]
    C["Peer C"]
    D["Peer D"]

    A <-->|TCP| B
    A <-->|TCP| C
    A <-->|TCP| D

    B <-->|TCP| C
    B <-->|TCP| D

    C <-->|TCP| D
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

The scheduler (Phase 3) will use a hybrid strategy:

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
