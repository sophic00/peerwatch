---
tier: normative
---

# Error Codes (normative)

Every layer of PeerWatch surfaces failures to a human and to another
machine. This document enumerates the codes so that a UI string, a log
line, and a `BYE` frame all agree.

Three families:

| Family | Prefix | Travels over | Meaning |
|---|---|---|---|
| Protocol disconnect | `PW_` | `BYE { code }` on `control` | a peer is leaving, or a peer is rejecting this connection |
| Relay | `RL_` | `RelayError { code }` | the relay is refusing a subscription or a signal |
| Local | `L_` | `UiEvent::Error`, `PlayerEvent::Error` | something on this machine failed |

Codes are `u16`. The numeric ranges are stable; the strings are for humans
and logs only and MUST NOT be parsed.

---

## 1. `BYE` codes (protocol, `PW_`)

`BYE` is sent on `control` before a peer closes a connection
([protocol.md](protocol.md) §7.1). A peer that closes without `BYE` is
treated as a crash, not a decision.

| Code | Name | When | Reconnectable? |
|---:|---|---|---|
| 0 | `PW_NORMAL` | clean exit, room left | n/a |
| 1 | `PW_VERSION_MISMATCH` | major version differs | after upgrade |
| 2 | `PW_UNSUPPORTED_MESSAGE` | required message type unknown | after upgrade |
| 3 | `PW_AUTH_FAILED` | `auth_tag` or `auth_sig` invalid | no |
| 4 | `PW_EPOCH_STALE` | message epoch not in `{e, e+1}` during grace | yes, after rekey catch-up |
| 5 | `PW_BANNED` | peer is on a verified banlist | no |
| 6 | `PW_NOT_INVITED` | no membership record | only with a fresh invite |
| 7 | `PW_ROOM_FULL` | roster limit reached | after someone leaves |
| 8 | `PW_PROTOCOL_VIOLATION` | malformed / out-of-state message | no (strike recorded) |
| 9 | `PW_AUTHORITY_VIOLATION` | valid signature, wrong host/term/cert | no (strike recorded) |
| 10 | `PW_RATE_LIMITED` | byte or message budget exceeded | yes, after backoff |
| 11 | `PW_ENVELOPE_TOO_LARGE` | exceeds negotiated `max_message_size` | yes, after renegotiation |
| 12 | `PW_CBOR_INVALID` | canonical-CBOR or depth/limit failure | no |
| 13 | `PW_TIMEOUT` | keepalive or handshake timeout | yes |
| 14 | `PW_HOST_LOST` | stream aborted because the lease holder vanished and no takeover occurred | yes |
| 15 | `PW_STREAM_REPLACED` | a new `PLAY_VIDEO` passed; this stream is retired | n/a |
| 16 | `PW_REKEY_FAILED` | could not obtain `epoch+1` within `rekey_grace` | yes, via lobby |
| 17 | `PW_INTERNAL` | local bug or unrecoverable local fault | yes, once |

### 1.1 Which codes are evidence

Only these indicate misbehaviour by the *sender's peer* and feed the
strike ladder ([security.md](../design/security.md) §4):

`PW_PROTOCOL_VIOLATION`, `PW_AUTHORITY_VIOLATION`, `PW_CBOR_INVALID`,
`PW_ENVELOPE_TOO_LARGE` (on repeat), `PW_RATE_LIMITED` (on repeat).

Everything else is either a local condition or a legitimate decision, and
MUST NOT contribute to a ban. In particular `PW_EPOCH_STALE` and
`PW_REKEY_FAILED` are *expected* during a kick and are not evidence of
anything.

---

## 2. Relay codes (`RL_`)

`RelayError { code, message }` ([protocol.md](protocol.md) §11.1). The relay
MUST NOT include peer IDs, room IDs, or key material in `message`; it is
shown to users and written to logs.

| Code | Name | Meaning |
|---:|---|---|
| 0 | `RL_OK` | reserved |
| 1 | `RL_BAD_FRAME` | not valid CBOR for a relay message |
| 2 | `RL_UNKNOWN_MESSAGE` | message type not in §11.1 |
| 3 | `RL_FRAME_TOO_LARGE` | envelope > 32 KiB, basis > 1 KiB |
| 4 | `RL_CHANNEL_FULL` | `max_peers_per_channel` reached |
| 5 | `RL_CHANNEL_QUOTA` | per-IP channel limit reached |
| 6 | `RL_ALIAS_TAKEN` | alias already subscribed on this channel |
| 7 | `RL_NOT_SUBSCRIBED` | `RelaySignal` for a channel you are not in |
| 8 | `RL_NO_SUCH_ALIAS` | `to` alias not present |
| 9 | `RL_BYTES_EXCEEDED` | per-alias bytes/s exceeded |
| 10 | `RL_LOBBY_FULL` | lobby record cap reached |
| 11 | `RL_RATE_LIMITED` | connection-level rate limit |
| 12 | `RL_IDLE_TIMEOUT` | 90 s with no traffic |
| 13 | `RL_SHUTTING_DOWN` | relay is draining; clients should reconnect elsewhere |
| 14 | `RL_UNAUTHORIZED` | relay requires a token the client did not present |

### 2.1 Client behaviour

| Code | Client action |
|---|---|
| `RL_CHANNEL_FULL`, `RL_CHANNEL_QUOTA` | fail the join with a clear message; do not retry |
| `RL_BYTES_EXCEEDED`, `RL_RATE_LIMITED` | exponential backoff, cap 30 s |
| `RL_IDLE_TIMEOUT`, `RL_SHUTTING_DOWN` | immediate reconnect, no backoff |
| `RL_BAD_FRAME`, `RL_UNKNOWN_MESSAGE` | do not retry; this is a client bug |
| `RL_UNAUTHORIZED` | surface to the user: the relay needs configuration |

A relay failure MUST NOT tear down an established mesh. The relay is only
needed for new joins and reconnects
([connectivity.md](../design/connectivity.md) §6).

---

## 3. Local codes (`L_`)

These never travel on the wire. They appear in `UiEvent::Error` and in
`PlayerEvent::Error` ([core-api.md](core-api.md) §5).

### 3.1 Player (`L_PLAYER_*`)

| Code | Name | Meaning | Core response |
|---:|---|---|---|
| 100 | `L_PLAYER_INIT` | `MediaSource` / `SourceBuffer` creation failed | retry once, then fail the stream |
| 101 | `L_PLAYER_CODEC_UNSUPPORTED` | `isTypeSupported` false for the stream's codecs | report to the room; this peer cannot play |
| 102 | `L_PLAYER_APPEND_REJECTED` | `appendBuffer` threw (not quota) | flush, re-create `SourceBuffer` |
| 103 | `L_PLAYER_QUOTA` | `QuotaExceededError` | `Evict` then retry once |
| 104 | `L_PLAYER_DECODE` | decoder error mid-stream | seek to segment start, retry; if repeat, fail |
| 105 | `L_PLAYER_SEEK_FAILED` | seek did not complete | fall back to nearest keyframe |
| 106 | `L_PLAYER_MPV_EXITED` | mpv process died | respawn once, then fall back |
| 107 | `L_PLAYER_MPV_IPC` | JSON-RPC failure | respawn the socket |
| 108 | `L_PLAYER_AUTOPLAY_BLOCKED` | browser refused playback without a gesture | surface the "click to watch" gate |
| 109 | `L_PLAYER_APPEND_TIMEOUT` | no `updateend` within 10 s | pause, re-create `SourceBuffer` |

### 3.2 Storage (`L_STORE_*`)

| Code | Name | Meaning |
|---:|---|---|
| 200 | `L_STORE_IO` | OS/OPFS write failed |
| 201 | `L_STORE_QUOTA` | quota exhausted; eviction could not free enough |
| 202 | `L_STORE_CORRUPT` | checksum or journal replay failed |
| 203 | `L_STORE_SCRUB_FAILED` | a stored piece failed re-verification |
| 204 | `L_STORE_UNAVAILABLE` | no usable backend; degrade to streaming-only |

A `L_STORE_*` error MUST NOT stop playback. The core drops to memory-only
mode and continues ([storage.md](../design/storage.md) §2).

### 3.3 Media pipeline (`L_MEDIA_*`)

| Code | Name | Meaning |
|---:|---|---|
| 300 | `L_MEDIA_NO_FFMPEG` | ffmpeg/ffprobe not found |
| 301 | `L_MEDIA_PROBE_FAILED` | ffprobe rejected the file |
| 302 | `L_MEDIA_UNSUPPORTED_CODEC` | no pipeline can produce a room-playable stream |
| 303 | `L_MEDIA_ENCODER_FAILED` | ffmpeg exited non-zero |
| 304 | `L_MEDIA_BOX_TOO_LARGE` | fMP4/EBML box exceeded the limits table |
| 305 | `L_MEDIA_NO_KEYFRAMES` | segmenter cannot find keyframes at the target interval |
| 306 | `L_MEDIA_UNDERRUN` | encoder fell behind the playhead |
| 307 | `L_MEDIA_TIMEOUT` | probe exceeded 30 s |
| 308 | `L_MEDIA_MOOV_MISSING` | MP4 with no seekable `moov` |

### 3.4 Network / platform (`L_NET_*`, `L_PLATFORM_*`)

| Code | Name | Meaning |
|---:|---|---|
| 400 | `L_NET_NO_TURN` | a peer pair cannot connect and no TURN is configured |
| 401 | `L_NET_ICE_FAILED` | ICE failed for one peer; room continues |
| 402 | `L_NET_STUN_UNREACHABLE` | gathering produced host candidates only |
| 403 | `L_NET_RELAY_UNREACHABLE` | relay down; new joins impossible |
| 404 | `L_NET_DATACHANNEL_FAILED` | SCTP channel open failed |
| 500 | `L_PLATFORM_NO_MSE` | browser lacks MSE; cannot play |
| 501 | `L_PLATFORM_NO_OPFS` | no OPFS; degrade to memory-only |
| 502 | `L_PLATFORM_NO_WORKER` | Worker unavailable; cannot run the core |
| 503 | `L_PLATFORM_BACKGROUND` | tab hidden and throttled (informational) |
| 504 | `L_PLATFORM_XDG` | config/cache dirs unavailable |

---

## 4. `DisconnectReason` (in-process)

`Event::PeerDisconnected { reason }` ([core-api.md](core-api.md) §2) uses a
separate, richer enum because the core needs to distinguish transport facts
from peer decisions:

```rust
pub enum DisconnectReason {
    Bye(ByeCode),
    IceFailed,            // connectivity checks timed out
    ConsentExpired,       // DTLS consent freshness failed
    ChannelClosed,        // shell closed it
    RelayLost,            // only affects signaling, not this mesh edge
    LocalShutdown,
}
```

Only `Bye(PW_PROTOCOL_VIOLATION | PW_AUTHORITY_VIOLATION | PW_CBOR_INVALID)`
and `Bye(PW_BANNED)` affect the roster's trust state. `IceFailed` and
`ConsentExpired` are the normal host-loss path
([sync.md](../design/sync.md) §6) and MUST NOT be scored as misbehaviour —
a flaky network is not an attack.

---

## 5. Strike ladder

Codes map to the ladder in [security.md](../design/security.md) §4:

| Evidence | Action |
|---|---|
| Bad block proof, with valid `sender_sig` | instant local ban |
| `PW_AUTH_FAILED`, forged envelope `sig` | instant local ban |
| `PW_AUTHORITY_VIOLATION`, `PW_PROTOCOL_VIOLATION`, `PW_CBOR_INVALID` | strike; 3 strikes = local ban |
| `PW_RATE_LIMITED`, `PW_ENVELOPE_TOO_LARGE` | throttle; 3 within 60 s = strike |
| `IceFailed`, `ConsentExpired`, timeouts | score decay only, never a strike |

A ban is local and permanent for the room; a room-wide removal requires a
`KickCertificate` ([governance.md](../design/governance.md) §4).

---

## 6. Adding a code

- New codes are appended within the existing family range; never renumber.
- A new code is a **minor** change: an older peer that receives an unknown
  `BYE` code treats it as `PW_NORMAL` (clean exit) and logs the raw value.
  This is deliberate — an unknown disconnect code must not be interpreted as
  misbehaviour.
- Codes 100–599 are local and MUST NOT appear on the wire.
