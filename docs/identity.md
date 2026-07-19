# Identity & Authentication

Two different questions hide inside "who are you?":

1. **Identity authentication**: are you the *same peer* you claim to be?
   (Needed for kick-bans that stick, signed votes, and host rejoin.)
2. **Room authorization**: are you *allowed in this room* at all?
   (Needed so strangers can't wander in.)

PeerWatch answers both with cryptography that runs locally on each device,
with no accounts, no passwords, no server-side state.

---

## 1. Identity: a per-installation Ed25519 keypair

The first time any PeerWatch client runs (browser, desktop, or CLI), it
generates an **Ed25519 keypair**:

- **Private key** — 32 bytes, never leaves the device.
- **Public key** — 32 bytes, this *is* the peer's identity.
- **Peer ID** — `base32(blake3(public_key))`, conventionally displayed in
  short form like `pw:vk3f7q…`. Stable across sessions, IP changes, and
  rejoins.

**Why Ed25519:** keys are 32 bytes and signatures 64 bytes (they fit
comfortably inside DataChannel messages), signing and verification are fast
(~50 µs / ~70 µs, irrelevant at message rates), the scheme is deterministic
(no RNG pitfalls inside WASM), and `ed25519-dalek` compiles identically for
native and WASM targets.

**Why not usernames/passwords:** there is no server to check a password
against, and a password proves "you know a secret", not "you are the same
entity as yesterday". A keypair proves *continuity of identity*, which is
exactly what banlists and host-resume need. The room secret (§4) already
plays the "password" role for admission.

### 1.1 Key storage

| Platform | Where | Notes |
|----------|-------|-------|
| CLI / desktop | `$XDG_CONFIG_HOME/peerwatch/identity.key` (mode `0600`) | SSH-key-style. Passphrase-encrypted at rest is a planned option (see [roadmap.md](roadmap.md)). |
| Browser | IndexedDB (`pw-identity` store) | Bound to the origin, cleared if site data is cleared (a *new* identity is then generated, which is acceptable: identity is a continuity device, not a legal name). |

Threat-model note: malware that can read your config dir / browser profile
owns your identity. This is the same exposure class as SSH private keys and
is documented in [security.md](security.md).

---

## 2. Proving identity: challenge–response (from first principles)

A peer cannot simply *say* "my public key is K". Anyone could copy K. It
must **prove possession of the private key** without ever transmitting it.
The standard tool is a **challenge–response signature**: you hand me a fresh
random value, I sign it, you verify the signature against my claimed public
key. Because the value is fresh, a recording of my old signature is useless
(**replay protection**).

### 2.1 Why our own layer on top of DTLS

WebRTC's DTLS handshake already authenticates the *connection*, but with
**fresh, self-signed, per-connection certificates**. It proves "you are the
same entity I exchanged signaling with five seconds ago", nothing more. It
says nothing about identity *across sessions*. So we keep DTLS for transport
security (it is excellent at that) and add the Ed25519 handshake on top for
durable identity.

### 2.2 The AUTH exchange

Runs once per connection, on the `control` DataChannel, immediately after
the channel opens:

```
A → B:  HELLO { version, pubkey_A, nonce_A }     // 32-byte random nonce
B → A:  HELLO { version, pubkey_B, nonce_B }
A → B:  AUTH  { sig_A = Sign_skA(transcript) }
B → A:  AUTH  { sig_B = Sign_skB(transcript) }

transcript = "pw-auth-v1"
           ‖ channel_hash
           ‖ nonce_A ‖ nonce_B
           ‖ dtls_fingerprint_A ‖ dtls_fingerprint_B
```

Each side verifies the other's signature against the claimed pubkey. Three
details make this robust:

1. **Both nonces are in the transcript** → neither side can replay an old
   AUTH. Each side contributed freshness.
2. **The DTLS fingerprints are in the transcript** → the application-layer
   identity is **bound to the transport**. Even the signaling relay (which
   forwarded the SDP containing those fingerprints) cannot splice itself
   between two peers and later claim one of their identities.
3. **Verification is independent and stateless** → every peer checks every
   signature itself, no one takes anyone's word about a third party. The
   same property later makes votes, SYNC messages, and takeovers
   independently verifiable.

---

## 3. Room authorization: the invite token is the capability

Identity says *who* you are, it says nothing about whether you're *welcome*.
That is the room secret's job.

### 3.1 Token format

```
pw_<room_id>.<secret>

room_id : 128-bit random, base32          → which room
secret  : 256-bit random, base64url       → the capability
```

The creator generates both, the token is shared out-of-band (encrypted
messenger, in person, …). **Possession of the token = eligibility to join.**

### 3.2 Derivation

From the token, every invitee derives via HKDF-SHA256:

```
channel_hash = blake3("pw-channel" ‖ room_id)   // signaling mailbox name
room_psk     = HKDF(secret, info="pw-room-psk") // room pre-shared key
sig_key      = HKDF(secret, info="pw-signal")   // AEAD key for signaling envelopes
```

- `channel_hash` tells the relay which mailbox to use, without revealing
  the room ID to the relay operator.
- `sig_key` AEAD-encrypts all signaling envelopes → the relay shuttles
  ciphertext it cannot read.
- `room_psk` is mixed into the AUTH handshake: the signed transcript is
  additionally MAC'd with a key derived from `room_psk`. A peer that doesn't
  know the secret **cannot produce a valid AUTH**, even if it somehow learns
  the channel name. Handshake fails → connection dropped.

### 3.3 What this yields

| Property | Mechanism |
|----------|-----------|
| Only invitees can join | room_psk required for a valid AUTH |
| Relay can't join or read | never sees room_id or secret, sees hashes + ciphertext |
| Identity survives rejoin | same keypair → same peer ID |
| Kicks stick | banlist stores pubkeys (see below) |
| No server trust required | all proofs are peer-to-peer |

---

## 4. Consequences for the rest of the system

- **Kick bans**: a successful kick vote adds the target's **public key** to
  the room banlist (persisted per room, gossiped to peers). Rejoining with
  the token but the banned key fails AUTH at every peer. The mesh refuses
  the connection. Returning under a *fresh* key is possible but is visibly a
  *different person* (new peer ID) and still requires the token, see the
  sybil discussion in [security.md](security.md).
- **Host rejoin**: if the Host drops and returns with the same key, the room
  recognizes the peer ID and may restore its role ([sync.md](sync.md) §6).
- **Signatures everywhere**: PROPOSAL, VOTE, SYNC, SOURCE_TAKEOVER,
  BAN_REPORT, CHAT. Every control message carries its author's signature,
  so forgery and impersonation are out by construction
  ([protocol.md](protocol.md)).

---

## 5. Honest limitations

- **A peer ID is not a legal identity.** It means "same key as before", not
  "this is Alice". Mitigations: a TOFU ("trust on first use") known-peers
  list so clients can display "you've watched with `pw:vk3f…` 12 times", and
  optional out-of-band fingerprint comparison for paranoid groups
(Signal-style safety numbers). Both are post-v1 niceties, the design
   leaves room for them.
- **Token leaks = room leaks.** Anyone holding the token can join with a
  fresh key. Defense is social (share carefully) plus kick votes, expiring
  tokens are a possible extension.
- **Key theft = identity theft.** Disk-level attackers get the identity
  (same class as SSH keys). Passphrase-at-rest is planned
  ([roadmap.md](roadmap.md)).

---

## 6. Algorithm inventory

| Use | Algorithm | Why |
|-----|-----------|-----|
| Signatures | Ed25519 | tiny, fast, deterministic, native+WASM |
| Hashing / IDs | BLAKE3 | fast, modern, single primitive for content IDs, peer IDs, channel names |
| Piece integrity | SHA-256 | compatibility and audit familiarity (v1 behavior), fast with SHA-NI |
| Key derivation | HKDF-SHA256 | standard, simple |
| Envelope encryption | XChaCha20-Poly1305 | AEAD, nonce-misuse-resistant enough for our per-message random nonces, fast in software |
| Transport encryption | DTLS 1.2/1.3 (via WebRTC) | mandatory, free, mutually authenticated |
