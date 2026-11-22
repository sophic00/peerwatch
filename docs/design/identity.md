---
tier: design
---

# Identity, Authentication, and Kick Exclusion

PeerWatch uses local cryptography with no accounts, no passwords, and no
trusted server. Normative formats live in [protocol.md](../spec/protocol.md)
§4 and §11.

---

## 1. Per-installation keys

On first run, every peer generates:

- **Ed25519 signing keypair** (`sk`, `pk`) →
  `PeerId = base32(BLAKE3("pw-peer-v3" || pk))` (`pw:<8 chars>…`).
- **X25519 encryption keypair** (`esk`, `epk`) — plus an ephemeral per-room
  session `signal_pub` self-signed as a `PEER_SIGNAL_KEY` record so static
  encryption keys are never exposed to the relay.

### 1.1 Key storage

| Platform | Location | Notes |
|---|---|---|
| Native (`pw-cli` / `pw-node`) | `$XDG_CONFIG_HOME/peerwatch/identity.key` (`0600`) | Local file; optional passphrase-at-rest planned |
| Browser (`pw-wasm`) | IndexedDB (`pw-identity`), accessed only inside Worker | Bound to origin; strict CSP required against XSS |

---

## 2. Single-signature DTLS-bound AUTH

WebRTC DTLS authenticates the transport with ephemeral self-signed certs;
PeerWatch layers durable Ed25519 identity and room membership on top via
`HELLO` → `AUTH` on `control`:

```
I → R: HELLO { version: 3, pubkey_I, enc_pubkey_I, nonce_I, caps_I, max_msg }
R → I: HELLO { version: 3, pubkey_R, enc_pubkey_R, nonce_R, caps_R, max_msg }
I → R: AUTH  { auth_tag, auth_sig_I }
R → I: AUTH  { auth_tag, auth_sig_R }
```

- The transcript binds `room_id`, both Ed25519 and X25519 keys in role order
  (`I` then `R`), both nonces, both DTLS certificate fingerprints, both
  canonical `Caps`, and `version`.
- `auth_tag = HMAC-SHA256(room_secret, "pw-auth-tag-v3" || transcript)` proves
  possession of the room secret.
- `auth_sig = Ed25519(sk, "pw-auth-sig-v3" || transcript || auth_tag)` is the
  **only** signature on `AUTH` (the outer envelope is unsigned for `AUTH`).
- Both sides also verify `pubkey ∉ banlist` (`KICK_CERT` set) and that `pubkey`
  has a valid `MemberAdd` chain from `RoomGenesis`.

---

## 3. Invites and honest kick exclusion (Option B)

### 3.1 Invites

An invite token (`pw3_<room_id>.<invite_id>.<secret>`) is a signed, expiring,
usage-capped capability (`InviteGrant`).

- `InviteEnvelope` encrypts `JoinRequest` with XChaCha20-Poly1305 under
  `invite_key` and exposes **no** `room_id` or `invite_id` in cleartext.
- The inviter checks expiry, usage count, optional `invitee_pubkey` binding,
  and HMAC proof, mints a signed `MemberAdd`, and replies with `JoinAccept`
  encrypted to the invitee's ephemeral `join_pub`.

### 3.2 Why v3 omits room-key rotation and lobby channels

A room has one `room_secret` generated at genesis and never rotated:

- **Forward secrecy is already guaranteed by the transport and signaling
  layers**: P2P streams use ephemeral per-connection DTLS, and signaling uses
  per-message ephemeral X25519. `room_secret` never encrypts video, chat, or
  SDP payloads.
- **Banlist + AUTH is the single, honest exclusion barrier**: when a `KICK`
  certificate passes, every member records the `KickCertificate` in its CRDT
  banlist, closes the WebRTC connection (`BYE(PW_BANNED)`), drops all future
  `SignalEnvelope`s from that peer, and rejects any future `AUTH` from that
  key.
- **Offline members rejoin effortlessly** without a stateful relay lobby or
  epoch-catchup races: when a non-banned member reconnects, every peer accepts
  its `AUTH` immediately and reconciles missed CRDT records via `STATE_DIGEST`
  / `STATE_SYNC`.

A kicked person can only re-enter by creating a fresh keypair **and** obtaining
a brand-new invite from an active member — a social decision, visibly shown as
a brand-new identity.

---

## 4. Algorithm inventory

| Purpose | Algorithm |
|---|---|
| Signatures | Ed25519 (`ed25519-dalek`) |
| Key agreement | X25519 (`x25519-dalek`) |
| Hashing, IDs, segment Merkle trees | BLAKE3 (`blake3`) |
| AEAD (signaling, invites) | XChaCha20-Poly1305 (`chacha20poly1305`) |
| Key derivation & MACs | HKDF-SHA256 / HMAC-SHA256 |
| Transport encryption | DTLS 1.2/1.3 (via `str0m` / WebRTC) |
