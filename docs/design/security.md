---
tier: design
---

# Security & Threat Model

PeerWatch targets **small groups of 5–16 people who know each other** watching
local video over an adversarial internet. This document defines our adversaries,
defense layers, and honest boundaries. Normative rules live in
[protocol.md](../spec/protocol.md) §4–§11.

---

## 1. Adversaries

- **A. Internet outsider**: no invite, tries to discover/join/snoop on rooms.
- **B. Relay operator**: runs `pw-relay`, sees WebSocket connections, opaque
  channel hashes, aliases, packet sizes, timing, and IPs.
- **C. Network MITM / ISP**: observes or tampers with traffic between peers.
- **D. Malicious room member**: holds a valid invite and `room_secret`, tries
  to poison video bytes, spoof clock/host authority, equivocate votes, or DoS.
- **E. Malicious content**: crafted media file targeting ffmpeg/mpv/MSE parsers.
- **F. Browser XSS**: same-origin script reading IndexedDB keys.

---

## 2. Defense layers

| Layer | Mechanism | Defeats |
|---|---|---|
| L0 Transport | DTLS 1.2/1.3 (ephemeral forward-secret certs via `str0m`/WebRTC) | C, A, passive capture |
| L1 Admission | Signed expiring `InviteGrant` + encrypted `InviteEnvelope` (zero cleartext room ID) | A, B |
| L2 Signaling | Per-message ephemeral X25519 + XChaCha20-Poly1305 `SignalEnvelope` + self-signed `PEER_SIGNAL_KEY` | B (SDP/MITM), D (snooping) |
| L3 Identity | Ed25519 + single-signature DTLS-bound `AUTH` + `MemberAdd` chain | D (impersonation), B (splice) |
| L4 Authority | CRDT signed facts, `HostLease(term)`, `VoteCertificate`, `TakeoverCertificate` | D (clock/banlist/vote forgery) |
| L5 Content | Per-block BLAKE3 Merkle proof against `segment_root` | D (data poisoning) |
| L6 Availability | Pre-allocation parser caps, byte-rate token buckets, strike ladder, aggregate SCTP pacing | D (flood/OOM/bufferbloat) |
| L7 Social | Majority `KICK` certificates + persistent CRDT banlist | D (griefing) |

---

## 3. Authentication vs authorization

An Ed25519 signature proves *who* sent a message, never *whether they have
authority*. Every receiver enforces:

- `SYNC`, `START_AT`, `MANIFEST_HEAD/APPEND/FINAL` → must match current
  `HostLease` (`host_pubkey`, `stream_id`, `term`) with monotonic `seq`.
- `CONTROL_REQ`, `QUEUE_ADD`, `QUEUE_REMOVE` → valid only when
  `genesis.mode == COLLABORATIVE`.
- `KICK_NOTICE` → must carry a verified majority `KickCertificate`.
- `BLOCK` → must verify against a known `segment_root` with exact expected
  block length; invalid block → **instant local ban** of the sender.

---

## 4. Per-adversary analysis and honest limitations

- **Relay operator (B)**: sees opaque `channel` hashes, per-room `alias`es,
  ephemeral `eph_pub`, sizes, timing, and client IPs. Cannot read `room_id`,
  `invite_id`, SDP, ICE candidates, media, or chat, and cannot substitute
  `signal_pub` (keys are self-signed by peers and never sourced from the
  relay). *Residual*: can deny service, omit online aliases, or log client IPs.
- **Kicked peer (D)**: because v3 uses **Option B** (no `room_secret`
  rotation), the load-bearing barrier is the CRDT `KickCertificate` banlist
  enforced at both signaling receipt and `AUTH` ([identity.md](identity.md) §3).
  Every peer permanently refuses the banned keypair. All P2P and signaling
  traffic remains forward-secret via ephemeral DTLS and ephemeral X25519.
- **Malicious content (E)**: browser MSE runs inside the browser media sandbox.
  Native `mpv` and `ffmpeg` do not — `pw-node` enforces strict pre-allocation
  box/EBML parser limits ([protocol.md](../spec/protocol.md) §9.1), invokes
  `ffmpeg` via argument vectors only, restricts `mpv` protocol whitelists, and
  sandboxes `mpv` with `bubblewrap`/`firejail` when available.
- **Browser XSS (F)**: private keys live in Worker-only IndexedDB; the web app
  ships with a strict CSP (`script-src 'self'`, no remote scripts, no `eval`).
- **Scope limits**: no anonymity against global traffic analysis; no BFT
  protection against a colluding strict majority of the room; a member who
  leaks `room_secret` or mints Sybil invites is a social/invite-hygiene issue.
