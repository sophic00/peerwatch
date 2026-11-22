---
tier: normative
---

# Protocol Schema (normative)

This document is the **normative** definition of PeerWatch v3's wire objects,
encoding, authority model, and validation rules. Other documents explain the
*why* and subsystem behaviour; when they disagree with this file, this file
wins (see the precedence ladder in [README](../README.md)).

The protocol version is **3**. RFC 2119 keywords (**MUST**, **MUST NOT**,
**SHOULD**, **MAY**) are used normatively across `docs/spec/`.

---

## 1. Encoding and framing

### 1.1 Serialization

- All payloads are **deterministic CBOR** (RFC 8949 §4.2): definite lengths,
  shortest-form integers, and map keys sorted by their encoded bytes.
- Hashes and signatures of protocol objects are computed over the exact
  canonical CBOR byte string prefixed with a domain string (§3). Receivers
  MUST verify hashes/signatures over the received canonical bytes without
  re-encoding them.
- Unknown CBOR map keys are ignored. Unknown message types follow §10.
- Decoders MUST enforce maximum nesting depth (16), maximum collection length
  (4096), and maximum byte/text length **before** allocating.

### 1.2 Transport channels

WebRTC DataChannels are message-oriented: one envelope = one DataChannel
message.

| Channel | SCTP mode | Traffic |
|---------|-----------|---------|
| `control` | reliable, ordered | handshake, CRDT state digest, leases, governance, queue, sync, chat, availability |
| `bulk` | reliable, ordered | `STATE_SYNC` batches, manifest head/chunks/appends, subtitles |
| `data` | reliable, unordered | `REQUEST`, `BLOCK`, `CANCEL`, `CHALLENGE_RESPONSE` |

Because all three DataChannels share one SCTP association and congestion
window, senders MUST pace `data` and `bulk` writes against **aggregate**
`bufferedAmount` across `data` + `bulk` (high watermark 256 KiB, low watermark
128 KiB) so `SYNC`, `PING`, and votes on `control` do not suffer SCTP
bufferbloat.

### 1.3 Envelope

```
Envelope = {
  v:       uint,                  // 3
  t:       uint,                  // message type (from schema/messages.yaml)
  flags:   uint,                  // bit 0 signed, bit 1 optional, bit 2 bulk
  room:    bytes .size 16,        // room_id
  stream:  bytes .size 16 / null,
  author:  bytes .size 32 / null, // Ed25519 public key; required if signed
  seq:     uint / null,           // per-(author, stream, type, term) replay counter
  payload: bytes,                 // canonical CBOR of the per-type struct
  sig:     bytes .size 64 / null, // Ed25519 signature; required iff bit 0 set
}
```

- `room` binds every message to one room. A receiver MUST reject any envelope
  whose `room` does not match the connection's authenticated room.
- `author` is required for every signed message and every gossiped message so
  forwarded objects remain independently verifiable.
- `seq` is required for replay-sensitive signed messages (`SYNC`, `START_AT`,
  `MANIFEST_APPEND`, `CONTROL_REQ`, `CHAT`, `TAKEOVER_*`, `PEER_SIGNAL_KEY`).
  Receivers track the highest accepted `seq` per `(author, stream, type, term)`
  and reject stale or duplicate values.
- `sig` is computed over:
  ```
  "pw-envelope-v3" || canonical(Envelope without sig)
  ```
  and MUST verify against `author`.
- `AUTH` (§4.3) is sent with envelope `flags & 1 == 0` (`sig = null`); its
  transcript-bound `auth_sig` inside the payload is the sole signature for the
  handshake.

### 1.4 Message size

- Default maximum envelope: **64 KiB**.
- `HELLO` advertises `max_message_size`; the negotiated value is
  `min(local, remote)`. Supported range: **48 KiB–256 KiB**.
- `BLOCK` carries up to a 32 KiB block plus its Merkle proof (≤ 12 hashes =
  384 B for a 128 MiB segment) and fits comfortably inside 48 KiB.
- `MANIFEST_CHUNK` payload is at most 32 KiB.

### 1.5 Integrity layers

| Layer | Protects | Mechanism |
|-------|----------|-----------|
| Transport | connection confidentiality & forward secrecy | DTLS 1.2/1.3 (WebRTC) |
| Identity | authorship & transport binding | Ed25519 + AUTH DTLS fingerprint binding |
| Membership | room admission & kick exclusion | signed `MEMBER_ADD` chain + `KICK_CERT` banlist |
| Authority | host clock, manifest, queue, governance | `HostLease`, `VoteCertificate`, `TakeoverCertificate`, `term` (§5) |
| Content metadata | manifest & subtitles | host signature + `manifest_hash` + chunk hashes |
| Content bytes | every block | BLAKE3 Merkle proof against `segment_root` (§6) |
| Availability | segment bitmaps & frontier masks | untrusted scheduling hint only |

---

## 2. Identifiers and capabilities

```
PeerId     = base32(BLAKE3("pw-peer-v3" || ed25519_pubkey))  // 52 chars
RoomId     = 128-bit random
StreamId   = 128-bit random
Hash32     = 32-byte BLAKE3 output
PublicKey  = 32-byte Ed25519 public key
EncPubKey  = 32-byte X25519 public key
Signature  = 64-byte Ed25519 signature
Term       = uint64, monotonically increasing host lease number
RoomMode   = DEMOCRATIC | COLLABORATIVE
```

`PeerId` is displayed as `pw:<first 8 chars>…`. A new keypair is a new peer.

### 2.1 Capabilities

`HELLO` carries structured capability declarations:

```
Caps = {
  flags: u64,               // NATIVE, LOW_UPLOAD, VOICE, STORAGE_CLASS, PERSISTENT
  storage_budget_mb: u32,   // 0 = memory-only / streaming-only
  containers: [Container],  // FMP4 | WEBM
  video_codecs: [VideoCodec], // H264 | VP9 | AV1 | HEVC
  audio_codecs: [AudioCodec], // AAC | OPUS
  max_h264_level: u16,      // e.g. 41 = Level 4.1 (0 if H264 unsupported)
}
```

Bit assignments for `flags`:

| Bit | Flag | Meaning |
|----:|------|---------|
| 0 | `NATIVE` | native daemon (`pw-node`) rather than browser WASM |
| 1 | `LOW_UPLOAD` | metered or constrained uplink |
| 2 | `VOICE` | Opus voice tracks supported (v2 / post-v1) |
| 3–5 | `STORAGE_CLASS` | 0 none, 1 sliding window, 2 medium, 3 full cache |
| 6 | `PERSISTENT` | persistent storage granted |

#### 2.1.1 Dynamic codec compatibility gate

Structured `Caps` let proposers choose a sensible pipeline (`REMUX` vs
`TRANSCODE_AHEAD` vs `WEBM`) before building a proposal, without forcing peers
to enumerate every H.264 profile/level string in `HELLO`.

When a `PROPOSAL(PLAY_VIDEO)` or `QUEUE_ADD` arrives carrying the exact
container/codec MIME string (`StreamParams.mime`, e.g.
`video/mp4; codecs="avc1.640028,mp4a.40.2"`), each peer runs its local live
compatibility check (`MediaSource.isTypeSupported(mime)` in browser/MSE mode,
or mpv decoder check) and MAY broadcast `PROPOSAL_COMPAT { proposal_id,
playable, reason }`.

- A proposer MUST choose `FMP4` (H.264/AAC) unless every connected peer
  declares `WEBM` + `VP9` + `OPUS` in `Caps`.
- If any peer reports `playable: false` in `PROPOSAL_COMPAT`, the UI warns the
  room and the proposer may re-propose via `TRANSCODE_AHEAD` to baseline/high
  H.264 + AAC-LC.

---

## 3. Canonical hash preimages

Every hash uses a distinct v3 domain prefix:

```
peer_id       = BLAKE3("pw-peer-v3" || pubkey)
room_channel  = BLAKE3("pw-channel-v3" || room_id || room_secret)
alias         = HMAC-SHA256(room_secret, "pw-alias-v3" || peer_id)[0..16]
invite_channel= BLAKE3("pw-invite-v3" || room_id || invite_id)
invite_key    = HKDF-SHA256(secret, salt=room_id || invite_id, info="pw-invite-v3", L=32)
block_hash    = BLAKE3("pw-block-v3" || block_bytes)
segment_root  = merkle_root("pw-segment-v3", block_hashes, block_count)
manifest_hash = BLAKE3("pw-manifest-v3" || canonical(manifest_body))
chunk_hash    = BLAKE3("pw-manifest-chunk-v3" || canonical(chunk_body))
content_id    = BLAKE3("pw-content-v3" || stream_id || manifest_hash_final)
proposal_id   = BLAKE3("pw-proposal-v3" || canonical(proposal_body))
vote_id       = BLAKE3("pw-vote-v3" || canonical(vote_body))
lease_hash    = BLAKE3("pw-lease-v3" || canonical(lease_body))
cert_hash     = BLAKE3("pw-cert-v3" || canonical(cert_without_sig))
record_hash   = BLAKE3("pw-record-v3" || canonical(signed_record))
```

### 3.1 Merkle tree

- Leaves are 32-byte `block_hash` values for blocks `0..block_count` of one
  segment.
- Interior nodes hash `BLAKE3("pw-merkle-v3" || left || right)`.
- Odd leaf: promote unchanged to the next level.
- Single-block segment (`block_count == 1`): `segment_root = block_hashes[0]`,
  proof is empty `[]`.
- Proof for leaf `i` is the ordered sibling list bottom-up.

---

## 4. Identity, AUTH, and room membership

### 4.1 Keys

Every installation holds:

- Ed25519 signing keypair (`sk`, `pk`) → `PeerId`.
- X25519 encryption keypair (`esk`, `epk`) — used for invite acceptance and
  peer-to-peer signaling encryption (ephemeral `signal_pub` rotated per room
  session).

### 4.2 HELLO

```
HELLO = {
  version: uint,            // 3
  pubkey: PublicKey,
  enc_pubkey: EncPubKey,
  nonce: bytes .size 32,
  caps: Caps,
  max_message_size: uint,
}
```

### 4.3 AUTH transcript

For initiator `I` and responder `R` on a freshly opened `control` channel:

```
transcript =
  "pw-auth-v3"
  || room_id
  || pubkey_I || pubkey_R
  || enc_pubkey_I || enc_pubkey_R
  || nonce_I || nonce_R
  || dtls_fingerprint_I || dtls_fingerprint_R
  || canonical(caps_I) || canonical(caps_R)
  || version

auth_tag = HMAC-SHA256(room_secret, "pw-auth-tag-v3" || transcript)
auth_sig = Ed25519(sk, "pw-auth-sig-v3" || transcript || auth_tag)

AUTH = { auth_tag, auth_sig }
```

Verification rules:

1. Both sides MUST verify `auth_tag` against `room_secret` and `auth_sig`
   against the peer's `pubkey` from `HELLO`.
2. Both sides MUST verify that the peer's `pubkey` is **not** in the verified
   `KICK_CERT` banlist (`BYE(PW_BANNED)` if banned).
3. Both sides MUST verify that the peer is authorized in the room membership
   log (`genesis.creator_pubkey` or a valid `MEMBER_ADD` chain; a newly
   admitted peer presents its `MEMBER_ADD` inside its initial `SignalEnvelope`
   and in `STATE_SYNC`).
4. Role ordering (`I` before `R`) prevents reflection. DTLS fingerprints bind
   identity to the WebRTC transport.

### 4.4 Room secret and kick exclusion (no key rotation)

A room has one 32-byte `room_secret` generated at `ROOM_GENESIS` and delivered
confidentially to admitted members in `JOIN_ACCEPT`.

- **No epoch rotation and no relay lobby exist in v3.**
- All peer-to-peer traffic is encrypted with per-connection ephemeral DTLS, and
  all signaling envelopes are encrypted with per-message ephemeral X25519.
- When a `KICK` vote passes, every member records the verified `KICK_CERT` in
  its CRDT state, closes the connection with `BYE(PW_BANNED)`, ignores all
  further `SignalEnvelope`s from that peer, and rejects any future AUTH from
  its `pubkey`.
- See [identity.md](../design/identity.md) §3 and
  [design-log.md](../design/design-log.md) D29 for the rationale.

---

## 5. CRDT room state and authority objects

Durable room state is a **join-semilattice (set union + max)** of
self-verifying signed records, synchronized via `STATE_DIGEST` / `STATE_SYNC`.
There is no monolithic host-authored `ROOM_STATE` snapshot.

```
RoomGenesis = {
  room_id: RoomId,
  mode: RoomMode,               // DEMOCRATIC | COLLABORATIVE
  creator_pubkey: PublicKey,
  created_ms: uint,
  signature: Signature,
}

MemberAdd = {
  room_id: RoomId,
  invite_id: bytes .size 16,
  member_pubkey: PublicKey,
  member_enc_pubkey: EncPubKey,
  accepter_pubkey: PublicKey,   // must already be valid in membership log
  signature: Signature,
}

PeerSignalKey = {
  room_id: RoomId,
  peer_pubkey: PublicKey,
  signal_pub: EncPubKey,        // per-room session X25519 key
  seq: uint,
  signature: Signature,         // self-signed by peer_pubkey
}
```

State derivation rules (identical on every peer):

- **Admitted members** = `{ genesis.creator_pubkey } ∪ { m.member_pubkey |
  valid MemberAdd chain from genesis }`.
- **Banlist** = `{ k.target_pubkey | verified KickCertificate }`. Every peer
  gossips `KickCertificate` hashes in `STATE_DIGEST`; no Host can omit a ban.
- **Active roster** = currently connected, authenticated, non-banned members
  (a signed `BYE` prunes a departing peer immediately; ICE/consent timeout
  prunes a crashed peer).
- **Current Host** = the `HostLease` with the highest valid `term` (ties broken
  by lexicographically smallest `lease_hash`).
- **Shared Queue** (in `COLLABORATIVE` mode) = add/remove set of signed
  `QUEUE_ADD` and `QUEUE_REMOVE` events, ordered deterministically by
  `(lamport, entry_id)`.

### 5.1 HostLease

```
HostLease = {
  room_id: RoomId,
  term: uint,                       // 0 = no host; first lease = 1
  prev_lease_hash: Hash32 / null,   // null only for term 1
  stream_id: StreamId,
  host_pubkey: PublicKey,
  started_at_media_ms: uint,
  authority: VoteCertificate | TakeoverCertificate | QueueClaim,
  signature: Signature,             // signed by host_pubkey
}

QueueClaim = {
  entry_id: Hash32,                 // references a valid QUEUE_ADD in COLLABORATIVE mode
  prev_stream_ended: bool,
  roster_snapshot: [PublicKey],     // active roster at claim time
}
```

Receivers accept a `HostLease` only if:

1. `signature` verifies against `host_pubkey`, and `host_pubkey` is an
   admitted, non-banned member;
2. `authority` is valid (`VoteCertificate` in either mode,
   `TakeoverCertificate` after host loss, or `QueueClaim` in `COLLABORATIVE`
   mode referencing an unremoved `QUEUE_ADD` authored by `host_pubkey`);
3. `term > current_term`, or `term == current_term` with strictly higher
   tie-break rank (more valid votes/acks, then smallest `lease_hash`);
4. `prev_lease_hash` chains to the prior accepted lease (or null for term 1).

Host-only messages (`SYNC`, `START_AT`, `MANIFEST_HEAD`, `MANIFEST_APPEND`,
`MANIFEST_FINAL`) MUST match the current `HostLease`'s `(stream_id, term)` and
`host_pubkey`, with strictly increasing `seq`.

### 5.2 Proposals and VoteCertificate

```
Proposal = {
  proposal_id: Hash32,
  body: {
    room_id: RoomId,
    kind: PLAY_VIDEO | KICK,
    payload: PlayVideoPayload | KickPayload,
    roster_snapshot: [PublicKey],   // frozen active roster at proposal time
    opens_at_ms: uint,
    closes_at_ms: uint,
  },
  proposer_pubkey: PublicKey,
  signature: Signature,
}

Vote = {
  vote_id: Hash32,
  proposal_id: Hash32,
  approve: bool,
  voter_pubkey: PublicKey,
  created_at_ms: uint,
  signature: Signature,
}

VoteCertificate = {
  proposal_id: Hash32,
  roster_snapshot: [PublicKey],
  votes: [Vote],
  result: PASS | FAIL,
  assembler_pubkey: PublicKey,
  signature: Signature,
}
```

Validity rules:

- Every vote signature is valid, `vote.proposal_id == proposal_id`, and
  `voter_pubkey ∈ roster_snapshot`.
- **No duplicate or conflicting voter**: a certificate containing two votes
  from the same `voter_pubkey` is **invalid**. If a peer observes two different
  valid signed votes from the same key for the same `proposal_id` across
  messages, that voter's votes for this proposal are voided and the peer
  records an authority strike.
- `result == PASS` iff `approvals > |roster_snapshot| / 2` (strict majority).
  `result` MUST match the recomputed tally.
- If multiple valid certificates exist for one proposal, prefer the one with
  the most valid votes, tie-breaking by lexicographically smallest `cert_hash`.

### 5.3 TakeoverCertificate (2-step, causality-safe)

```
TakeoverClaim = {
  room_id: RoomId,
  stream_id: StreamId,
  from_term: uint,
  to_term: uint,                    // must equal from_term + 1
  candidate_pubkey: PublicKey,
  t_media_ms: uint,
  roster_snapshot: [PublicKey],     // active roster excluding lost host
  signature: Signature,
}

TakeoverAck = {
  claim_hash: Hash32,               // BLAKE3("pw-record-v3" || canonical(TakeoverClaim))
  signer_pubkey: PublicKey,
  signature: Signature,
}

TakeoverCertificate = {
  claim: TakeoverClaim,
  acks: [TakeoverAck],              // strict majority of claim.roster_snapshot
}
```

Challenge protocol (normative):

1. When the Host disconnects (`IceFailed` / `ConsentExpired`), a candidate
   holding segments at and ahead of the room's last known `t_media_ms`
   broadcasts `TAKEOVER_CLAIM`.
2. Any peer in `roster_snapshot` MAY send `CHALLENGE { stream_id, nonce,
   blocks: [{ seg, block }] }` naming up to 4 blocks inside `[0, frontier]`
   that the candidate advertises in `AVAILABILITY`. (A peer that has already
   verified recent frontier blocks directly from the candidate MAY skip
   challenging and sign `TAKEOVER_ACK` immediately.)
3. Each peer MAY issue at most 1 `CHALLENGE` per 10 s and at most 8 per
   candidate per term. Out-of-region challenges MUST be ignored.
4. The candidate answers with `CHALLENGE_RESPONSE` messages on `data` (one
   block + Merkle proof each) within 5 s.
5. Once satisfied, the peer signs and broadcasts `TAKEOVER_ACK`.
6. Once the candidate collects valid `TAKEOVER_ACK`s from a strict majority of
   `roster_snapshot` (no duplicate signers), it publishes `HOST_LEASE(to_term)`
   carrying `TakeoverCertificate`.

### 5.4 BanEntry and KICK

```
BanEntry = {
  target_pubkey: PublicKey,
  certificate: VoteCertificate,     // passed KICK proposal
}
```

A room-wide ban takes effect only with a verified `KickCertificate` (a passed
`VoteCertificate` whose proposal kind is `KICK`). Local bans for cryptographic
offenses (bad block Merkle proof, forged signature) take effect immediately on
the witnessing peer without waiting for a vote.

---

## 6. Manifest and 2-level content model

### 6.1 Granularity

PeerWatch v3 uses a **2-level** data hierarchy — segments align directly with
playback and integrity:

| Level | Size | Role |
|-------|------|------|
| **Segment** | ≈ 4 s (keyframe-aligned, variable byte length) | **Playback, integrity, and availability unit.** Segment `0` is the init segment; `1..=N` are media segments. |
| **Block** | ≤ 32 KiB (32,768 B) | **Request, transfer, and Merkle leaf unit.** Addressed by `(seg, block)`. |

- Each segment `s` has exact `byte_len` and
  `block_count = ceil(byte_len / 32768)`.
- Blocks `0..block_count-1` have length `32768`; the final block of a segment
  has length `byte_len - (block_count - 1) * 32768` (in `1..=32768`).
- A `BLOCK` with the wrong length for `(seg, block)` is a protocol violation.

### 6.2 StreamParams and SubtitleTrack

```
StreamParams = {
  profile: FMP4 | WEBM,
  mime: tstr .size (1..160),        // exact MSE/mpv MIME+codecs string
  block_length: 32768,
  target_segment_ms: 4000,
  audio_lang: tstr / null,
}

SubtitleTrack = {
  id: uint,
  lang: tstr .size (1..16),
  label: tstr .size (1..64),
  format: WEBVTT,
  byte_len: uint,
  hash: Hash32,                     // BLAKE3("pw-sub-v3" || bytes)
  bytes: bytes / null,              // inline if <= 16 KiB, else fetched via bulk
}
```

### 6.3 ManifestHead

```
ManifestHead = {
  stream_id: StreamId,
  term: uint,
  seq: uint,
  version: uint,
  manifest_hash: Hash32,
  params: StreamParams,
  segment_count: uint,              // includes segment 0 (init)
  total_bytes: uint / null,         // null while live-transcoding
  duration_ms: uint / null,         // null while live-transcoding
  init_segment: SegmentEntry,       // segment index 0 (ftyp+moov / EBML header)
  subtitles: [SubtitleTrack],       // optional WebVTT sidecars (<= 16 tracks)
  chunk_hashes: [Hash32],           // 32 KiB chunks covering segments 1..N
  chunk_count: uint,
}
```

- `chunk_count` MUST equal `len(chunk_hashes)`.
- A 2-hour movie (~1,800 segments × ~56 B) has a ~100 KiB manifest (~4 chunks
  total), regardless of whether the video is 2 Mbps SD or 100 GB 4K.

### 6.4 ManifestChunk and SegmentEntry

```
SegmentEntry = {
  seg_index: uint,                  // 0 = init, 1.. = media segments
  t_start_ms: uint,
  t_end_ms: uint,
  byte_len: uint,                   // exact segment length in bytes
  segment_root: Hash32,             // BLAKE3 Merkle root over segment blocks
}

ManifestChunk = {
  stream_id: StreamId,
  version: uint,
  chunk_index: uint,
  segments: [SegmentEntry],         // <= 512 entries per 32 KiB chunk
}
```

Every segment begins at byte `0` of its own byte range and ends at `byte_len`,
eliminating cross-segment coupling and allowing zero-scan MSE `appendBuffer()`
as well as prefix-sum virtual offsets for mpv HTTP Range requests.

### 6.5 Live append, seekable transcode, and finalization

- `stream_id` is fixed at proposal/queue time and never changes.
- `MANIFEST_APPEND`:
  ```
  MANIFEST_APPEND = {
    stream_id, term, seq,
    from_version: uint,
    to_version: uint,               // must equal from_version + 1
    new_segments: [SegmentEntry],   // <= 256 entries; may be sparse after host seek
    new_chunk_hashes: [Hash32],
    new_manifest_hash: Hash32,
    new_segment_count: uint,
  }
  ```
  Because segments carry explicit `seg_index` and timestamps, a live-transcoding
  Host that seeks past the encoded frontier can restart ffmpeg at the target
  timestamp and publish those segments immediately; unencoded middle ranges
  remain absent until filled or skipped.
- `MANIFEST_FINAL`:
  ```
  MANIFEST_FINAL = {
    stream_id, term, seq,
    content_id: Hash32,             // BLAKE3("pw-content-v3" || stream_id || final_manifest_hash)
    final_manifest_hash: Hash32,
    final_segment_count: uint,
    final_total_bytes: uint,
    final_duration_ms: uint,
  }
  ```

### 6.6 Block verification and local blame

```
BLOCK = {
  stream_id: StreamId,
  seg: uint,
  block: uint,
  bytes: bstr,                      // <= 32768 bytes
  proof: [Hash32],                  // Merkle path to segments[seg].segment_root
}
```

1. Receiver checks `bytes.len()` matches the exact expected length for
   `(seg, block)` from `SegmentEntry.byte_len`.
2. Receiver computes `BLAKE3("pw-block-v3" || bytes)` and verifies `proof`
   against `segment_root` from a verified manifest chunk/append.
3. On mismatch: receiver drops the block and **instantly locally bans** the
   authenticated peer whose connection delivered it.
4. Receivers MUST NOT accept a block whose `segment_root` is not yet known;
   they fetch the missing manifest chunk first.
5. Receivers MUST NEVER upload an unverified block. Once verified in RAM, a
   block MAY be served to peers immediately without waiting for disk `fdatasync`
   (see [storage.md](../design/storage.md) §1.3).

### 6.7 Compact availability encoding (`AVAILABILITY`)

```
Bitmap = {
  encoding: RAW | RLE,
  total_bits: uint,                 // max segment index + 1
  data: bytes,                      // RAW: ceil(total_bits/8) bytes;
                                    // RLE: packed (start: u32, len: u32) runs
}

FrontierMask = {
  seg: uint,
  block_mask: bytes,                // compact bitset of verified blocks in `seg`
}

AVAILABILITY = {
  stream_id: StreamId,
  version: uint,
  segments: Bitmap,                 // completed segments
  frontier: [FrontierMask],         // <= 8 in-progress segments near playhead
}
```

- Senders pick whichever of `RAW` or `RLE` is smaller (for a 2-hour movie:
  8 bytes `RLE` for a complete seeder, ≤ 226 bytes `RAW` worst case).
- `frontier` lets peers advertise verified 32 KiB blocks of an in-progress
  urgent segment immediately for sub-second Host fan-out.
- An `AVAILABILITY` referencing segments beyond the receiver's manifest
  `segment_count` MUST be buffered as a manifest-version signal (triggering a
  manifest resync), never scored as a strike unless it remains invalid after
  resync.

---

## 7. Message catalog

`✍` means the envelope MUST carry a valid `sig` and `author`. `✍(host)` means
the sender MUST also be the current `HostLease` holder for `(stream_id, term)`.

> The tables in §7.1–§7.7 are **generated** from
> [`schema/messages.yaml`](../schema/messages.yaml) by
> `scripts/gen_catalog.py`. Do not edit them by hand.

### 7.1 Identity and transport

<!-- CATALOG:7.1 -->
| ID | Type | ✍ | Payload | Channel | When |
|---:|------|---|---------|---------|------|
| 0x01 | `HELLO` | — | { version, pubkey, enc_pubkey, nonce, caps, max_message_size } | control | first message each way on a new control channel; every field is bound by AUTH |
| 0x02 | `AUTH` | — | { auth_tag, auth_sig } per §4.3 | control | answers HELLO; single transcript-bound signature + room_secret MAC |
| 0x03 | `PING` | — | { t0 } | control | every 5 s per connection |
| 0x04 | `PONG` | — | { t0 } | control | echoes PING; feeds the windowed-min RTT estimator |
| 0x05 | `BYE` | ✍ | { code, reason } | control | clean exit (prunes active roster) or rejection; codes in errors.md |
<!-- /CATALOG:7.1 -->

### 7.2 Room state and presence

<!-- CATALOG:7.2 -->
| ID | Type | ✍ | Payload | Channel | When |
|---:|------|---|---------|---------|------|
| 0x10 | `STATE_DIGEST` | — | { member_hashes, kick_hashes, lease_term, lease_hash, proposal_ids, queue_head, signal_key_seqs } | control | after AUTH and periodically on change; reconciles CRDT room state |
| 0x11 | `STATE_SYNC` | — | { genesis?, member_adds, kick_certs, host_lease?, proposals, queue_events, signal_keys } | bulk | sends self-verifying signed records missing from a peer's STATE_DIGEST |
| 0x12 | `HOST_LEASE` | ✍(host) | HostLease (§5.1) | control | broadcast on vote pass, takeover, or collaborative queue advance |
| 0x13 | `PEER_SIGNAL_KEY` | ✍ | { room_id, signal_pub, seq } | control | self-signed signaling key announcement; gossiped in room state |
| 0x14 | `PRESENCE` | — | { status, buffered_ms } | control | on change; liveness and buffer depth only, no media position |
<!-- /CATALOG:7.2 -->

### 7.3 Manifest

<!-- CATALOG:7.3 -->
| ID | Type | ✍ | Payload | Channel | When |
|---:|------|---|---------|---------|------|
| 0x18 | `MANIFEST_HEAD` | ✍(host) | ManifestHead (§6.3) | bulk | on stream activation and on demand |
| 0x19 | `MANIFEST_CHUNK` | — | { stream_id, version, chunk_index, chunk_hash, bytes } | bulk | snapshot transfer (~100 KB total for 2 h movie); designated responder only |
| 0x1A | `MANIFEST_GET` | — | { stream_id, version, chunk_indices, responder } | bulk | request missing chunks; `responder` prevents N-way amplification |
| 0x1B | `MANIFEST_APPEND` | ✍(host) | §6.5 | bulk | live-transcode segment growth (contiguous or post-seek) |
| 0x1C | `MANIFEST_FINAL` | ✍(host) | §6.5 | bulk | stream complete; assigns final content_id |
<!-- /CATALOG:7.3 -->

### 7.4 Swarm data

<!-- CATALOG:7.4 -->
| ID | Type | ✍ | Payload | Channel | When |
|---:|------|---|---------|---------|------|
| 0x20 | `AVAILABILITY` | — | { stream_id, version, segments: Bitmap, frontier: [{ seg, block_mask }] } | control | every 1 s to every peer; compact raw/RLE segment bitmap + active frontier block masks |
| 0x21 | `REQUEST` | — | { stream_id, want: [{ seg, block_indices }] } | data | batched block request; total requested blocks <= Q |
| 0x22 | `BLOCK` | — | { stream_id, seg, block, bytes, proof } | data | one <=32 KiB block plus its Merkle path to segment_root |
| 0x23 | `CANCEL` | — | { stream_id, want: [{ seg, block_indices }] } | data | kills queued requests only; in-flight blocks are stored anyway |
<!-- /CATALOG:7.4 -->

### 7.5 Sync, collaborative control, and host lifecycle

<!-- CATALOG:7.5 -->
| ID | Type | ✍ | Payload | Channel | When |
|---:|------|---|---------|---------|------|
| 0x28 | `SYNC` | ✍(host) | { stream_id, term, seq, t_media_ms, state, rate } | control | every 1 s and immediately on any transport event |
| 0x29 | `START_AT` | ✍(host) | { stream_id, term, seq, t_media_ms } | control | after the buffering gate |
| 0x2A | `STALL_NOTICE` | ✍ | { stream_id, term, seq, t_media_ms, reason } | control | local starvation; informs the room and the scheduler |
| 0x2B | `TAKEOVER_CLAIM` | ✍ | TakeoverClaim (§5.3) | control | a candidate claims the next term after host loss |
| 0x2C | `TAKEOVER_ACK` | ✍ | TakeoverAck (§5.3) | control | peer acknowledges a claim after verifying possession |
| 0x2D | `CHALLENGE` | — | { stream_id, nonce, blocks: [{ seg, block }] } | control | optional proof-of-possession check during takeover; must be in-region |
| 0x2E | `CHALLENGE_RESPONSE` | — | { stream_id, nonce, index, block } | data | one block + Merkle proof per message; sent only to the challenge issuer |
| 0x2F | `READY` | — | { stream_id, term, buffered_ms } | control | buffering-gate report |
| 0x35 | `CONTROL_REQ` | ✍ | { stream_id, term, action: Play \| Pause \| Seek(t_ms) \| SetRate(rate) } | control | Collaborative mode only: member asks current Host to update room transport |
<!-- /CATALOG:7.5 -->

`SYNC`/`START_AT` carry no Host wall clock; followers anchor on their own
monotonic receive clock ([sync.md](../design/sync.md) §2). `CONTROL_REQ` is
valid only when `genesis.mode == COLLABORATIVE`; the Host validates it, updates
local playback, and immediately emits the resulting `SYNC`.

### 7.6 Governance, queue, and social

<!-- CATALOG:7.6 -->
| ID | Type | ✍ | Payload | Channel | When |
|---:|------|---|---------|---------|------|
| 0x30 | `PROPOSAL` | ✍ | Proposal (§5.2) | control | broadcast PLAY_VIDEO or KICK proposal |
| 0x31 | `VOTE` | ✍ | Vote (§5.2) | control | broadcast; non-changeable, equivocation is fatal |
| 0x32 | `VOTE_CERT` | ✍ | VoteCertificate (§5.2) | control | publishes frozen active-roster majority tally |
| 0x33 | `KICK_NOTICE` | ✍ | { ban: BanEntry } | control | carries a verified KickCertificate |
| 0x34 | `PROPOSAL_COMPAT` | — | { proposal_id, playable: bool, reason? } | control | peer reports live MediaSource.isTypeSupported result for a proposal's codec |
| 0x36 | `QUEUE_ADD` | ✍ | { entry_id, stream_id, manifest_head, pipeline, lamport } | control | Collaborative mode: enqueue a video to the shared playlist |
| 0x37 | `QUEUE_REMOVE` | ✍ | { entry_id, lamport } | control | Collaborative mode: remove an entry (by its proposer or after playback) |
| 0x38 | `CHAT` | ✍ | { msg_id, ts_ms, lamport, body } | control | broadcast; dedup by msg_id; order by (lamport, ts_ms, peer_id) |
<!-- /CATALOG:7.6 -->

### 7.7 Reserved

<!-- CATALOG:7.7 -->
| Range | Use |
|-------|-----|
| 0x40-0x4F | voice control and media tracks (v2 / post-v1) |
| 0xF0-0xFF | experimental; ignored by default |
<!-- /CATALOG:7.7 -->

---

## 8. Validation and replay rules

Every receiver validates incoming messages in strict order:

1. **Envelope**: version (`3`), size, CBOR depth/limits, `room` match.
2. **Handshake gate**: before `Established`, only `HELLO` and `AUTH` are
   accepted.
3. **Signature**: if signed, `author` is a non-banned member and `sig`
   verifies over canonical bytes.
4. **Replay**: `seq` strictly exceeds the highest accepted value for
   `(author, stream, type, term)`; `CHAT` dedups by `msg_id`.
5. **Authority**:
   - host-only types (`SYNC`, `START_AT`, `MANIFEST_HEAD`, `MANIFEST_APPEND`,
     `MANIFEST_FINAL`) MUST match current `HostLease`;
   - `HOST_LEASE` MUST satisfy §5.1;
   - `KICK_NOTICE` MUST carry a verified `KickCertificate`;
   - `CONTROL_REQ`, `QUEUE_ADD`, `QUEUE_REMOVE` MUST only be accepted when
     `genesis.mode == COLLABORATIVE`.
6. **Content**: `BLOCK` verifies against a known `segment_root` (§6.6);
   `MANIFEST_CHUNK` verifies against `chunk_hashes`.
7. **Limits**: enforce per-peer token buckets (§9).

---

## 9. Limits (normative)

| Limit | Value | On excess |
|-------|------:|-----------|
| Envelope | ≤ 64 KiB (negotiated 48–256 KiB) | close + strike |
| CBOR depth / collection length | 16 / 4096 | strike |
| Control messages | ≤ 64/s and ≤ 128 KiB/s per peer | throttle → strike |
| Data bytes | ≤ 8 MiB/s burst, ≤ 4 MiB/s sustained per peer | throttle |
| Bulk/manifest bytes | ≤ 1 MiB/s per peer | throttle |
| `REQUEST` / `CANCEL` total blocks | ≤ `Q` blocks per message (`Q ≤ 64`) | ignore message + strike |
| `AVAILABILITY` payload | ≤ 4 KiB, ≤ 1/s, ≤ 8 frontier entries | strike |
| `CHAT` body | ≤ 2 KiB | truncate + strike on repeat |
| `MANIFEST_CHUNK` | ≤ 32 KiB | strike |
| `Subtitles` per stream | ≤ 16 tracks, ≤ 2 MiB per track | reject manifest |
| Active proposals per peer | 1 | drop excess |
| `CHALLENGE` blocks / rate | ≤ 4 blocks, ≤ 1 per 10 s, ≤ 8/candidate/term | ignore excess |
| Room roster ceiling | ≤ 16 active peers (hard cap 32 in membership log) | `PW_ROOM_FULL` |

### 9.1 Media parser limits

Enforced **before** allocation on all untrusted media bytes:

| Limit | Value | On excess |
|-------|------:|-----------|
| fMP4 box size (`ftyp`/`moov`/`moof`/`mdat`) | ≤ 16 MiB | reject stream |
| Init segment (`ftyp` + `moov`) total | ≤ 4 MiB | reject stream |
| Media segment (`moof` + `mdat`) total | ≤ 32 MiB | reject stream |
| Box / EBML nesting depth | ≤ 8 | reject stream |
| `moov` sample-table entries | ≤ 1,000,000 | reject stream |
| EBML cluster size (WebM) | ≤ 32 MiB | reject stream |
| Transcode read-ahead buffer | ≤ 120 s of media | throttle pipe |
| ffmpeg/ffprobe stderr retained | ≤ 64 KiB ring | discard oldest |
| ffprobe JSON output / wall clock | ≤ 1 MiB / ≤ 30 s | kill + reject |

---

## 10. Versioning and compatibility

- `v` is the major protocol version (`3`). Mismatched major versions receive
  `BYE(PW_VERSION_MISMATCH)`.
- Minor additions use optional CBOR map keys (ignored by older decoders) or
  message types with `flags & 2 != 0` (optional). An unknown required type
  triggers `BYE(PW_UNSUPPORTED_MESSAGE)`.
- Existing field semantics MUST NOT change in place.

---

## 11. Stateless signaling relay protocol

The relay (`pw-relay`) is a **100% stateless in-memory WebSocket mailbox**. It
stores zero disk state and zero lobby history.

### 11.1 Relay messages

```
RelaySubscribe = {
  channel: bytes .size 32,      // room_channel or invite_channel
  alias: bytes .size 16,        // per-room alias (or random on invite channel)
}

RelayRoster = {
  aliases: [ bytes .size 16 ],  // online aliases on this channel (presence hint only)
}

RelaySignal = {
  to: bytes .size 16,
  envelope: bytes,              // opaque SignalEnvelope or InviteEnvelope
}

RelayLeave = {}
RelayError = { code: u16, message: tstr }
```

The relay does **not** carry `signal_pub` in `RelayRoster`. Peers obtain
authenticated `signal_pub` values exclusively from self-signed
`PEER_SIGNAL_KEY` records or from the signed plaintext of an initial
`SignalEnvelope`.

### 11.2 SignalEnvelope (deadlock-free)

```
SignalEnvelope = {
  v: uint,                      // 3
  from_alias: bytes .size 16,
  to_alias: bytes .size 16,
  seq: uint,
  eph_pub: EncPubKey,           // per-message ephemeral X25519 key
  nonce: bytes .size 24,
  ciphertext: bytes,            // XChaCha20-Poly1305 AEAD
  sig: Signature,
}

SignalPlaintext = {
  from_pubkey: PublicKey,
  room_id: RoomId,
  sender_signal_pub: EncPubKey, // allows immediate encrypted reply without ROOM_STATE
  member_add: MemberAdd / null, // included by new joiner on first offer
  payload: bytes,               // SDP offer/answer or ICE candidates
}
```

- `key = HKDF-SHA256(X25519(eph_sk, to_signal_pub), salt=room_channel, info="pw-signal-v3", L=32)`.
- `ciphertext = XChaCha20-Poly1305(key, nonce, canonical(SignalPlaintext), aad=canonical(header))`.
- `sig = Ed25519(from_sk, "pw-signal-sig-v3" || canonical(header) || ciphertext)`.
- On receipt: decrypt, verify `sig` against `from_pubkey`, verify
  `from_alias == alias(room_secret, peer_id(from_pubkey))`, verify `from_pubkey`
  is not in `KICK_CERT` banlist, verify membership (already known OR valid
  `member_add` chained to an existing member), and check monotonic `seq` per
  `from_alias`.
- The recipient immediately caches `sender_signal_pub` (authenticated by
  `from_pubkey`'s signature) and uses it to encrypt the SDP answer back — zero
  deadlock on join and zero trust in the relay.

### 11.3 Invite bootstrap (zero cleartext room metadata)

```
InviteToken   = "pw3_" || base32(room_id) || "." || base32(invite_id) || "." || base64url(secret)
InviteChannel = BLAKE3("pw-invite-v3" || room_id || invite_id)

InviteGrant = {
  room_id: RoomId,
  invite_id: bytes .size 16,
  inviter_pubkey: PublicKey,
  expiry_ms: uint,
  max_uses: uint,
  invitee_pubkey: PublicKey / null,
  signature: Signature,
}

InviteEnvelope = {
  v: uint,                      // 3
  nonce: bytes .size 24,
  ciphertext: bytes,            // XChaCha20-Poly1305(invite_key, nonce, canonical(JoinRequest), aad=v||invite_channel)
}

JoinRequest = {
  room_id: RoomId,
  invite_id: bytes .size 16,
  pubkey: PublicKey,
  enc_pubkey: EncPubKey,
  join_pub: EncPubKey,          // ephemeral X25519 key for JOIN_ACCEPT
  nonce: bytes .size 32,
  proof: bytes .size 32,        // HMAC-SHA256(invite_key, "pw-join-v3" || room_id || invite_id || pubkey || join_pub || nonce)
}

JoinAcceptEnvelope = {
  v: uint,                      // 3
  eph_pub: EncPubKey,
  nonce: bytes .size 24,
  ciphertext: bytes,            // XChaCha20-Poly1305(HKDF(X25519(eph_sk, join_pub)), canonical(JoinAccept))
}

JoinAccept = {
  room_id: RoomId,
  room_secret: bytes .size 32,
  genesis: RoomGenesis,
  member_adds: [MemberAdd],     // includes the newly minted MemberAdd for the joiner
  kick_certs: [BanEntry],
  signal_keys: [PeerSignalKey],
  host_lease: HostLease / null,
  accepter_pubkey: PublicKey,
  signature: Signature,
}
```

- `InviteEnvelope` exposes **neither** `room_id` nor `invite_id` in cleartext;
  the relay routes solely by opaque `InviteChannel`.
- The inviter (or member holding the `InviteGrant`) verifies expiry, usage
  budget, optional `invitee_pubkey` binding, and `proof`, mints a signed
  `MemberAdd`, and replies with `JoinAcceptEnvelope`.
- The joiner verifies `genesis`, the `MemberAdd` chain, and `accepter_pubkey`'s
  signature, then subscribes to `room_channel` and connects directly to peers
  via `SignalEnvelope`.

### 11.4 Relay limits

- Max `SignalEnvelope` / `InviteEnvelope` / `JoinAcceptEnvelope` size: **32 KiB**.
- Max signaling throughput per alias: **64 KiB/s**.
- Idle timeout: **90 s** (clients ping every 30 s).
- Per-IP connection and channel quotas are operator-configurable.
- Zero disk persistence and zero message retention once disconnected.

---

## 12. Explicit scope boundaries

- The relay is untrusted for confidentiality and integrity, but it can still
  deny service, omit online aliases, and observe client IPs.
- Kicked peers are excluded by the verified `KICK_CERT` banlist at signaling
  and AUTH, not by key rotation (§4.4).
- A peer ID proves continuity of a local keypair, not real-world identity.
- PeerWatch does not provide anonymity against a global traffic observer.
