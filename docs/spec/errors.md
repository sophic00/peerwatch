---
tier: normative
---

# Error Codes (normative)

Every failure across PeerWatch maps to a stable `u16` code in one of three
families so logs, UI messages, and `BYE` frames agree.

| Family | Prefix | Travels over | Meaning |
|---|---|---|---|
| Protocol disconnect | `PW_` | `BYE { code }` on `control` | peer exit or rejection |
| Relay | `RL_` | `RelayError { code }` | relay refusing a subscription or signal |
| Local | `L_` | `UiEvent::Error`, `PlayerEvent::Error` | local machine / platform fault |

Numeric codes are stable and MUST NOT be renumbered; human text is for display
and logs only.

---

## 1. `BYE` codes (protocol, `PW_`)

`BYE` is signed and sent on `control` before closing a connection
([protocol.md](protocol.md) §7.1). A clean `BYE(PW_NORMAL)` prunes the peer
from the active roster immediately. Closing without `BYE` is treated as a
crash (`IceFailed` / `ConsentExpired`).

| Code | Name | When | Reconnectable? |
|---:|---|---|---|
| 0 | `PW_NORMAL` | clean exit, room left | n/a |
| 1 | `PW_VERSION_MISMATCH` | major version differs | after upgrade |
| 2 | `PW_UNSUPPORTED_MESSAGE` | unknown required message type | after upgrade |
| 3 | `PW_AUTH_FAILED` | `auth_tag` or `auth_sig` invalid | no |
| 4 | `PW_BANNED` | peer is on a verified `KICK_CERT` banlist | no |
| 5 | `PW_NOT_INVITED` | no valid `MEMBER_ADD` chain | only with fresh invite |
| 6 | `PW_ROOM_FULL` | active roster limit reached | after someone leaves |
| 7 | `PW_PROTOCOL_VIOLATION` | malformed / out-of-state message | no (strike) |
| 8 | `PW_AUTHORITY_VIOLATION` | valid signature, wrong host/term/mode/cert | no (strike) |
| 9 | `PW_RATE_LIMITED` | byte or message budget exceeded | yes, after backoff |
| 10 | `PW_ENVELOPE_TOO_LARGE` | exceeds negotiated `max_message_size` | yes |
| 11 | `PW_CBOR_INVALID` | non-canonical CBOR or depth/size limit failure | no |
| 12 | `PW_TIMEOUT` | keepalive or handshake timeout | yes |
| 13 | `PW_HOST_LOST` | stream abandoned after host loss without takeover | yes |
| 14 | `PW_STREAM_REPLACED` | a newer stream took the lease | n/a |
| 15 | `PW_MODE_MISMATCH` | collaborative-only message sent in democratic room | no (strike) |
| 16 | `PW_INTERNAL` | local fault | yes |

### 1.1 Misbehaviour vs normal faults

Only `PW_PROTOCOL_VIOLATION`, `PW_AUTHORITY_VIOLATION`, `PW_CBOR_INVALID`,
`PW_MODE_MISMATCH`, and repeated `PW_ENVELOPE_TOO_LARGE` / `PW_RATE_LIMITED`
feed the strike ladder (§5). Network drops (`IceFailed`, `ConsentExpired`) and
`PW_TIMEOUT` are never strikes.

---

## 2. Relay codes (`RL_`)

`RelayError { code, message }` ([protocol.md](protocol.md) §11.1). `message`
MUST NOT contain aliases, channel hashes, or IPs.

| Code | Name | Meaning |
|---:|---|---|
| 0 | `RL_OK` | reserved |
| 1 | `RL_BAD_FRAME` | invalid CBOR relay message |
| 2 | `RL_UNKNOWN_MESSAGE` | unknown relay message type |
| 3 | `RL_FRAME_TOO_LARGE` | envelope > 32 KiB |
| 4 | `RL_CHANNEL_FULL` | `max_peers_per_channel` reached |
| 5 | `RL_CHANNEL_QUOTA` | per-IP channel quota reached |
| 6 | `RL_ALIAS_TAKEN` | alias already active on channel |
| 7 | `RL_NOT_SUBSCRIBED` | `RelaySignal` for unsubscribed channel |
| 8 | `RL_NO_SUCH_ALIAS` | target alias not online |
| 9 | `RL_BYTES_EXCEEDED` | per-alias 64 KiB/s limit exceeded |
| 10 | `RL_RATE_LIMITED` | connection rate limit exceeded |
| 11 | `RL_IDLE_TIMEOUT` | 90 s with no traffic |
| 12 | `RL_SHUTTING_DOWN` | relay draining; reconnect |
| 13 | `RL_UNAUTHORIZED` | missing/invalid optional relay bearer token |

A relay error MUST NOT disconnect an established P2P mesh.

---

## 3. Local codes (`L_`)

Codes 100–599 are local-only and MUST NOT travel on the wire.

### 3.1 Player (`L_PLAYER_*`)

| Code | Name | Meaning | Core response |
|---:|---|---|---|
| 100 | `L_PLAYER_INIT` | `MediaSource` / `SourceBuffer` init failed | retry once, then fail stream |
| 101 | `L_PLAYER_CODEC_UNSUPPORTED` | `isTypeSupported` false for stream MIME | report `PROPOSAL_COMPAT(false)` |
| 102 | `L_PLAYER_APPEND_REJECTED` | `appendBuffer` error (non-quota) | flush & recreate `SourceBuffer` |
| 103 | `L_PLAYER_QUOTA` | `QuotaExceededError` | `Evict` then retry once |
| 104 | `L_PLAYER_DECODE` | decoder fault mid-stream | seek to segment start, retry once |
| 105 | `L_PLAYER_SEEK_FAILED` | seek did not settle | snap to nearest keyframe |
| 106 | `L_PLAYER_MPV_EXITED` | mpv process exited unexpectedly | respawn once |
| 107 | `L_PLAYER_MPV_IPC` | mpv JSON-RPC socket fault | reconnect/respawn |
| 108 | `L_PLAYER_AUTOPLAY_BLOCKED` | browser requires click gesture | show "Click to watch" overlay |
| 109 | `L_PLAYER_APPEND_TIMEOUT` | no `updateend` within 10 s | pause & recreate `SourceBuffer` |

### 3.2 Storage (`L_STORE_*`)

| Code | Name | Meaning |
|---:|---|---|
| 200 | `L_STORE_IO` | OS/OPFS I/O failure |
| 201 | `L_STORE_QUOTA` | quota exhausted after eviction |
| 202 | `L_STORE_CORRUPT` | index/checksum verification failed |
| 203 | `L_STORE_SCRUB_FAILED` | stored segment failed Merkle scrub |
| 204 | `L_STORE_UNAVAILABLE` | no durable store; degrade to RAM-only |

Storage errors MUST NOT stop playback; the peer degrades cleanly to RAM-only
streaming.

### 3.3 Media pipeline (`L_MEDIA_*`)

| Code | Name | Meaning |
|---:|---|---|
| 300 | `L_MEDIA_NO_FFMPEG` | ffmpeg/ffprobe not found |
| 301 | `L_MEDIA_PROBE_FAILED` | ffprobe rejected the file |
| 302 | `L_MEDIA_UNSUPPORTED_CODEC` | cannot produce room-playable stream |
| 303 | `L_MEDIA_ENCODER_FAILED` | ffmpeg exited non-zero (HW fallback also failed) |
| 304 | `L_MEDIA_BOX_TOO_LARGE` | fMP4/EBML box exceeded limits (§9.1 of schema) |
| 305 | `L_MEDIA_NO_KEYFRAMES` | unable to segment on keyframes |
| 306 | `L_MEDIA_UNDERRUN` | live transcode fell behind playhead |
| 307 | `L_MEDIA_TIMEOUT` | probe exceeded 30 s |
| 308 | `L_MEDIA_MOOV_MISSING` | MP4 missing seekable `moov` |

### 3.4 Network and platform (`L_NET_*`, `L_PLATFORM_*`)

| Code | Name | Meaning |
|---:|---|---|
| 400 | `L_NET_NO_TURN` | direct path failed and no TURN configured |
| 401 | `L_NET_ICE_FAILED` | ICE failed for one peer |
| 402 | `L_NET_STUN_UNREACHABLE` | host candidates only |
| 403 | `L_NET_RELAY_UNREACHABLE` | relay down; new joins unavailable |
| 404 | `L_NET_DATACHANNEL_FAILED` | SCTP channel failed to open |
| 500 | `L_PLATFORM_NO_MSE` | browser lacks MSE |
| 501 | `L_PLATFORM_NO_OPFS` | browser lacks OPFS; degrade to RAM-only |
| 502 | `L_PLATFORM_NO_WORKER` | Worker unavailable |
| 503 | `L_PLATFORM_BACKGROUND` | hidden tab throttled (informational) |
| 504 | `L_PLATFORM_XDG` | config/cache directory unavailable |

---

## 4. `DisconnectReason` (in-process)

```rust
pub enum DisconnectReason {
    Bye(ByeCode),
    IceFailed,
    ConsentExpired,
    ChannelClosed,
    LocalShutdown,
}
```

`IceFailed` and `ConsentExpired` trigger the host-loss path when the lost peer
is the current Host; they never add strikes.

---

## 5. Strike ladder

| Evidence | Action |
|---|---|
| Bad `BLOCK` Merkle proof or wrong block length | **instant local ban** |
| `PW_AUTH_FAILED` or invalid Ed25519 envelope signature | **instant local ban** |
| `PW_AUTHORITY_VIOLATION`, `PW_PROTOCOL_VIOLATION`, `PW_CBOR_INVALID`, `PW_MODE_MISMATCH` | strike; **3 strikes = local ban** |
| `PW_RATE_LIMITED`, `PW_ENVELOPE_TOO_LARGE` | throttle; 3 in 60 s = strike |
| `IceFailed`, `ConsentExpired`, timeouts | reliability score decay only |

Unknown `BYE` codes received from newer peers are logged and treated as
`PW_NORMAL`.
