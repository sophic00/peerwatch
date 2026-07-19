# Wire Protocol (v2)

The complete, normative description of what peers send each other. For the
*meaning* of the flows, see the linked subsystem documents, this file is the
catalog.

---

## 1. Framing and serialization

- Transport: WebRTC DataChannels ([connectivity.md](connectivity.md)).
  DataChannels are **message-oriented**. One protocol envelope = one
  DataChannel message. No length prefixes, no stream framing.
- Two channels per peer connection:

| Channel | SCTP mode | Traffic |
|---------|-----------|---------|
| `control` | reliable, **ordered** | everything except block payloads/requests |
| `data` | reliable, **unordered** | `REQUEST`, `PIECE`, `CANCEL` |

- Serialization: **bincode** (via serde). Compact, schema-less but
  version-tolerant by field order discipline, identical code on native and
  WASM. Max message size: 1 MiB (a 64 KiB block + overhead fits trivially,
  the manifest is the largest message, ~400 KiB for a 6 GB video).
- **Envelope**:

```
Envelope {
  v: u8,                    // protocol version, = 2
  t: u8,                    // message type (catalog below)
  payload: bytes,           // bincode of the per-type struct
  sig: Option<[u8; 64]>,    // Ed25519 over (v ‖ t ‖ payload ‖ channel_binding)
}
```

- `sig` is present on exactly the types marked ✍ below. `channel_binding`
  mixes in the room channel hash so signatures can't be replayed across
  rooms. Unknown `t` values are **ignored** (forward compatibility).

## 2. Message catalog

### 2.1 Identity & session (control)

| # | Type | Sig | Payload | When |
|---|------|-----|---------|------|
| 0x01 | `HELLO` | — | `{ version, pubkey, nonce, caps }` | first message each way on a new control channel. `caps`: capability flags (`VOICE`, `WEBM_OK`, `LOW_UPLOAD`, `NATIVE`, …) + storage budget class |
| 0x02 | `AUTH` | ✍ | `{ signature over transcript }` | answers HELLO, transcript per [identity.md](identity.md) §2.2 (includes room_psk-derived MAC) |
| 0x03 | `PING` | — | `{ t0 }` | every 5 s per connection |
| 0x04 | `PONG` | — | `{ t0 }` | echoes PING |
| 0x05 | `BYE` | — | `{ reason }` | clean exit |

### 2.2 Room state (control)

| # | Type | Sig | Payload | When |
|---|------|-----|---------|------|
| 0x10 | `ROOM_STATE` | ✍ | `{ roster: [{peer_id, pubkey, caps, joined_ms}], current_content: Option<{content_id, host_peer_id, state}>, active_proposals, banlist }` | sent to a peer right after AUTH, delta-gossiped on change |
| 0x11 | `PRESENCE` | ✍ | `{ status: WATCHING | BUFFERING | FREE_ROAM | AWAY }` | on change, drives UI + scheduler hints |

### 2.3 Content metadata (control)

| # | Type | Sig | Payload | When |
|---|------|-----|---------|------|
| 0x18 | `MANIFEST` | ✍(host) | the manifest struct ([distribution.md](distribution.md) §2) | on video activation, on demand to late joiners |
| 0x19 | `MANIFEST_APPEND` | ✍(host) | `{ new_pieces: [sha256…], new_segments: […] }` | live-transcode growth |
| 0x1A | `MANIFEST_FINAL` | ✍(host) | `{ content_id, total_pieces }` | stream complete, commits the final content ID |

### 2.4 Swarm data (control + data)

| # | Type | Sig | Payload | When |
|---|------|-----|---------|------|
| 0x20 | `BITFIELD` | — | `{ content_id, bits: bytes }` | control, every 1 s to every peer |
| 0x21 | `REQUEST` | — | `{ content_id, want: [{piece, block_mask}…] }` | data, batched, ≤ 64 entries |
| 0x22 | `PIECE` | — | `{ content_id, piece, block, bytes: 64 KiB }` | data, one block per message (type name kept from v1) |
| 0x23 | `CANCEL` | — | `{ content_id, want: [{piece, block_mask}…] }` | data, kills *queued* (not in-flight) requests ([distribution.md](distribution.md) §4.5) |

### 2.5 Playback sync (control)

| # | Type | Sig | Payload | When |
|---|------|-----|---------|------|
| 0x28 | `SYNC` | ✍(host) | `{ content_id, t_media, state: PLAY|PAUSE, rate, host_wallclock_ms }` | every 1 s + on transport events |
| 0x29 | `START_AT` | ✍(host) | `{ content_id, host_wallclock_ms, t_media }` | after buffering gate |
| 0x2A | `STALL_NOTICE` | ✍ | `{ content_id, t_media, reason: BUFFER_EMPTY|FRONTIER }` | local starvation ([sync.md](sync.md) §4) |
| 0x2B | `SOURCE_TAKEOVER` | ✍ | `{ content_id, t_media }` | complete peer assumes host role ([sync.md](sync.md) §6) |
| 0x2C | `READY` | — | `{ content_id }` | buffering-gate report to Host-elect |

### 2.6 Governance (control)

| # | Type | Sig | Payload | When |
|---|------|-----|---------|------|
| 0x30 | `PROPOSAL` | ✍ | the Proposal struct ([governance.md](governance.md) §1) | broadcast |
| 0x31 | `VOTE` | ✍ | the Vote struct | broadcast |
| 0x32 | `VOTE_RESULT` | ✍(each peer's own tally) | `{ proposal_id, passed, approvals, roster_size }` | on tally |
| 0x33 | `KICK_NOTICE` | ✍ | `{ target_pubkey, proposal_id }` | on kick passage, also carried in banlist |
| 0x34 | `BAN_REPORT` | ✍ | `{ offender_peer_id, reason: POISON|SIGFAIL|PROTO|FLOOD, evidence }` | advisory gossip ([security.md](security.md) §3) |

### 2.7 Social (control)

| # | Type | Sig | Payload | When |
|---|------|-----|---------|------|
| 0x38 | `CHAT` | ✍ | `{ ts_ms, body (≤ 2 KiB UTF-8) }` | broadcast, ordered by (ts, peer_id) at display |

### 2.8 Reserved

| Range | For |
|-------|-----|
| 0x40–0x4F | `VOICE_*` — voice-capability negotiation & control (phase 2, [roadmap.md](roadmap.md)) |
| 0xF0–0xFF | experimental |

## 3. Sequencing rules (normative)

1. On channel open: `HELLO` ↔ `HELLO`, then `AUTH` ↔ `AUTH`. **No other
   message is accepted before a verified AUTH.**
2. After AUTH: the *existing* peer sends `ROOM_STATE`, if a video is active,
   `MANIFEST` follows, then both sides send `BITFIELD`. From there,
   everything is asynchronous.
3. `PROPOSAL(PLAY_VIDEO)` passing ⇒ host-elect sends `MANIFEST` to peers
   missing it, collects `READY`s, then issues `START_AT`
   ([governance.md](governance.md) §3).
4. `SOURCE_TAKEOVER` is valid only from a peer whose bitfield (as last
   received) was complete for that content, receivers verify before
   switching clock authority.
5. Every signed message's `sig` is verified before *any* payload field is
   acted upon, failures → [security.md](security.md) §3.
6. `BITFIELD`, `REQUEST`, `PIECE`, `CANCEL`, `PING`, `PONG` are the only
   unsigned types, all are confined to the data plane or are individually
   useless to forge (a forged bitfield merely hides/reveals pieces the
   hashes will adjudicate).

## 4. Limits (normative)

| Limit | Value | Exceeding it |
|-------|------:|--------------|
| Envelope size | ≤ 1 MiB | close + strike |
| `REQUEST` entries | ≤ 64 | strike |
| Control msgs / s / peer | ≤ 64 | throttle → strikes |
| Outstanding block requests / peer | ≤ Q (BDP-adaptive, ≤ 64) | excess ignored |
| `CHAT` body | ≤ 2 KiB | truncated display + strike on repeat |
| Active proposals / peer | 1 | excess dropped |
