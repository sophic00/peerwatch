---
tier: normative
---

# Protocol Schema (normative)

This document is the **normative** definition of PeerWatch's wire objects,
encoding, authority model, and validation rules. Other documents explain the
*why* and the subsystem behaviour; when they disagree with this file, this
file wins (see the precedence ladder in [README](../README.md)).

The protocol version is **2**. There is no deployed v2 implementation, so
the rules below are not a compatibility break. Any future deployed version
must keep the versioning rules in §10.

RFC 2119 keywords (**MUST**, **MUST NOT**, **SHOULD**, **MAY**) are used
normatively throughout this document and in every file under `docs/spec/`.

---

## 1. Encoding and framing

### 1.1 Serialization

- All payloads are **deterministic CBOR** (RFC 8949 §4.2): definite lengths,
  shortest-form integers, and map keys sorted by their encoded bytes.
- Hashes of protocol objects are computed over the **canonical CBOR** of the
  object, prefixed with a domain string (see §3.4). Never hash a
  non-canonical serializer output.
- Unknown CBOR map keys are ignored. Unknown message types are handled per
  §10.
- Decoders MUST enforce a maximum nesting depth (16), maximum map/array
  length, and maximum string/byte-string length before allocating.

### 1.2 Transport channels

WebRTC DataChannels are message-oriented: one envelope = one DataChannel
message.

| Channel | SCTP mode | Traffic |
|---------|-----------|---------|
| `control` | reliable, ordered | handshake, room state, governance, presence, chat, sync |
| `bulk` | reliable, ordered | manifest head/chunks/appends, large metadata |
| `data` | reliable, unordered | `REQUEST`, `BLOCK`, `CANCEL` |

A manifest must never block `SYNC`/votes behind it, which is why manifest
traffic has its own `bulk` channel.

### 1.3 Envelope

```
Envelope = {
  v:       uint,              // 2
  t:       uint,              // message type
  flags:   uint,              // bit 0 signed, bit 1 optional, bit 2 bulk
  room:    bytes .size 16,    // room_id
  epoch:   uint,              // room key epoch
  stream:  bytes .size 16 / null,
  author:  bytes .size 32 / null,  // Ed25519 public key, required if signed
  seq:     uint / null,       // per-(author, stream, type) replay counter
  payload: bytes,             // canonical CBOR of the per-type struct
  sig:     bytes .size 64 / null,
}
```

- `room` and `epoch` bind the message to one room-key generation. A peer
  that has not accepted the current epoch rejects the message.
- `author` is required for every signed message and for every message that
  can be gossiped. It is redundant with the connection identity, but it
  makes forwarded messages verifiable.
- `seq` is required for signed messages that can be replayed (`SYNC`,
  `START_AT`, `MANIFEST_APPEND`, `CHAT`, `TAKEOVER_*`). Receivers keep the
  highest accepted `seq` per `(author, stream, type, term)` and reject
  stale or duplicate values.
- `sig` is computed over:
  ```
  "pw-envelope-v2" || canonical(Envelope without sig)
  ```
  and MUST verify against `author`.
- `AUTH` is the one exception: it carries **two** signatures. The envelope
  `sig` is the generic envelope signature above; the payload field
  `auth_sig` is the transcript-bound signature defined in §4.3. A receiver
  MUST verify both. `auth_sig` is the one that proves possession of the
  identity key over the DTLS binding; the envelope `sig` is redundant for
  AUTH but is retained so the envelope code path is uniform.

### 1.4 Message size

- Default maximum envelope: **64 KiB**.
- `HELLO` advertises `max_message_size`; the usable value is
  `min(local, remote)`. The supported range is **48 KiB–256 KiB**. A peer
  that cannot support 48 KiB cannot join the data plane.
- `BLOCK` carries a 32 KiB block plus a Merkle proof and stays below 64 KiB.
- `MANIFEST_CHUNK` payload is at most 32 KiB.
- A 1 MiB envelope is not supported. Message-count limits without byte
  budgets are not limits (§9).

### 1.5 Integrity layers

| Layer | Protects | Mechanism |
|-------|----------|-----------|
| Transport | connection | DTLS/SCTP (WebRTC) |
| Identity | author | Ed25519 envelope signature |
| Authority | host/vote/kick/takeover | certificates + `term` (`§5`) |
| Content metadata | manifest | host signature + `manifest_hash` + chunk hashes |
| Content bytes | each block | block Merkle proof against `piece_root` (`§6`) |
| Availability | bitfields | no trust; only a scheduling hint |

A bitfield is **not** proof of possession. Takeover and ban decisions never
rely on an unsigned bitfield.

---

## 2. Identifiers and capabilities

```
PeerId     = base32(BLAKE3("pw-peer-v2" || ed25519_pubkey))   // 52 chars
StreamId   = 128-bit random
Hash32     = 32-byte BLAKE3 output
PublicKey  = 32-byte Ed25519 public key
EncPubKey  = 32-byte X25519 public key
Signature  = 64-byte Ed25519 signature
Term       = uint64, monotonically increasing host lease number
```

`PeerId` is displayed as `pw:<first 8 chars>…`. The full value is used on the
wire. A peer with a new key is a new peer.

### 2.1 Capabilities

`HELLO` and `ROOM_STATE` carry:

```
Caps = {
  flags: u64,               // NATIVE, LOW_UPLOAD, VOICE, ...
  storage_budget_mb: u32,   // 0 = memory-only / streaming-only
  codecs: [tstr],           // exact codec strings this peer can PLAY
}
```

Bit assignments:

| Bit | Flag | Meaning |
|----:|------|---------|
| 0 | `NATIVE` | native process (not a browser tab) |
| 1 | _reserved_ | was `WEBM_OK`; superseded by `codecs` (§2.1.1) |
| 2 | `LOW_UPLOAD` | metered/limited uplink |
| 3 | `VOICE` | voice tracks supported (M5) |
| 4–6 | `STORAGE_CLASS` | 0 none, 1 sliding window, 2 medium, 3 full archive |
| 7 | `PERSISTENT` | persistent storage permission granted |

`max_message_size` lives in `HELLO` only (§4.2). It is deliberately **not**
duplicated in `Caps`: `Caps` is replayed inside `ROOM_STATE` and a
per-connection value must not travel in a room-wide object.

#### 2.1.1 `codecs` is the capability gate

`codecs` is the set of codec strings the peer can **play**, using the exact
form MSE and mpv consume — for example `avc1.640028,mp4a.40.2` for fMP4 or
`vp09.00.10.08,opus` for WebM. It is not a profile list: two peers that both
support `FMP4` may disagree on H.264 High vs Baseline, and the protocol must
be able to tell.

Rules:

- The Host MUST NOT start a stream unless every connected peer's `codecs`
  contains `StreamParams.codecs` (§6.2) **verbatim**. Substring or prefix
  matching is a protocol violation: codec strings are opaque tokens.
- `Caps.codecs` is a capability statement, not a promise. A peer that lies
  about `codecs` strands itself; it MAY be dropped by its own player and
  MUST NOT be treated as a protocol violation.
- `codecs` length is limited (§9). The set is replayed inside `ROOM_STATE`,
  so it MUST fit one envelope together with the roster.

The consequence for the stream profile is derived, not declared: a stream is
`WEBM` iff `StreamParams.codecs` is a WebM codec string, and it is admissible
only if every peer listed it (see
[media-pipeline.md](../design/media-pipeline.md) §2.1).

---

## 3. Canonical hash preimages

Domain-separated preimages prevent cross-protocol confusion:

```
peer_id      = BLAKE3("pw-peer-v2" || pubkey)
channel_hash = BLAKE3("pw-channel-v2" || room_id || epoch)
alias        = HMAC-SHA256(room_psk, "pw-alias-v2" || peer_id || epoch)[0..16]
signal_pub   = X25519 public key, generated per (room_id, epoch), published
               in the host-signed ROOM_STATE roster (§7.2) — never taken
               from the relay (see §11.2)
manifest_hash= BLAKE3("pw-manifest-v2" || canonical(manifest_body))
chunk_hash   = BLAKE3("pw-manifest-chunk-v2" || canonical(chunk_body))
piece_root   = merkle_root("pw-piece-v2", block_hashes, block_count)
content_id   = BLAKE3("pw-content-v2" || stream_id || manifest_hash_final)
proposal_id  = BLAKE3("pw-proposal-v2" || canonical(proposal_body))
vote_id      = BLAKE3("pw-vote-v2" || canonical(vote_body))
lease_hash   = BLAKE3("pw-lease-v2" || canonical(lease_body))
certificate_hash = BLAKE3("pw-cert-v2" || canonical(cert_without_signature))
block_hash   = BLAKE3("pw-block-v2" || block_bytes)
invite_channel = BLAKE3("pw-invite-channel-v2" || room_id || invite_id)
```

`manifest_hash` covers the logical manifest: stream parameters, init
segment reference, the ordered piece roots, the segment entries,
`total_pieces`, and `total_blocks`. It does **not** depend on how the
manifest is chunked for transfer. `chunk_hashes` are transfer-integrity
hashes for the snapshot chunks; a peer verifies chunks against them, then
recomputes `manifest_hash` over the logical content. Display metadata
(title, description) is outside the hash.

### 3.1 Merkle tree

- Leaves are 32-byte hashes.
- Pair `H(left || right)` with the domain prefix `"pw-merkle-v2"`.
- Odd leaf: promote it unchanged to the next level (Bitcoin-style).
- The proof for leaf `i` is the sibling hash at each level, bottom-up.
- The receiver recomputes the root and compares it with the trusted root.

---

## 4. Identity and AUTH

### 4.1 Keys

Every installation has:

- Ed25519 signing keypair (`sk`, `pk`).
- X25519 encryption keypair (`esk`, `epk`).
- `PeerId = peer_id(pk)`.

The encryption key is separate from the signing key. It is used for
per-peer signaling envelopes and invite acceptance.

### 4.2 HELLO

```
HELLO = {
  version: uint,            // 2
  pubkey: PublicKey,
  enc_pubkey: EncPubKey,
  nonce: bytes .size 32,
  caps: Caps,
  max_message_size: uint,
}
```

`HELLO` is unsigned, but every field is included in the AUTH transcript. A
tampered `HELLO` makes AUTH fail.

### 4.3 AUTH transcript

For an initiator `I` and responder `R`:

```
transcript =
  "pw-auth-v2"
  || room_id || epoch || channel_hash
  || pubkey_I || pubkey_R
  || enc_pubkey_I || enc_pubkey_R
  || nonce_I || nonce_R
  || dtls_fingerprint_I || dtls_fingerprint_R
  || canonical(caps_I) || canonical(caps_R)
  || version

room_psk  = HKDF-SHA256(
              ikm  = room_key,
              salt = room_id || epoch,
              info = "pw-room-psk-v2",
              L    = 32)

auth_tag  = HMAC-SHA256(room_psk, "pw-auth-tag-v2" || transcript)
sig_I     = Ed25519(sk_I, "pw-auth-sig-v2" || transcript || auth_tag)
sig_R     = Ed25519(sk_R, "pw-auth-sig-v2" || transcript || auth_tag)

AUTH payload = { auth_tag, auth_sig }
```

Each side verifies the peer’s `auth_tag` and `auth_sig`. The AUTH envelope
also carries the normal envelope `sig`; `auth_sig` is the transcript-bound
signature, while the envelope `sig` is the generic message signature. The
role labels (`I`/`R`) prevent reflection. The DTLS fingerprints bind the
application identity to the transport. The room key epoch makes old-key
peers unauthenticatable after a rekey.

### 4.4 Room keys and epochs

- `room_key` is a 32-byte secret known to current members.
- `epoch` is a monotonically increasing key generation.
### 4.4 Room keys and epochs

- `room_key` is a 32-byte secret known to current members.
- `epoch` is a monotonically increasing key generation.
- The room relay channel is `channel_hash(room_id, epoch)`, so a kicked
  peer cannot subscribe to the new channel.

#### 4.4.1 Epoch keys are derived, never chosen

> **`room_key'` for `epoch+1` is a pure function of `room_key` and the
> rekey basis. No peer picks a random key.**

```
room_key(e+1) = HKDF-SHA256(
                  ikm  = room_key(e),
                  salt = room_id || (e+1),
                  info = "pw-rekey-v2" || rekey_basis_hash,
                  L    = 32)
```

`rekey_basis_hash` is `certificate_hash(KickCertificate)` for a kick rekey,
or `lease_hash(HostLease)` for a scheduled rotation. `rekey_basis_hash` is
carried in the `REKEY` payload so every receiver can recompute the same key.

This makes a rekey **convergent by construction**. Every honest member that
holds epoch `e` and the basis derives the identical epoch-`e+1` key, so
there is exactly one possible key per epoch and no tie-break is needed.
Without this rule, two members holding the same `KickCertificate` could each
generate a different key for the same epoch and fork the room into two
incompatible `epoch+1`s.

#### 4.4.2 Who may publish `REKEY`

`REKEY` is a **notification, not an authority**. Because the key is derived
(§4.4.1), any member that holds the basis can publish the same, identical
key. A `REKEY` is accepted only if all of the following hold:

1. `new_epoch == current_epoch + 1`;
2. the `basis` is a valid `KickCertificate` or a current `HostLease`;
3. the recomputed `room_key'` matches the `wrapped_keys` for this peer (a
   decrypt-and-compare check);
4. `wrapped_keys` decrypts to the value derived from the basis.

Rule 3 is the anti-fork check: a `REKEY` carrying a key that does not match
the derivation is a protocol violation, regardless of who sent it. Receivers
MUST NOT "first valid wins"; they MUST recompute.

`wrapped_keys` exists only to bootstrap peers whose X25519 keys are already
known. It is **not** the mechanism of record — §4.4.1 is.

#### 4.4.3 Offline members and the lobby channel

A member that is disconnected during a rekey cannot receive `wrapped_keys`.
Because the key is derived (§4.4.1) it can still *compute* the new key — but
only if it learns `rekey_basis_hash`. Two mechanisms cover that:

**Lobby channel.** A second, epoch-independent relay channel per room:

```
lobby_channel = BLAKE3("pw-lobby-v2" || room_id)
```

Every member subscribes to the lobby channel for the lifetime of its
membership, in addition to the current epoch channel. The lobby carries only
`LOBBY_BASIS` messages — never media, chat, votes, or keys:

```
LOBBY_BASIS = {
  room_id,
  epoch,                     // the epoch this basis produces
  basis_hash: Hash32,        // rekey_basis_hash from §4.4.1
  basis_kind: KICK | ROTATE,
  publisher_pubkey: PublicKey,
  signature: Signature,      // Ed25519 over canonical body
}
```

A rejoining peer that holds epoch `e` reads the lobby, learns
`rekey_basis_hash`, derives `room_key(e+1)`, and joins the epoch-`e+1`
channel. It never needs a wrapped key and it never needs to be online at the
moment of the kick.

**Catch-up chain.** If the peer is several epochs behind, the lobby carries
the `basis_hash` of every epoch since it left; a peer that missed `k` rekeys
applies them in order. `LOBBY_BASIS` messages are retained by the relay for
at most `lobby_retention` (default 24 h, §11.4) and are signed, so a
malicious relay cannot fabricate one.

**Why a kicked peer still cannot return.** The lobby is a *public* channel
derived from `room_id`, so the kicked peer can subscribe to it. That is
fine: it learns `basis_hash` but cannot derive `room_key(e+1)` because it
never held `room_key(e)` after the rotation... except that it *did* hold
`room_key(e)`. Therefore:

> **The kicked peer MUST be excluded by the basis, not only by the chain.**
> A `KICK` basis derives the next key from `room_key(e)` **and** the
> certificate hash. The kicked peer holds `room_key(e)` and can read
> `basis_hash` from the lobby, so it **can** derive `room_key(e+1)`.

The kick therefore relies on three independent barriers, and the spec must
state which one is load-bearing:

| Barrier | Defeats a kicked peer? |
|---|---|
| `room_key(e+1)` derivation | **No** — it holds `room_key(e)` |
| Relay channel rotation | **No** — `channel_hash` is computable from `room_id` + `epoch` |
| Banlist + AUTH rejection | **Yes** — the load-bearing barrier |

Consequence, stated plainly: **a kicked peer can derive the new room key.**
It is excluded because every member checks the banlist during AUTH and
refuses the connection. Key rotation is defence-in-depth against *passive*
capture of future traffic, not against the kicked peer itself. A future
revision that needs cryptographic exclusion must rotate to a key that is
*not* derivable from the previous one (fresh DH among remaining members),
accepting the loss of offline catch-up.

#### 4.4.4 Rekey transition window

Epoch changes are not atomic across the mesh. The rules:

- A peer MUST accept messages in epoch `e` **and** `e+1` for
  `rekey_grace` (default 30 s) after it first accepts `e+1`.
- A peer MUST stop originating epoch-`e` messages the moment it accepts
  `e+1`.
- A peer that receives an epoch-`e` message after `rekey_grace` MUST drop it
  and count a strike, but MUST NOT ban (the sender may be lagging, not
  hostile).
- A peer that fails to obtain `e+1` within `rekey_grace` MUST enter
  `REKEY_RECOVERING`, pause uploads and votes, and poll the lobby channel
  (§4.4.3). It MUST NOT continue to act on epoch-`e` state.
- `rekey_grace` is a local timer; there is no global rekey commit.

This replaces the previous "peers reject old-epoch messages" rule, which
ejected slow peers with no recovery path.

---

## 5. Authority objects

Signatures prove authorship. Authority is proven by a **certificate** and a
**term**. Every authoritative message is checked against the current lease,
not just against a valid signature.

### 5.1 HostLease

```
HostLease = {
  term: uint,                       // 0 = no host; first lease = 1
  prev_lease_hash: Hash32 / null,   // null for the first lease (term 1)
  stream_id: StreamId,
  host_peer_id: PeerId,
  host_pubkey: PublicKey,
  started_at_media_ms: uint,
  certificate: HostCertificate,
}
HostCertificate = VoteCertificate | TakeoverCertificate
```

The lease is signed by the host. Receivers accept a lease only if:

1. the certificate is valid (§5.2/§5.3);
2. `term` is greater than the currently accepted term;
3. `prev_lease_hash` matches the current lease (or is null for the first
   lease in a new room, term 1);
4. the certificate’s stream/roster is consistent with the lease;
5. if two valid leases have the same `term`, prefer the certificate with
   the most valid votes/acks; tie-break by the lexicographically smallest
   `certificate_hash`. The other lease is invalid. This is the
   deterministic tie-break for concurrent proposals/takeovers.

A host is authoritative only for the stream and term in its lease.
`SYNC`, `START_AT`, `MANIFEST_HEAD`, `MANIFEST_APPEND`, `MANIFEST_FINAL`,
and `TAKEOVER_*` MUST carry the same `(stream_id, term)` and a higher `seq`
than the last accepted message from that host. `MANIFEST_CHUNK`/
`MANIFEST_GET` may be served by any peer.

### 5.2 VoteCertificate

```
Proposal = {
  proposal_id: Hash32,          // hash of body
  body: {
    kind: PLAY_VIDEO | KICK,
    payload: PLAY_VIDEO_Payload | KICK_Payload,
    roster_snapshot: [RosterEntry],
    opens_at_ms: uint,
    closes_at_ms: uint,
  },
  proposer_pubkey: PublicKey,
  signature: Signature,
}

Vote = {
  vote_id: Hash32,              // BLAKE3("pw-vote-v2" || canonical(body))
  proposal_id: Hash32,
  approve: bool,
  voter_pubkey: PublicKey,
  created_at_ms: uint,
  signature: Signature,         // over body without vote_id/signature
}

VoteCertificate = {
  proposal_id: Hash32,
  roster_snapshot: [RosterEntry],
  votes: [Vote],
  result: PASS | FAIL,
  leader_pubkey: PublicKey,
  signature: Signature,
}
```

Validity rules:

- Every vote signature is valid and the voter is in `roster_snapshot`.
- `vote_id` MUST equal `BLAKE3("pw-vote-v2" || canonical(vote_body))` where
  `vote_body` is the vote without `vote_id` and `signature`.
- No voter appears twice. Votes are non-transferable and
  non-changeable, so two different votes from one key for one proposal is
  **provable equivocation**. The certificate MUST be rejected outright
  rather than resolved: a certificate containing two votes from the same
  voter is invalid, and the equivocator's key is reported via `BAN_REPORT`
  with both signed votes as evidence. Picking the lower `vote_id` (the
  earlier rule) let an equivocator decide which of its votes counted by
  grinding hashes, so it is removed.
- `result = PASS` iff `approvals > |roster_snapshot| / 2` (strict majority).
  A two-peer room therefore requires both votes.
- `result` MUST agree with the recomputed tally. A certificate whose
  `result` field disagrees with its own votes is invalid, regardless of
  signature.
- The leader signature is valid and the leader is in the roster.
- The certificate’s `roster_snapshot` equals the proposal’s snapshot.
- The certificate is the authoritative vote set. Timestamps are advisory
  for UI; consensus does not depend on wall-clock agreement.
- A peer that sent a vote and does not see it in a certificate MAY reject
  that certificate and gossip its vote. Any peer may assemble an
  alternative certificate from valid votes.
- If multiple valid certificates exist for one proposal, prefer the one
  with the most valid votes; tie-break by the lexicographically smallest
  `certificate_hash` (hash of the canonical certificate without its
  signature). Peers gossip certificates until they converge.

### 5.3 TakeoverCertificate

```
TakeoverClaim = {
  stream_id: StreamId,
  from_term: uint,
  to_term: uint,
  candidate: PeerId,
  candidate_pubkey: PublicKey,
  t_media_ms: uint,
  challenge_nonce: bytes .size 32,
  challenge_commitment: Hash32, // hash of nonce + challenge responses
}

TakeoverAck = {
  claim_hash: Hash32,
  signer_pubkey: PublicKey,
  signature: Signature,
}

TakeoverCertificate = {
  claim: TakeoverClaim,
  acks: [TakeoverAck],       // signed by a strict majority of the frozen roster
  roster_snapshot: [RosterEntry],
  result: PASS | FAIL,
}
```

Validity rules:

- `to_term = from_term + 1`.
- The candidate is in the frozen roster and has a complete store for the
  stream at the claimed `t_media_ms`.
- The candidate answered a `CHALLENGE` for pieces/blocks in the claimed
  region, with block Merkle proofs, within 5 s.

  **Challenge protocol (normative).** `CHALLENGE` is unsigned and may be
  sent by any peer, so the following rules are what stop it from being a
  griefing tool:

  1. The challenger MUST choose blocks that lie inside
     `[0, t_media_ms]` **and** inside the candidate's advertised complete
     range. A `CHALLENGE` naming a block outside that range MUST be
     ignored by the candidate.
  2. A `TAKEOVER_ACK` signer MUST only honour a challenge **it issued
     itself**, identified by its own `nonce`. A signer MUST NOT accept a
     third party's assertion that a challenge was answered.
  3. Each peer MUST issue at most `challenge_rate` (default 1 per 10 s) of
     `CHALLENGE`s, and at most `challenge_budget` (default 8) per
     candidate per term.
  4. A candidate MUST respond to a well-formed `CHALLENGE` with one
     `CHALLENGE_RESPONSE` per requested block, within 5 s.

  The response is sent on the `data` path; the claim commits to the
  concatenated response hashes in `challenge_commitment`. Because each ACK
  signer challenges independently with its own nonce, a candidate cannot
  pre-compute answers and a single malicious challenger cannot fail the
  candidate.
- `acks` contains valid signatures from a strict majority of the frozen
  roster, no duplicates, and every signer is in the snapshot.
- The certificate is the only way to change the clock authority after a
  host is lost. There is no “lexicographically smallest peer wins” rule
  without ACKs. A partition that cannot assemble a majority stalls rather
  than creating two hosts.

### 5.4 BanEntry and KICK

```
BanEntry = {
  target_pubkey: PublicKey,
  target_peer_id: PeerId,
  certificate: VoteCertificate,  // the passed KICK
}
```

A peer enforces a ban only if it has verified the `KickCertificate` and the
target is in the frozen roster. A `KickCertificate` is a `VoteCertificate`
whose proposal body kind is `KICK`; it is not a separate signature format.
A bare `KICK_NOTICE` without a certificate is ignored. `BAN_REPORT` is
advisory and never used to auto-ban without local evidence.

---

## 6. Manifest and content

### 6.1 Granularity

| Level | Size | Role |
|-------|------|------|
| Block | 32 KiB | request, transfer, and Merkle leaf |
| Piece | 512 KiB = 16 blocks | integrity and bitfield unit |
| Segment | ≈ 4 s, keyframe-aligned | playback unit |

The last piece may have fewer than 16 blocks; the last block may be shorter
than 32 KiB.

### 6.2 StreamParams

```
StreamParams = {
  profile: FMP4 | WEBM,
  codecs: tstr .size (1..128),   // exact MSE/mpv codec string
  piece_length: 524288,
  block_length: 32768,
  target_segment_ms: 4000,
}
```

`duration_ms` and `total_size` are **optional** in the head because a live
transcode does not know them in advance.

### 6.3 ManifestHead

```
ManifestHead = {
  stream_id: StreamId,
  term: uint,                   // host lease term that produced this head
  seq: uint,                    // replay counter, mirrors the envelope
  version: uint,
  manifest_hash: Hash32,
  params: StreamParams,
  total_pieces: uint,
  total_blocks: uint,
  total_bytes: uint / null,
  duration_ms: uint / null,
  init_segment: SegmentRef,     // same shape as SegmentEntry; init bytes live in its piece range
  segment_count: uint,
  chunk_hashes: [Hash32],       // ordered, one per manifest chunk
  chunk_count: uint,
}
```

`manifest_hash` is computed over the canonical logical manifest body
(excluding `manifest_hash`): params, init segment, ordered piece roots,
segment entries, `total_pieces`, and `total_blocks`. It is independent of
chunk boundaries. The head’s `chunk_hashes` are an additional transfer
integrity check for snapshot chunks.

`term` and `seq` are **inside the signed payload**, not only in the
envelope. The envelope (§1.3) has no `term` field, so any message that must
be checked against a lease has to carry its term in the payload. This was
missing from `ManifestHead` in an earlier revision, which made the one
message that bootstraps a stream impossible to authority-check (§5.1).

`chunk_hashes` bounds the maximum stream size: the head must fit in one
envelope (§9), and each hash is 32 bytes, so `chunk_count` is bounded by
`(max_envelope - head_overhead) / 32`. At the default 64 KiB envelope and a
32 KiB chunk, that is ~1 TiB of manifest payload — far beyond the documented
scale envelope, but implementers MUST reject a head whose
`chunk_count != len(chunk_hashes)` rather than allocating from the count.

### 6.4 ManifestChunk

```
ManifestChunk = {
  stream_id: StreamId,
  version: uint,
  chunk_index: uint,
  piece_start: uint,
  piece_roots: [Hash32],        // ≤ 512 entries
  segments: [SegmentEntry],     // ≤ 256 entries
}

SegmentEntry = {
  t_start_ms: uint,
  t_end_ms: uint,
  piece_start: uint,
  piece_end: uint,              // inclusive
  block_start: uint,
  block_end: uint,              // inclusive
}

SegmentRef = SegmentEntry        // init segment uses the same range shape
```

`chunk_hash = BLAKE3("pw-manifest-chunk-v2" || canonical(ManifestChunk))`.
The head’s `chunk_hashes` commits to every transfer chunk. A late joiner
fetches the head and the chunks it needs; it does not need the whole
manifest to start playing.

For a 100 GB video (200,000 pieces, 6.4 MB of piece roots), a 32 KiB chunk
holds up to 512 roots, so the manifest is ~400 chunks / ~13 KB of chunk
hashes. This is small enough for the head and is transferred in 32 KiB
chunks on the `bulk` channel.

### 6.5 Live append and stable identity

- `stream_id` is chosen once at proposal time and never changes.
- `version` increments on every `MANIFEST_APPEND`.
- `MANIFEST_APPEND` carries new piece roots and segment entries:
  ```
  MANIFEST_APPEND = {
    stream_id, term, seq,
    from_version: uint,
    to_version: uint,
    new_piece_roots: [Hash32],   // ≤ 512
    new_segments: [SegmentEntry],// ≤ 256
    new_chunk_hashes: [Hash32],  // optional: chunks sealed by this append
    new_chunk_count: uint / null,
    new_manifest_hash: Hash32,
    new_total_pieces: uint,
    new_total_blocks: uint,
  }
  ```
  It is signed by the host and accepted only for the current stream/term
  and `from_version == local_version`. If a peer is behind, it requests the
  head and the missing chunks/deltas. A host may periodically re-sign and
  broadcast a fresh `MANIFEST_HEAD`; the head’s `chunk_hashes` are the
  snapshot at that version, and later appends are applied on top.
- `MANIFEST_FINAL`:
  ```
  MANIFEST_FINAL = {
    stream_id, term, seq,
    content_id: Hash32,          // final BLAKE3("pw-content-v2" || stream_id || manifest_hash)
    final_manifest_hash: Hash32,
    final_total_pieces: uint,
  }
  ```
- All data-plane messages (`BITFIELD`, `REQUEST`, `BLOCK`, `CANCEL`) use
  `stream_id`, not `content_id`. `content_id` is only the final cache key.
- Storage/resume uses `stream_id`; it can be upgraded to `content_id` after
  `MANIFEST_FINAL`.

### 6.6 Piece roots and block proofs

- For piece `i`, `block_count = min(16, total_blocks - i*16)`.
- `piece_root = merkle_root("pw-piece-v2", block_hashes[0..block_count])`.
- `BLOCK` carries:
  ```
  BLOCK = {
    stream_id, piece, block, block_count,
    bytes: bstr,
    proof: [Hash32],            // Merkle path to piece_root
  }
  ```
- The receiver computes `BLAKE3("pw-block-v2" || bytes)` and verifies the
  proof against `piece_root` from a trusted manifest chunk. A bad block is
  attributable to the exact sender, before the rest of the piece arrives.
- `BlockProof` (used by `BAN_REPORT` and local blame) is the tuple
  `{ stream_id, piece, block, block_count, bad_bytes, block_hash, proof,
  sender_pubkey, sender_sig }`, where `sender_sig` is the offending peer's
  Ed25519 signature over
  `"pw-blockproof-v2" || canonical(body without sender_sig)`.

  A verifier hashes `bad_bytes`, recomputes the leaf, checks that the proof
  does *not* match the committed piece root, and verifies `sender_sig`
  against `sender_pubkey`. Only then is the proof evidence about a
  *specific peer*.

  > **Why the signature is mandatory.** Without it, `BlockProof` proves only
  > that *some* bytes fail the root — a property any peer can fabricate with
  > arbitrary `bad_bytes`. It is not attributing evidence and MUST NOT be
  > treated as such. With `sender_sig`, a peer that signs a proof over bytes
  > it did not send is itself committing a provable protocol violation, so
  > the incentive is symmetric.

  A `BlockProof` without a valid `sender_sig` is a **rumor**: it MAY inform a
  peer's local scheduling score, but it MUST NOT contribute to a strike, a
  local ban, or a room-wide ban.
- If the receiver does not yet have the piece root, it requests the
  relevant manifest chunk; it must not accept the block on the sender’s
  word.

### 6.7 Last piece/block

- `total_pieces = ceil(total_size / piece_length)` when `total_size` is
  known; for live streams, `total_pieces` grows with `MANIFEST_APPEND`.
- `total_blocks = ceil(total_size / block_length)` when known.
- The last piece has `block_count` blocks; the last block has
  `last_block_len = total_size - (total_blocks-1)*block_length`.
- A block mask MUST NOT request bits `>= block_count`. A `BLOCK` with the
  wrong length for its index is a protocol violation.

---

## 7. Message catalog

`✍` means the envelope must carry a valid `sig` and `author`. `✍(host)`
means the sender must additionally be the current `HostLease` holder for the
named `(stream_id, term)`.

> The tables in §7.1–§7.7 are **generated** from
> [`schema/messages.yaml`](../schema/messages.yaml) by
> `scripts/gen_catalog.py`. Do not edit them by hand — CI runs
> `gen_catalog.py --check` and fails on drift. Prose between the tables is
> hand-written and is preserved by the generator.

### 7.1 Identity and transport

<!-- CATALOG:7.1 -->
| ID | Type | ✍ | Payload | Channel | When |
|---:|------|---|---------|---------|------|
| 0x01 | `HELLO` | — | { version, pubkey, enc_pubkey, nonce, caps, max_message_size } | control | first message each way on a new control channel; every field is bound by AUTH |
| 0x02 | `AUTH` | ✍ | { auth_tag, auth_sig } per §4.3 | control | answers HELLO; carries both an envelope sig and a transcript-bound auth_sig |
| 0x03 | `PING` | — | { t0 } | control | every 5 s per connection |
| 0x04 | `PONG` | — | { t0 } | control | echoes PING; feeds the windowed-min RTT estimator |
| 0x05 | `BYE` | — | { code, reason } | control | clean exit or rejection; codes in errors.md |
<!-- /CATALOG:7.1 -->

### 7.2 Room and authority

<!-- CATALOG:7.2 -->
| ID | Type | ✍ | Payload | Channel | When |
|---:|------|---|---------|---------|------|
| 0x10 | `ROOM_STATE` | ✍(host) | { state_version, term, host_lease, roster, banlist, banlist_complete, banlist_root, active_proposals, current_stream } | control | after AUTH, and on any change |
| 0x11 | `PRESENCE` | — | { status, buffered_ms } | control | on change; status only, no media position |
| 0x12 | `HOST_LEASE` | ✍(host) | HostLease | control | on election or takeover; also carried inside ROOM_STATE |
| 0x13 | `REKEY` | ✍ | { new_epoch, basis, basis_hash, wrapped_keys } | control | after a kick or scheduled rotation; key is derived, not chosen |
<!-- /CATALOG:7.2 -->

`ROOM_STATE` is authored by the current host. If `host_lease` is `null`
(no host yet), any member may author a roster-only state; it cannot
authorize a host or a stream. `state_version` increases monotonically
within a term. Receivers accept only `(term, state_version)` greater than
the current pair. A late joiner may receive a state snapshot from any peer,
but it must contain a validly signed `ROOM_STATE` and, once a lease exists,
a valid host signature.

`banlist_complete` and `banlist_root` exist because `ROOM_STATE` must fit one
envelope. When the full certificates do not fit, the sender sets
`banlist_complete: false` and includes only summaries; the receiver then
fetches the certificates from at least two peers and compares roots. See
§9 and [governance.md](../design/governance.md) §4.1.1.

Supporting room objects:

```
RosterEntry = {
  peer_id, sig_pubkey, enc_pubkey,
  signal_pub: EncPubKey,        // per-(room, epoch); the ONLY trusted source
  caps, joined_ms,
  invite_id,
}

StreamRef = {
  stream_id, host_peer_id, term,
  manifest_hash, state: PROPOSED | PLAYING | PAUSED | ENDED,
}
```

`signal_pub` is the authenticated alias→key binding that §11.2 needs. It is
carried here, in a host-signed object, so that a malicious relay cannot
substitute its own key and read plaintext SDP. The relay's `RelayRoster`
(§11.1) is a discovery *hint* only; a peer that has no roster entry for a
recipient MUST NOT send it a `SignalEnvelope`.

### 7.3 Manifest

<!-- CATALOG:7.3 -->
| ID | Type | ✍ | Payload | Channel | When |
|---:|------|---|---------|---------|------|
| 0x18 | `MANIFEST_HEAD` | ✍(host) | ManifestHead | bulk | on stream activation and on demand |
| 0x19 | `MANIFEST_CHUNK` | — | { stream_id, version, chunk_index, chunk_hash, bytes } | bulk | snapshot transfer; designated responder only |
| 0x1A | `MANIFEST_GET` | — | { stream_id, version, chunk_indices, responder } | bulk | request missing chunks; `responder` prevents N-way amplification |
| 0x1B | `MANIFEST_APPEND` | ✍(host) | §6.5 | bulk | live-transcode growth |
| 0x1C | `MANIFEST_FINAL` | ✍(host) | §6.5 | bulk | stream complete; assigns content_id |
<!-- /CATALOG:7.3 -->

`MANIFEST_CHUNK` is unsigned because its `chunk_hash` is committed by the
signed head. Any peer may serve a chunk — but only a **designated
responder** may, or one broadcast `MANIFEST_GET` in a 16-peer mesh produces
15 identical 32 KiB responses (480 KiB of upload for a 32 KiB need) and
hands an attacker a free amplification vector. A peer serves a chunk only if
`responder == self`, or `responder` is null and this peer is the lowest
`peer_id` among peers it believes hold that chunk.

### 7.4 Swarm data

<!-- CATALOG:7.4 -->
| ID | Type | ✍ | Payload | Channel | When |
|---:|------|---|---------|---------|------|
| 0x20 | `BITFIELD` | — | { stream_id, version, bits } | control | every 1 s to every peer; Roaring, run-optimized |
| 0x21 | `REQUEST` | — | { stream_id, want: [{ piece, block_mask }] } | data | batched; block-mask total must be <= Q |
| 0x22 | `BLOCK` | — | { stream_id, piece, block, block_count, bytes, proof } | data | one 32 KiB block plus its Merkle path |
| 0x23 | `CANCEL` | — | { stream_id, want: [...] } | data | kills queued requests only; in-flight blocks are stored anyway |
<!-- /CATALOG:7.4 -->

`BITFIELD` uses Roaring compression
([roaring-compression.md](roaring-compression.md)).

**`REQUEST`/`CANCEL` account in blocks, not entries.** An entry is
`{ piece, block_mask }`, so one entry can name up to 16 blocks. The limits
compose: entries ≤ 64 per message (§9), and the **block-mask total of one
`REQUEST` MUST be ≤ `Q`**. A message that exceeds it is a protocol violation
and the whole message is ignored — truncating it would give a buggy sender
partial service and hide the bug.

### 7.5 Sync and host lifecycle

<!-- CATALOG:7.5 -->
| ID | Type | ✍ | Payload | Channel | When |
|---:|------|---|---------|---------|------|
| 0x28 | `SYNC` | ✍(host) | { stream_id, term, seq, t_media_ms, state, rate } | control | every 1 s and on any transport event |
| 0x29 | `START_AT` | ✍(host) | { stream_id, term, seq, t_media_ms } | control | after the buffering gate |
| 0x2A | `STALL_NOTICE` | ✍ | { stream_id, term, seq, t_media_ms, reason } | control | local starvation; informs the room and the scheduler |
| 0x2B | `TAKEOVER_CLAIM` | ✍ | TakeoverClaim | control | a candidate claims the next term |
| 0x2C | `TAKEOVER_ACK` | ✍ | TakeoverAck | control | majority acknowledgement of a claim |
| 0x2D | `CHALLENGE` | — | { stream_id, nonce, blocks: [{ piece, block }] } | control | proof-of-possession challenge; must be in-region |
| 0x2E | `CHALLENGE_RESPONSE` | — | { stream_id, nonce, index, block } | data | one block per message; only to the challenge issuer |
| 0x2F | `READY` | — | { stream_id, term, buffered_ms } | control | buffering-gate report |
<!-- /CATALOG:7.5 -->

`SYNC`/`START_AT` carry no host wall clock. Followers anchor on their own
receive time ([sync.md](../design/sync.md) §2). `TAKEOVER_*` and
`CHALLENGE_*` are the only legitimate way to change clock authority after
host loss.

#### 7.5.1 One source of truth per fact

Media position was previously reported by four messages (`SYNC`,
`PRESENCE`, `READY`, `STALL_NOTICE`), three of them signed, with overlapping
semantics. It is now split by *who owns the fact*:

| Fact | Owner | Message | Signed |
|---|---|---|---|
| The room's reference position | Host | `SYNC` | yes (lease) |
| The room's start instant | Host | `START_AT` | yes (lease) |
| *My* position, when I am starving | each peer | `STALL_NOTICE` | yes (authorship) |
| *My* liveness and buffer depth | each peer | `PRESENCE` | no |
| *My* readiness for a buffering gate | each peer | `READY` | no |

`PRESENCE` is **unsigned** and carries no media position, only a status and a
buffer depth. A forged one makes a liar look `ACTIVE` when it is `BUFFERING`,
which costs the liar a stall and nobody else anything; signing it bought
nothing and cost one Ed25519 verify per peer per change at 16 peers.

`READY` remains a separate type only because the buffering gate counts it per
`term` ([governance.md](../design/governance.md) §3); otherwise it is a
one-shot `PRESENCE` with a term tag.

### 7.6 Governance and social

<!-- CATALOG:7.6 -->
| ID | Type | ✍ | Payload | Channel | When |
|---:|------|---|---------|---------|------|
| 0x30 | `PROPOSAL` | ✍ | Proposal | control | broadcast |
| 0x31 | `VOTE` | ✍ | Vote | control | broadcast; non-changeable, equivocation is fatal |
| 0x32 | `VOTE_CERT` | ✍ | VoteCertificate | control | leader publishes the frozen-roster tally |
| 0x33 | `KICK_NOTICE` | ✍ | { ban: BanEntry } | control | carries the passed KickCertificate |
| 0x34 | `BAN_REPORT` | ✍ | { offender, reason, evidence: BlockProof / null } | control | advisory gossip; evidence needs sender_sig to be actionable |
| 0x38 | `CHAT` | ✍ | { msg_id, ts_ms, lamport, body } | control | broadcast; dedup by msg_id; order by (lamport, ts_ms, peer_id) |
<!-- /CATALOG:7.6 -->

`BAN_REPORT` is advisory: a receiver MAY locally avoid the offender, but it
MUST NOT add a room-wide ban without a verified `KickCertificate`. Its
`evidence` field is only actionable when the `BlockProof` carries a valid
`sender_sig` (§6.6); without one it is a rumor.

A `VOTE_CERT` containing two votes from one voter is **invalid**, not
resolved — see §5.2.

### 7.7 Reserved

<!-- CATALOG:7.7 -->
| Range | Use |
|-------|-----|
| 0x40-0x4F | voice control and media tracks (M5) |
| 0xF0-0xFF | experimental; ignored by default |
<!-- /CATALOG:7.7 -->

## 8. Validation and replay rules

Every receiver processes a message in this order:

1. **Envelope**: version, size, CBOR depth, `room`, `epoch`.
2. **Signature**: if the type is signed, `author` and `sig` are present and
   verify; if the type is unsigned, `sig` is ignored.
3. **Replay**: `seq` is greater than the last accepted value for
   `(author, stream, type, term)`; `CHAT` dedups by `msg_id`.
4. **Authority**:
   - host-only types (`SYNC`, `START_AT`, `MANIFEST_HEAD`, `MANIFEST_APPEND`,
     `MANIFEST_FINAL`) must match the current `HostLease` host/term.
     `MANIFEST_CHUNK`/`MANIFEST_GET` may be served by any peer; their
     integrity is the chunk hash committed by the head.
   - `ROOM_STATE` must carry a higher `(term, state_version)`; if
     `host_lease` is non-null it must be host-signed and carry a valid
     certificate, and if it is null it may be signed by any member and
     cannot authorize a host;
   - `HOST_LEASE` must carry a valid certificate and a higher term;
   - `KICK_NOTICE`/`BanEntry` must carry a valid `KickCertificate`;
   - `REKEY` must carry a valid `KickCertificate` or current `HostLease`
     basis, and `new_epoch == current_epoch + 1`;
   - `TAKEOVER_CLAIM` must carry a valid `TakeoverClaim`, be from a
     complete candidate, and be followed by a successful challenge. The
     final `HOST_LEASE` must carry a `TakeoverCertificate` with majority
     `TAKEOVER_ACK`s and a matching challenge commitment.
5. **Stream/manifest**: the `stream_id` is current; the manifest version is
   known or requested; block proofs verify against a trusted `piece_root`.
6. **Limits**: per-type count and byte budgets (§9).

A failed signature or proof is objective evidence; a failed authority check
is a protocol violation (strike, then ban on repeat). Do not conflate the
two.

---

## 9. Limits (normative)

Limits are enforced with token buckets per peer and per message class.

| Limit | Value | On excess |
|-------|------:|-----------|
| Envelope | ≤ 64 KiB (negotiated 48–256 KiB) | close + strike |
| CBOR depth / collection length | 16 / 4096 | strike |
| Control messages | ≤ 64/s and ≤ 128 KiB/s per peer | throttle → strike |
| Data bytes | ≤ 8 MiB/s burst, ≤ 4 MiB/s sustained per peer | throttle |
| Bulk/manifest bytes | ≤ 1 MiB/s per peer | throttle |
| `REQUEST`/`CANCEL` entries | ≤ 64 each | strike |
| `REQUEST` block-mask total | ≤ `Q` blocks per message | whole message ignored + strike |
| Outstanding blocks per peer | ≤ Q, Q ≤ 64 | excess ignored |
| `BITFIELD` payload | ≤ 16 KiB, ≤ 1/s | strike |
| `CHAT` body | ≤ 2 KiB | truncate + strike on repeat |
| Manifest chunk | ≤ 32 KiB | strike |
| Active proposals per peer | 1 | excess dropped |
| `TAKEOVER_ACK` set | ≤ roster size | excess ignored |
| `CHALLENGE` blocks | ≤ 4 | excess ignored |
| `CHALLENGE` rate | ≤ 1 per 10 s, ≤ 8 per candidate per term | excess ignored |
| `Caps.codecs` | ≤ 16 entries, ≤ 128 B each | strike |
| `RosterEntry` count in `ROOM_STATE` | ≤ 64 | reject state |

`ROOM_STATE` must fit in one envelope. If a large banlist would exceed the
cap, send the state **without** the full certificates, set
`banlist_complete: false`, and follow with individual
`KICK_NOTICE`/`BanEntry` messages. A late joiner that receives
`banlist_complete: false` MUST request the missing certificates from at
least two peers and MUST NOT enforce any ban entry it has not verified.
`banlist_complete` is how omission becomes *detectable*: a peer that
receives `banlist_complete: true` and then a `KICK_NOTICE` for an epoch
older than that state has proof of a withholding Host (see
[governance.md](../design/governance.md) §4.1).

`Q` is BDP-adaptive ([distribution.md](../design/distribution.md) §4.3), but the
application MUST also respect the DataChannel’s `bufferedAmount`; an
unbounded app queue is a bug even if Q is small.

### 9.1 Media parser limits

The segmenter and the transmuxer parse bytes that came off the wire from an
untrusted Host. The protocol limits above do not bound them; these do.

| Limit | Value | On excess |
|-------|------:|-----------|
| fMP4 box size (`ftyp`/`moov`/`moof`/`mdat`) | ≤ 16 MiB | reject stream |
| Init segment (`ftyp` + `moov`) total | ≤ 4 MiB | reject stream |
| Media segment (`moof` + `mdat`) total | ≤ 32 MiB | reject stream |
| Box nesting depth | ≤ 8 | reject stream |
| `moov` sample-table entries | ≤ 1,000,000 | reject stream |
| EBML cluster size (WebM) | ≤ 32 MiB | reject stream |
| EBML element nesting depth | ≤ 8 | reject stream |
| Segments buffered ahead by the segmenter | ≤ 180 s of media | throttle reader |
| ffmpeg/ffprobe stderr retained | ≤ 64 KiB ring | discard oldest |
| ffprobe JSON output | ≤ 1 MiB | reject probe |
| Total ffmpeg wall clock for probe | ≤ 30 s | kill process |

A stream that violates any row is rejected with a clear `MEDIA_*` error
([errors.md](errors.md)) and the proposal fails; it is **not** a ban, because
a legitimate encoder can emit a large `mdat` and the Host may simply have a
file the receiver refuses to parse. Declining to parse is always allowed.

The receiver MUST apply these limits *before* allocating, exactly as
required for CBOR (§1.1): a 64-bit box-size field is attacker-controlled
and a naive `Vec::with_capacity(size)` is a one-packet OOM.

---

## 10. Versioning and compatibility

- `v` is the major protocol version. A peer with a different major version
  may complete AUTH but cannot join the data plane; it receives a clear
  `BYE` code.
- Minor changes may add optional CBOR map keys; unknown keys are ignored.
- New message types are optional by default. A receiver that does not know
  an optional type ignores it; a required type it does not know causes a
  clean disconnect with `UNSUPPORTED_MESSAGE`.
- Never change the meaning of an existing field. Add a new field or a new
  message type.
- `content_id`/`manifest_hash` are stable across implementations only if
  they use the canonical CBOR rules in §1.1 and §3.
- A deployed v2 implementation must ship a migration note before any
  breaking change.

---

## 11. Signaling relay protocol

The relay is a blind mailbox. It never sees media, chat, room keys, or
plaintext SDP.

### 11.1 Relay messages

```
RelaySubscribe = {
  channel: bytes .size 32,     // channel_hash (epoch channel, invite channel, or lobby)
  alias: bytes .size 16,       // per-epoch alias
  signal_pub: EncPubKey,       // epoch-scoped signaling key, NOT the identity key
}

RelayRoster = {
  aliases: [ { alias, signal_pub } ],   // a discovery hint, never trusted
}

RelaySignal = {
  to: bytes .size 16,
  envelope: bytes,             // SignalEnvelope or InviteEnvelope
}

RelayBasis = {
  channel: bytes .size 32,     // lobby channel
  body: bytes,                 // LOBBY_BASIS (§4.4.3), opaque to the relay
}

RelayLeave = {}
RelayError = { code, message }   // codes in [errors.md](errors.md)
```

`RelayRoster.signal_pub` is **advisory only** (§11.2): it tells a peer which
keys the relay *claims*, and the authenticated value from the host-signed
`ROOM_STATE` always wins. A relay that lies here is detected when the
recipient fails to decrypt, which is a loud, diagnosable failure rather
than a silent MITM.

`RelayBasis` carries lobby-channel basis records (§4.4.3). They are signed
by their publisher and the relay MUST treat `body` as opaque bytes, so a
rejoining peer can reconstruct missed rekeys after being offline.

### 11.2 SignalEnvelope

```
SignalEnvelope = {
  v: uint,
  epoch: uint,                 // needed as AAD and for replay keying
  from_alias: bytes .size 16,
  to_alias: bytes .size 16,
  seq: uint,
  eph_pub: EncPubKey,
  nonce: bytes .size 24,
  ciphertext: bytes,
  sig: Signature,
}
```

- `key = HKDF(X25519(eph_sk, to_signal_pub), salt=channel_hash, info="pw-signal-v2", L=32)`.
- `plaintext = { from_pubkey: PublicKey, room_id: bytes .size 16, payload: bytes }`.
- `ciphertext = XChaCha20-Poly1305(key, nonce, plaintext, aad=canonical(header))`.
- `sig = Ed25519(from_sk, "pw-signal-sig-v2" || canonical(header) || ciphertext)`.

> **`room_id` is encrypted, not in the header.** An earlier revision put
> `room: bytes .size 16` in the cleartext header, which let the relay read
> the room identity directly and link a room across epochs, contradicting
> the claim that it "sees only aliases, sizes, and timing". It is now
> inside `plaintext`. The header uses `channel_hash` as the HKDF salt, which
> the relay already knows, so no new information is exposed.

- **`to_signal_pub` comes from the roster, not the relay.** The sender
  looks up the recipient's `signal_pub` in the host-signed `ROOM_STATE`
  roster (§7.2). The relay's `RelayRoster` (§11.1) is only used to discover
  *which* aliases are currently subscribed. If a peer has no authenticated
  `signal_pub` for a recipient, it MUST NOT send that recipient a
  `SignalEnvelope`; it falls back to waiting for the recipient's
  `ROOM_STATE` or to manual signaling. Taking `signal_pub` from the relay
  would let a malicious relay substitute its own key and read plaintext
  SDP/ICE.
- The recipient decrypts the plaintext, learns `from_pubkey`, verifies `sig`
  with it, and checks that `from_pubkey` maps to `from_alias` under
  `alias = HMAC-SHA256(room_psk, "pw-alias-v2" || peer_id || epoch)[0..16]`.
  The relay never sees `from_pubkey` or `room_id`; it sees the epoch-scoped
  `eph_pub` and aliases.
- Recipients keep the highest accepted `seq` per `(from_alias, epoch)` and
  reject stale/duplicate signal envelopes. The relay can drop or reorder
  envelopes, but it cannot make an old SDP/ICE blob current.
- `signal_pub` is generated per room epoch and is **not** the peer’s static
  identity `enc_pubkey`. The relay therefore sees a key scoped to one
  epoch, not a stable identifier that links the peer across rooms. The
  static `enc_pubkey` is used only inside the room (ROOM_STATE, REKEY) and
  is never published to the relay.
- **What the relay actually sees:** opaque `channel_hash` values, per-epoch
  aliases, epoch integers, `eph_pub`, envelope sizes, timing, and the IP
  addresses of WebSocket connections. It cannot read SDP/ICE, room identity,
  or sender identity, and it cannot impersonate a sender. It can still deny
  service, omit peers, and observe IPs; see
  [security.md](../design/security.md) §5 B.

### 11.3 Invite bootstrap

```
InviteToken = "pw1_" || base32(room_id) || "." || base32(invite_id) || "." || base64url(secret)
InviteChannel = BLAKE3("pw-invite-channel-v2" || room_id || invite_id)

ROOM_GENESIS = {
  room_id, epoch: 0,
  creator_pubkey,
  roster: [RosterEntry],
  membership_log_root: Hash32,
  signature,
}

MembershipEntry = MEMBER_ADD | MEMBER_REMOVE

MEMBER_ADD = {
  invite_id, peer_id, sig_pubkey, enc_pubkey,
  accepter_pubkey, signature,
}

MEMBER_REMOVE = {
  target_pubkey, certificate: VoteCertificate,   // the passed KICK
}

InviteGrant = {
  room_id, invite_id,
  inviter_pubkey,
  expiry_ms, max_uses,
  invitee_pubkey: PublicKey / null,
  signature,
}

JOIN_REQUEST = {
  room_id, invite_id,
  pubkey, join_pub, nonce,
  proof = HMAC-SHA256(invite_key, "pw-join-v2" || room_id || invite_id || pubkey || join_pub || nonce),
}

InviteEnvelope = {
  v, room_id, invite_id,
  nonce: bytes .size 24,
  ciphertext: bytes,          // JOIN_REQUEST, encrypted under invite_key
  mac: bytes .size 32,        // HMAC(invite_key, header || ciphertext)
}

JOIN_ACCEPT = {
  room_id, epoch, room_key,       // encrypted to the invitee's ephemeral join_pub
  roster, membership_log,
  accepter_pubkey, signature,
}
```

`invite_key = HKDF(secret, salt=room_id||invite_id, info="pw-invite-v2", L=32)`.
`InviteGrant` is signed by the inviter; a receiver accepts it only if
`inviter_pubkey` is in the current membership log/roster.
`JOIN_REQUEST` is wrapped in an `InviteEnvelope` encrypted under
`invite_key` and sent on `InviteChannel`. A member accepts a join
only if it has a matching `InviteGrant`, the grant
is unexpired and unused, and the proof verifies. The `JOIN_ACCEPT` is
wrapped in a `SignalEnvelope` encrypted to the invitee's ephemeral
`join_pub`. After that, the
invitee joins the room channel for the current epoch and runs AUTH.

### 11.4 Relay limits

- Max channels and peers per channel are operator-configurable.
- Max `SignalEnvelope`/`InviteEnvelope` size: 32 KiB.
- Max signaling bytes/s per alias: 64 KiB/s.
- Max `LOBBY_BASIS` body size: 1 KiB; max retained per lobby channel: 512
  records (~2 epochs of history for a very busy room).
- `lobby_retention`: 24 h. Lobby records older than this are dropped. A peer
  that has been offline longer than `lobby_retention` cannot catch up via
  the lobby and MUST obtain a fresh invite (its old epoch key is still
  valid for reading old caches, but it cannot rejoin).
- Idle timeout: 90 s; clients ping every 30 s.
- The relay stores no persistent data on disk; lobby records live in memory
  only, and a room’s mailboxes vanish when the last member disconnects and
  `lobby_retention` elapses.

---

## 12. What this schema deliberately does not do

- It does not make the relay trusted. It makes the relay unable to read or
  forge signaling; it can still deny service, omit peers, and observe IPs.
- It does not make a peer ID a legal identity.
- It does not make a room resistant to a member leaking the current room
  key.
- **It does not cryptographically exclude a kicked peer from the next
  epoch.** Epoch keys are derived from the previous key (§4.4.1), which the
  kicked peer holds, and the lobby is public by construction. The kicked
  peer is excluded by the banlist and by AUTH rejection, not by secrecy.
  Key rotation is defence-in-depth against passive capture of *future*
  traffic. Cryptographic exclusion would require a fresh Diffie-Hellman
  among the remaining members, which is incompatible with offline catch-up;
  the trade-off is documented in [design-log.md](../design/design-log.md).
- It does not provide anonymity against a global traffic observer.
- It does not provide attribution for a bad block unless the sender signed
  the `BlockProof` (§6.6).
