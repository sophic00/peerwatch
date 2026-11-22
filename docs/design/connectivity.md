---
tier: design
---

# Connectivity — `str0m`, WebRTC, and Stateless Signaling

PeerWatch connects every peer directly over encrypted WebRTC DataChannels,
using a stateless signaling relay only to exchange encrypted SDP/ICE messages.
Normative relay and envelope formats live in [protocol.md](../spec/protocol.md)
§11.

---

## 1. Transport stack: `str0m` (native) ↔ `RTCPeerConnection` (browser)

- **Browser peers** use the built-in `RTCPeerConnection` on the main thread
  (transferred into the WASM Worker via `MessagePort`).
- **Native peers (`pw-node`)** use **`str0m`**, a pure-Rust, Sans-I/O WebRTC
  stack (ICE + DTLS + SCTP + SRTP).
- Why `str0m` fits PeerWatch:
  - Matches `pw-core`'s exact Sans-I/O design (`handle_input` / `poll_output`),
    allowing the real WebRTC stack to run cleanly inside Tokio or inside
    deterministic test harnesses.
  - Small dependency surface, explicit DTLS certificate fingerprint access for
    `AUTH`, direct visibility into SCTP send-buffer pressure, and clean Opus
    media track support for v2 voice chat.

---

## 2. ICE, STUN, and TURN

1. **Host candidates** connect peers on the same LAN directly.
2. **Server-reflexive candidates** via STUN punch UDP holes through cone NATs
   (~85–90% of consumer links).
3. **Relayed candidates** via TURN (`coturn`, using short-lived
   `use-auth-secret` HMAC credentials over UDP 3478 and TLS 5349) rescue
   symmetric NATs and restrictive firewalls. TURN forwards DTLS ciphertext it
   cannot decrypt.

If a peer pair cannot hole-punch and no TURN server is configured, the client
surfaces `L_NET_NO_TURN` clearly instead of hanging silently.

---

## 3. DataChannels and aggregate SCTP backpressure

Every peer connection opens three DataChannels:

| Channel | SCTP Mode | Carries |
|---|---|---|
| `control` | reliable, ordered | `HELLO`/`AUTH`, `STATE_DIGEST`, `HOST_LEASE`, `AVAILABILITY`, `SYNC`, `CONTROL_REQ`, governance, queue, chat |
| `bulk` | reliable, ordered | `STATE_SYNC`, `MANIFEST_*`, WebVTT subtitles |
| `data` | reliable, **unordered** | `REQUEST`, `BLOCK`, `CANCEL`, `CHALLENGE_RESPONSE` |

Because all three channels share one SCTP association and congestion window,
filling the send queue on `data` or `bulk` introduces queueing delay onto
`control` (`SYNC`/`PING`). Both `str0m` and browser adapters therefore report
**aggregate** `bufferedAmount` across `data` + `bulk`:

- Pause `Data`/`Bulk` sends above **256 KiB** (`high_watermark`).
- Resume below **128 KiB** (`low_watermark`).
- Never throttle `Control` behind `Data`/`Bulk`.

---

## 4. Stateless signaling relay (`pw-relay`)

The relay is a tiny in-memory WebSocket mailbox with **zero disk storage** and
**zero message retention** (no lobby channel, no epoch history).

| Visible to relay | Hidden from relay |
|---|---|
| Opaque 32 B `channel` hashes (`room_channel` / `invite_channel`) | `room_id`, `invite_id`, `room_secret` |
| 16 B per-room `alias` values | Stable `PeerId` / Ed25519 keys |
| Per-message ephemeral `eph_pub`, sizes, timing, source IPs | Plaintext SDP, ICE candidates, media, chat, votes |

`alias = HMAC-SHA256(room_secret, "pw-alias-v3" || peer_id)[0..16]`, so the
same peer produces unrelated aliases in different rooms.

---

## 5. Deadlock-free join sequence

```mermaid
sequenceDiagram
    autonumber
    participant J as Joiner J
    participant R as pw-relay (stateless)
    participant A as Inviter A
    participant B as Member B

    J->>R: RelaySubscribe(invite_channel, rand_alias)
    J->>R: InviteEnvelope (no cleartext room_id/invite_id)
    R->>A: opaque InviteEnvelope
    A->>A: verify InviteGrant, expiry, uses, HMAC proof; mint MemberAdd(J)
    A->>R: JoinAcceptEnvelope (encrypted to J's ephemeral join_pub)
    R->>J: opaque JoinAcceptEnvelope
    J->>J: verify genesis, MemberAdd chain, signal_keys; learn room_secret
    J->>R: RelaySubscribe(room_channel, alias_J)
    J->>A: SignalEnvelope(SDP offer + sender_signal_pub + MemberAdd(J))
    J->>B: SignalEnvelope(SDP offer + sender_signal_pub + MemberAdd(J))
    Note over B: B decrypts, verifies MemberAdd(J) + J's sig,<br/>caches J's sender_signal_pub immediately
    B->>J: SignalEnvelope(SDP answer encrypted to J's sender_signal_pub)
    A->>J: SignalEnvelope(SDP answer)
    Note over J,B: ICE + DTLS + SCTP established
    J<-->B: HELLO / AUTH (DTLS fingerprint bound)
    J<-->B: STATE_DIGEST / STATE_SYNC (CRDT reconciliation)
```

Because `J` includes its signed `MemberAdd` and its own `sender_signal_pub`
inside its encrypted, Ed25519-signed initial `SignalEnvelope`, `B` can verify
`J`'s authorization and encrypt the SDP answer immediately — without waiting
for any Host snapshot and without trusting the relay for any public keys.

---

## 6. Topology, disconnects, and relay resilience

- **Full mesh (5–16 peers)**: at 16 peers, each client holds 15 connections.
- **Clean leave (`BYE`)**: signed `BYE(PW_NORMAL)` immediately prunes the peer
  from the active roster so future quorums shrink cleanly.
- **Unclean drop**: ICE consent freshness detects failure in ~5–15 s; if the
  lost peer was the Host, the room enters `Degraded` ([sync.md](sync.md) §6).
- **Relay outage**: never breaks an established mesh. Existing peers keep
  streaming indefinitely; only new joins and reconnects wait for relay recovery
  (or manual copy-paste SDP fallback).
