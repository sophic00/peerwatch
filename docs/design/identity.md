---
tier: design
---

# Identity & Authentication

Two different questions hide inside “who are you?”:

1. **Identity authentication**: are you the *same peer* you claim to be?
   (Needed for signed votes, banlists that stick, and host continuity.)
2. **Room authorization**: are you *allowed in this room* at all?
   (Needed so strangers can't wander in, and so a kicked peer loses access.)

PeerWatch answers both with cryptography that runs locally on each device,
with no accounts, no passwords, and no server-side identity state. The
normative transcript and token formats are in
[protocol.md](../spec/protocol.md) §4 and §11.

---

## 1. Identity: a per-installation keypair set

The first time any PeerWatch client runs (browser, desktop, or CLI), it
generates:

- an **Ed25519 signing keypair** — `sk` / `pk`;
- an **X25519 encryption keypair** — `esk` / `epk`;
- a **Peer ID** — `base32(BLAKE3("pw-peer-v2" || pk))`, displayed in short
  form like `pw:vk3f7q…`.

The signing key proves authorship. The encryption key lets other peers send
you signaling data and rekey material that only you can read. They are
separate keys with separate purposes; never reuse an Ed25519 key as an
X25519 key without a domain-separated conversion.

**Why Ed25519:** keys are 32 bytes and signatures 64 bytes (they fit
comfortably inside DataChannel messages), signing and verification are fast
(~50 µs / ~70 µs natively), the scheme is deterministic (no RNG pitfalls
inside WASM), and `ed25519-dalek` compiles identically for native and WASM
targets.

**Why X25519:** signaling envelopes and `REKEY` messages need public-key
encryption. X25519 + XChaCha20-Poly1305 is small, fast, and available in
Rust/WASM.

**Why not usernames/passwords:** there is no server to check a password
against, and a password proves “you know a secret”, not “you are the same
entity as yesterday”. A keypair proves *continuity of identity*, which is
what banlists and host continuity need. The room key (§4) plays the
“password” role for admission.

### 1.1 Key storage

| Platform | Where | Notes |
|----------|-------|-------|
| CLI / desktop | `$XDG_CONFIG_HOME/peerwatch/identity.key` (mode `0600`, directory `0700`) | SSH-key-style. Passphrase-encrypted at rest is a planned option ([roadmap.md](../roadmap.md)). |
| Browser | IndexedDB (`pw-identity` store) | Bound to the origin, cleared if site data is cleared (a *new* identity is then generated). |

Threat-model note: malware that can read your config dir / browser profile
owns your identity. In a browser, any same-origin script (XSS) can read
IndexedDB and exfiltrate the key. This is the same exposure class as SSH
private keys; the web UI therefore needs a strict CSP, no remote scripts,
and the identity key should never be exposed to page JavaScript outside the
Worker bridge ([security.md](security.md) §5 F).

---

## 2. Proving identity: challenge–response (from first principles)

A peer cannot simply *say* “my public key is K”. Anyone could copy K. It
must **prove possession of the private key** without ever transmitting it.
The standard tool is a **challenge–response signature**: you hand me a fresh
random value, I sign it, you verify the signature against my claimed public
key. Because the value is fresh, a recording of my old signature is useless
(**replay protection**).

### 2.1 Why our own layer on top of DTLS

WebRTC's DTLS handshake already authenticates the *connection*, but with
**fresh, self-signed, per-connection certificates**. It proves “you are the
same entity I exchanged signaling with five seconds ago”, nothing more. It
says nothing about identity *across sessions*. So we keep DTLS for transport
security and add the Ed25519/X25519 handshake on top for durable identity.

### 2.2 The HELLO / AUTH exchange

Runs once per connection, on the `control` DataChannel, immediately after
the channel opens:

```
I → R:  HELLO { version, pubkey_I, enc_pubkey_I, nonce_I, caps_I, max_msg }
R → I:  HELLO { version, pubkey_R, enc_pubkey_R, nonce_R, caps_R, max_msg }
I → R:  AUTH  { auth_tag, auth_sig_I }
R → I:  AUTH  { auth_tag, auth_sig_R }
```

The exact transcript is normative:

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

room_psk = HKDF-SHA256(room_key, salt=room_id||epoch, info="pw-room-psk-v2", L=32)
auth_tag = HMAC-SHA256(room_psk, "pw-auth-tag-v2" || transcript)
sig      = Ed25519(sk, "pw-auth-sig-v2" || transcript || auth_tag)
```

Four details make this robust:

1. **Both nonces are in the transcript** → neither side can replay an old
   AUTH. Each side contributed freshness.
2. **The public keys and role labels are in the transcript** → the
   signature cannot be reflected back as the other side’s signature.
3. **The DTLS fingerprints are in the transcript** → the application-layer
   identity is bound to the transport. A signaling relay that forwarded the
   SDP cannot splice itself between two peers and later claim one of their
   identities.
4. **The room epoch is in the transcript and the MAC key** → a peer with an
   old room key cannot authenticate after a rekey.

Verification is independent and stateless: every peer checks every
signature itself. No one takes anyone’s word about a third party. But see
§5: a signature proves *who*, not *what they are allowed to do*.

---

## 3. Room authorization: invites, room keys, and epochs

Identity says *who* you are; it says nothing about whether you're
*welcome*. That is the invite/room-key system's job.

### 3.1 Invite tokens

```
pw1_<room_id>.<invite_id>.<secret>

room_id   : 128-bit random, base32
invite_id : 128-bit random, base32
secret    : 256-bit random, base64url
```

An invite is a **signed, expiring capability**, not a room-wide password:

```
InviteGrant = {
  room_id, invite_id,
  inviter_pubkey,
  expiry_ms, max_uses,
  invitee_pubkey: PublicKey / null,   // optional binding
  signature,
}
```

A member issues the grant and gives the token to the invitee out-of-band.
Possession of the token proves eligibility only until it is used or expires.
The inviter can bind it to the invitee’s public key, making it non-
transferable.

**Room creation.** The creator generates `room_id`, `room_key`, `epoch = 0`,
and a signed `ROOM_GENESIS { room_id, epoch, creator_pubkey, roster,
membership_log_root, signature }`. The genesis is the root of the
membership log. After signing it, the creator has no special privileges; it
is just the first member and an inviter.

### 3.2 Joining

The invite secret bootstraps a confidential join:

```
invite_key = HKDF(secret, salt=room_id||invite_id, info="pw-invite-v2", L=32)

JOIN_REQUEST = { room_id, invite_id, pubkey, join_pub, nonce,
                 proof = HMAC(invite_key, "pw-join-v2" || ... ) }

JOIN_ACCEPT  = { room_id, epoch, room_key,
                 roster, membership_log,
                 accepter_pubkey, signature }
```

- `JOIN_REQUEST` is encrypted under `invite_key` and sent to the invite
  channel.
- A member that holds the matching `InviteGrant` verifies the proof,
  expiry, usage count, and optional key binding.
- `JOIN_ACCEPT` is encrypted to the invitee’s ephemeral `join_pub` key and
  signed by the accepter. It carries the current room key, epoch, roster,
  and membership log.
- The invitee then joins the room channel for that epoch and runs AUTH.

If the inviter is offline, the join waits for a member holding the grant.
This is deliberate: a room should not silently admit a stranger just because
someone once shared a link.

### 3.3 Room key and epoch

- `room_key` is a 32-byte secret shared by current members.
- `epoch` increments on every rekey.
- `room_psk` for AUTH is derived from `room_key`, `room_id`, and `epoch`.
- The relay channel is `channel_hash = BLAKE3("pw-channel-v2" || room_id || epoch)`.
- **The next key is derived, not chosen:**

  ```
  room_key(e+1) = HKDF-SHA256(room_key(e),
                              salt = room_id || (e+1),
                              info = "pw-rekey-v2" || rekey_basis_hash,
                              L    = 32)
  ```

  Every member that holds epoch `e` and the basis derives the identical
  epoch-`e+1` key, so there is exactly one possible key per epoch. This is
  what makes rekey *convergent*: two members holding the same
  `KickCertificate` cannot fork the room by inventing different keys.
- `wrapped_keys` in `REKEY` is a **bootstrap convenience only**, not the
  mechanism of record. A `REKEY` whose wrapped key does not match the
  derivation is a protocol violation regardless of who sent it.
- Offline members catch up through the **lobby channel**
  (`BLAKE3("pw-lobby-v2" || room_id)`), which carries signed
  `LOBBY_BASIS` records — the `rekey_basis_hash` values of recent epochs.
  See [protocol.md](../spec/protocol.md) §4.4.3 and
  [connectivity.md](connectivity.md) §3.5.
- A rekey has a **transition window** (`rekey_grace`, default 30 s) during
  which a peer accepts both epochs; see [protocol.md](../spec/protocol.md)
  §4.4.4.

#### What key rotation does and does not achieve

Because the next key is derived from the previous one, **a kicked peer that
held `room_key(e)` can compute `room_key(e+1)`** — it has the input and the
lobby gives it the basis. This must be stated plainly:

| Barrier | Defeats a kicked peer? |
|---|---|
| `room_key(e+1)` secrecy | **No** — it can derive it |
| Relay channel rotation | **No** — `channel_hash` is computable |
| Banlist check in AUTH | **Yes** — the load-bearing barrier |

So “a kicked peer cannot rejoin” rests on every member refusing its AUTH,
not on it lacking a key. Rotation is defence-in-depth against *passive*
capture of future traffic (a network observer who recorded epoch `e`
traffic cannot decrypt epoch `e+1`), which is a real and worthwhile
property, but it is not exclusion.

Cryptographic exclusion would require the remaining members to run a fresh
Diffie-Hellman and produce a key not derivable from `room_key(e)`. That is
incompatible with offline catch-up — a peer that was not present cannot
contribute to or learn the DH result. The trade-off is recorded in
[design-log.md](design-log.md).

### 3.4 What this yields

| Property | Mechanism |
|----------|-----------|
| Only invitees can join | signed invite grant + invite proof + room key |
| Relay can't join or read | never sees room key, plaintext SDP, or stable peer IDs |
| Identity survives rejoin | same keypair → same peer ID |
| Kicks stick | key rotates; old epoch key/channel is useless; banlist blocks the old key |
| Fresh key can rejoin | only with a new invite; room can refuse |
| No server trust required | all proofs are peer-to-peer |

“Kicks stick” means the *kicked key* cannot return with the old credential.
It does not mean a determined person with a new key and a new invite can
never return; that is a social decision ([security.md](security.md) §6).

---

## 4. Certificates: authority is more than a signature

A valid signature only proves that a key signed a payload. It does not
prove that the signer is the Host, that a kick passed, or that a takeover
is legitimate. PeerWatch therefore carries **certificates**:

- `VoteCertificate` — signed votes from a strict majority of a frozen
  roster. Proves a `PLAY_VIDEO` or `KICK` passed.
- `TakeoverCertificate` — signed `TAKEOVER_ACK`s from a strict majority,
  plus a proof-of-possession challenge. Proves a new Host is legitimate.
- `HostLease` — a signed `(stream_id, term, host)` object carrying the
  certificate that elected it.

Every authoritative message (`SYNC`, `START_AT`, `MANIFEST_*`,
`ROOM_STATE`, `KICK_NOTICE`) is checked against the current lease and
certificate, not merely against a valid signature. This is the single most
important correction to the original identity design. See
[governance.md](governance.md) and [protocol.md](../spec/protocol.md)
§5.

---

## 5. Consequences for the rest of the system

- **Kick bans**: a passed `KICK` produces a `KickCertificate`. Every peer
  adds `{target_pubkey, certificate}` to the room banlist, tears down the
  connection, rotates the room key, and refuses old-epoch AUTH. A late
  joiner receives the banlist with certificates and verifies them; a bare
  banlist entry without a certificate is ignored.
- **Host continuity**: a rejoin with the same key is a known peer. If no
  takeover happened, it may resume only after a majority confirmation for a
  new term; otherwise it is a normal complete seeder.
- **Signed state messages**: `PROPOSAL`, `VOTE`, `VOTE_CERT`, `SYNC`,
  `TAKEOVER_CLAIM`, `BAN_REPORT`, `CHAT`, `REKEY`, and the manifest
  messages carry their author’s signature. Liveness/data messages (`PING`,
  `BITFIELD`, `BLOCK`, …) do not; they have no authority.
- **Gossiped messages** carry `author_pubkey` in the envelope, so a peer can
  forward a signed `ROOM_STATE` or `VOTE` without relying on the receiving
  connection’s identity.

---

## 6. Honest limitations

- **A peer ID is not a legal identity.** It means “same key as before”, not
  “this is Alice”. Mitigations: a TOFU (“trust on first use”) known-peers
  list so clients can display “you've watched with `pw:vk3f…` 12 times”, and
  optional out-of-band fingerprint comparison for paranoid groups
  (Signal-style safety numbers). Both are post-v1 niceties; the design
  leaves room for them.
- **Invite leaks = one admission.** A leaked unused invite can admit one
  peer (or up to `max_uses`). Unlike the old room-wide token, it does not
  give permanent access to the room key or future epochs. Revoke unused
  invites by expiry and by refusing grants.
- **Room-key leak = epoch compromise.** A member who leaks the current
  `room_key` lets others join that epoch. Rekey on kick fixes the kicked
  key, not a leaked epoch key. Treat the current room key as a secret.
- **Key theft = identity theft.** Disk-level attackers get the identity
  (same class as SSH keys). Passphrase-at-rest is planned
  ([roadmap.md](../roadmap.md)).
- **Browser XSS = identity + token theft.** IndexedDB is not a secure
  enclave. Strict CSP and no remote scripts are part of the security model.

---

## 7. Algorithm inventory

| Use | Algorithm | Why |
|-----|-----------|-----|
| Signatures | Ed25519 | tiny, fast, deterministic, native+WASM |
| Key agreement | X25519 | small, fast, native+WASM |
| Hashing / IDs | BLAKE3 | fast, modern, one primitive for content/peer/channel IDs |
| Piece roots / block proofs | BLAKE3 Merkle tree | fast, incremental, per-block attribution |
| Key derivation | HKDF-SHA256 | standard, simple |
| Envelope/invite encryption | XChaCha20-Poly1305 | AEAD, 24-byte random nonces, fast in software |
| Invite proof | HMAC-SHA256 | simple capability proof |
| Transport encryption | DTLS 1.2/1.3 (via WebRTC) | mandatory, free, mutually authenticated |

SHA-256 is deliberately **not** used for piece integrity. There is no
compatibility constraint to preserve, BLAKE3 is faster in software and WASM,
and using one hash everywhere keeps a single hashing dependency.
