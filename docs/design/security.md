---
tier: design
---

# Security & Threat Model

PeerWatch is designed for **small groups of people who know each other**
(5–16), watching user-supplied video over an adversarial internet. This
document states what we defend against, how, and — just as importantly —
what we explicitly do not.

The normative authority objects, signatures, and replay rules are in
[protocol.md](../spec/protocol.md) §4–§9. This document is the threat
model and the reasoning.

---

## 1. Assets and adversaries

**Assets:** the room’s privacy (who is watching what), the integrity of the
video bytes each peer plays, the integrity of room governance (votes, kicks,
clock authority), availability (a griefer shouldn’t be able to freeze the
room), and each peer’s identity continuity.

**Adversaries considered:**

- **A. Outsider on the internet** — doesn’t have an invite, tries to join or snoop.
- **B. The signaling relay operator** — sees envelopes and IPs, tries to read/join/correlate/eclipse.
- **C. Network MITM** — ISP-level observer/censor between peers.
- **D. Malicious room member** — has an invite and the room key, poisons data, forges votes, floods, griefs.
- **E. Malicious *content*** — a proposed file crafted to exploit media parsers.
- **F. Compromised browser origin (XSS)** — same-origin script reads IndexedDB and tokens.

---

## 2. Defense layers (and which adversary each defeats)

```
L0  Transport encryption & authentication — DTLS (free with WebRTC)     → C, A
L1  Invites + room key/epoch — signed expiring capabilities             → A, B
L2  Per-peer signaling encryption — X25519 + AEAD, signed envelopes     → B, D (snooping)
L3  Identity — Ed25519 + X25519, AUTH transcript, DTLS binding          → D (impersonation)
L4  Authority — host lease/term, vote/takeover/kick certificates        → D (forged authority)
L5  Data integrity — per-block Merkle proofs against piece roots        → D (poisoning)
L6  Behavioral defense — scoring, byte-rate limits, strikes, auto-bans    → D (availability)
L7  Social governance — majority certificates, kicks, rekey              → D (judgment calls)
```

The design goal: **cryptography handles everything cryptography can
handle. Signatures prove authorship; certificates prove authority; votes
decide taste.**

---

## 3. The central distinction: authentication vs authorization

“Every control message is signed, therefore impersonation is out” is only
half true. A valid Ed25519 signature proves that a key signed a payload. It
does **not** prove that the signer is allowed to:

- send a `SYNC` (only the current Host may);
- announce a `KICK`/banlist entry (only a passed `KickCertificate` may);
- claim a host lease (only a `VoteCertificate` or `TakeoverCertificate` may);
- claim a takeover (only a majority `TakeoverCertificate` + challenge may).

A malicious member can sign anything with its own valid key. The receiver
must check the **authority object** and the **term**, not just the
signature. The protocol therefore separates:

| Question | Mechanism |
|----------|-----------|
| Who sent this? | Ed25519 signature + envelope `author` |
| Are they allowed to send it? | `HostLease`, certificates, `term`, `seq` |
| Is this the current state? | `(term, state_version)` monotonic rules |
| Is this message fresh? | `seq` / `msg_id`, not merely a signature |

A valid signature from a non-host is an **authority violation** (strike,
then ban on repeat), not “forgery.” A forged signature or an unsigned
message claiming to be signed is a **signature failure**.

---

## 4. Automatic elimination of bad peers

Every peer maintains, per connected peer: throughput/latency estimates
(scheduler inputs), a **reliability score** `r_i ∈ [0,1]`, and a strike
count. Rules, in escalating order:

| Offense | Evidence | Response |
|---------|----------|----------|
| **Block fails its Merkle proof** | objective, attributable to the exact sender | **Instant local ban.** Poisoning is unambiguous; the bad block is dropped and re-fetched from someone else. |
| **Signature failure on a signed message** | objective | Instant local ban (forgery or corruption). |
| **Authority violation** (valid signature, wrong host/term/certificate) | objective | Strike. **3 strikes = local ban.** |
| **Protocol violation** (malformed/oversized/out-of-state) | objective | Strike. **3 strikes = ban.** |
| **Rate abuse** (request/message/byte floods) | objective | Throttle → drop → strikes. |
| **Timeouts/flakiness** | statistical | `r_i` decays → scheduler smoothly avoids the peer. Recovers slowly on good behavior. |
| **Freeloading** (never uploads) | statistical | *Not* bannable; browser peers on metered links are legitimate. Served last and slowest only. |

**Block proofs fix local blame, not third-party blame.** A 512 KiB piece is
assembled from 16 blocks that may come from different peers. Hashing the
whole piece and banning “the sender” is unfair and exploitable. Each block
carries a Merkle proof to its piece root, so the receiver knows exactly
which connection delivered the bad bytes — that is *local* attribution, and
it is exact.

It is not *transferable* attribution. A `BlockProof` is
`{ bad_bytes, block_hash, proof, sender_pubkey, sender_sig }`; the
`sender_sig` is what turns “these bytes are wrong” into “this peer sent
these bytes”. Without it, any peer can fabricate a proof over arbitrary
garbage — the check “does not match the committed root” is trivially
satisfiable — so an unsigned proof is a rumor and MUST NOT contribute to a
strike or a ban ([protocol.md](../spec/protocol.md) §6.6).

With `sender_sig`, the incentive is symmetric: a peer that signs a proof
over bytes it did not send has itself committed a provable violation.

**Gossip is advisory.** A peer that locally bans someone may broadcast a
signed `BAN_REPORT { offender, reason, evidence }`. If the evidence is a
block proof, it is independently verifiable. A report without proof is a
rumor. No peer adds a room-wide ban based on `BAN_REPORT`; room-wide
removal requires a `KickCertificate`.

---

## 5. Per-adversary analysis

**A. Outsider.** Cannot find the room without an invite (invite channel is
a hash of `room_id + invite_id`), cannot read signaling (per-peer X25519),
cannot complete AUTH (room key/epoch required), cannot MITM the transport
(DTLS with fingerprint-pinned certs, and the AUTH transcript binds those
fingerprints). Residual: *invite theft* — treat invites like one-time
passwords; they are expiring and usage-capped.

**B. Relay operator.** Sees opaque channel hashes, per-epoch aliases,
epoch integers, epoch-scoped `signal_pub` values, envelope sizes/timing, and
IP addresses. It does **not** see `room_id` (it is inside the encrypted
`SignalEnvelope` plaintext), stable peer IDs, static identity keys, the room
key, plaintext SDP/ICE, media, chat, or votes.

Cannot join (no room key), cannot impersonate (envelopes signed), and
cannot read SDP — **provided the sender takes `signal_pub` from the
host-signed roster rather than from the relay**. If a sender trusted the
relay's roster, a malicious relay could substitute its own key and decrypt
the SDP; AUTH would still stop impersonation, but network topology would
leak. That is why [protocol.md](../spec/protocol.md) §11.2 makes the
authenticated roster the only permitted source.

Residual: metadata correlation within an epoch, room-existence and
epoch-timing observation (the lobby is public to anyone holding `room_id`),
IP-level observation, denial of service, and eclipse (it can omit
peers/offers). Self-hosting reduces IP exposure; multiple relays or manual
signaling reduce eclipse risk. It cannot link the same person across two
rooms, because aliases are `HMAC(room_psk, …)` and `room_psk` is per-room.

**C. Network MITM.** Sees encrypted DTLS datagrams. Can infer *that* peers
talk and roughly how much (video-sized flows). Cannot see content, inject,
or downgrade. Residual: traffic analysis, same class as any encrypted P2P.

**D. Malicious member.** Faces L3–L7:

- can’t forge identity (Ed25519 AUTH);
- can’t forge authority (lease/certificates/term);
- can’t poison data (block Merkle proofs) and is attributable if it tries;
- can’t replay old SYNC/START_AT/APPEND (`seq` window);
- can’t flood beyond byte-rate limits (throttle/strikes);
- can’t stuff ballots for free (invites cost admission; one key one vote);
- can’t make a kicked key return (rekey + banlist);
- can’t impersonate the Host’s clock (lease check).

Remaining power: being annoying *within* the rules (off-topic proposals,
leaving at dramatic moments), Sybil accounts if it can obtain multiple
invites, and collusion with a majority. That is what kick votes and the
invite policy are for.

**E. Malicious content.** A crafted video could target ffmpeg/mpv/GStreamer
decoder bugs. Mitigations:

1. Browser peers decode in the browser’s sandboxed media pipeline (strong).
2. Transcode/remux happens on the *proposer’s* machine; peers receive the
   already-normalized fMP4/WebM stream.
3. Native peers still decode that stream with mpv/GStreamer. Run mpv with a
   restricted protocol whitelist, no network access where possible, and OS
   sandboxing (bubblewrap/firejail) if available. Keep mpv/GStreamer
   updated. Do not claim “browser sandbox” covers native playback.
4. Validate the manifest’s codec/profile string and container before
   handing bytes to a player; do not let a manifest choose an arbitrary
   parser.
5. The trust norm: don’t accept proposals from people you wouldn’t hand a
   video file to. This residual risk is inherent to any user-supplied-media
   tool.

**F. Compromised browser origin (XSS).** The Ed25519/X25519 private keys
live in IndexedDB; the room key and invites live in memory/Worker state.
Any same-origin script can read them. Mitigations: strict CSP, no remote
scripts, Subresource Integrity, no `eval`, and keep keys inside the Worker
so page-level UI code cannot access them directly. XSS is equivalent to
identity + token theft; the security model says so.

---

## 6. Sybil, invites, and kicks

A room-wide bearer token makes “one key = one vote” weak: any member can
mint unlimited keys with the same token, and a kicked peer can rejoin with a
fresh key. The model here is:

- **Signed, expiring, usage-capped invites** (`InviteGrant`) admit one
  peer (or a bounded number).
- The invitee receives the current `room_key`/`epoch` via an encrypted
  `JOIN_ACCEPT`; mere possession of an old invite does not grant future
  epochs.
- A kicked key is banned and the room key rotates. The kicked peer cannot
  authenticate or subscribe to the new relay channel.
- A fresh key requires a fresh invite from a member. The room can refuse;
  repeated abuse is social, not cryptographic.

Residual Sybil risk:

- A member who issues many invites can create many identities. That member
  is already trusted by the room.
- A leaked current `room_key` compromises that epoch; rekey after a kick
  does not fix a key leak. Treat the room key as a secret.
- A majority coalition can admit whoever it wants. That is governance.

---

## 7. Replay, state versions, and ordering

- Every replay-sensitive authoritative message carries a monotonic
  identifier: `seq` for `SYNC`/`START_AT`/`MANIFEST_APPEND`/`CHAT`, a
  `proposal_id`/`vote_id`/`certificate_hash` for governance, and
  `(term, state_version)` for `ROOM_STATE`. The signature covers the
  identifier.
- Receivers keep the highest accepted `seq` per `(author, stream, type,
  term)` and reject stale/duplicate values.
- `ROOM_STATE` has `(term, state_version)`; only a higher pair is accepted.
- `CHAT` dedups by `msg_id`; display order uses `(lamport, ts_ms, peer_id)`,
  not a single wall clock.
- `MANIFEST_APPEND` carries `from_version → to_version`; a gap triggers a
  head/chunk resync, not blind application.
- `VOTE_CERT`/`TakeoverCertificate` are verified as whole objects; a replayed
  old certificate with a lower term is ignored.

A channel binding alone only prevents cross-room replay. `seq` and `term`
prevent same-room replay, which is the case that matters: a recorded `SYNC`
replayed into the same room would otherwise move everyone's playhead.

---

## 8. Limits and resource exhaustion

Message counts alone are not enough: 64 messages/s × 64 KiB = 4 MB/s,
and the old 1 MiB cap made the same rule a 64 MB/s DoS. The protocol
enforces **byte budgets** and per-type limits
([protocol.md](../spec/protocol.md) §9):

- control ≤ 64 msgs/s and ≤ 128 KiB/s per peer;
- data ≤ 8 MiB/s burst / 4 MiB/s sustained per peer;
- bulk/manifest ≤ 1 MiB/s per peer, chunk ≤ 32 KiB;
- bitfield ≤ 16 KiB, 1/s;
- request/cancel ≤ 64 entries, outstanding ≤ Q ≤ 64 **blocks** (not entries);
- chat ≤ 2 KiB;
- CBOR depth/collection limits;
- relay per-alias byte limits and channel quotas.

The scheduler also respects the DataChannel `bufferedAmount`; an app queue
that ignores SCTP backpressure is a memory/latency DoS even if Q is small.

---

## 9. What the signatures cover (and don’t)

**Signed:** AUTH, ROOM_STATE, HOST_LEASE, REKEY,
MANIFEST_HEAD/APPEND/FINAL, SYNC, START_AT, STALL_NOTICE,
TAKEOVER_CLAIM/ACK, PROPOSAL, VOTE, VOTE_CERT, KICK_NOTICE, BAN_REPORT,
CHAT.

**Unsigned:** HELLO, PING, PONG, BYE, PRESENCE, BITFIELD, REQUEST, BLOCK,
CANCEL, READY, MANIFEST_CHUNK, MANIFEST_GET, CHALLENGE, CHALLENGE_RESPONSE.

Unsigned messages have no authority:

- a forged bitfield only causes wasted requests;
- a forged REQUEST/CANCEL only wastes the receiver’s upload, bounded by
  policy;
- a forged BLOCK fails its Merkle proof;
- a forged MANIFEST_CHUNK fails its chunk hash;
- a forged HELLO fails AUTH because its fields are in the transcript;
- a forged CHALLENGE only triggers a proof response, which is harmless;
- a forged READY only starts playback early for the liar, who then stalls.

Per-block signatures would cost CPU for zero added security: a block’s
authenticity is its Merkle proof against the signed manifest.

---

## 10. Honest limitations (read before relying on this)

- **Sybil reality**: a member who issues many invites can create many
  identities; a leaked current room key compromises that epoch. At 5–16
  person social scale this is manageable, but rooms are **not** resistant
  to a determined infiltrator with a valid invite and room key. Invite
  hygiene is the real perimeter.
- **No real-world identity**: a peer ID means “same key”, not “Alice”.
  Optional TOFU known-peers and fingerprint comparison are planned niceties
  ([identity.md](identity.md) §6).
- **Key theft = identity theft** (same class as SSH keys). Passphrase-at-
  rest for native keys is planned.
- **Metadata at the relay** (aliases, IPs, timing) exists wherever the
  relay runs. Self-hosting minimizes exposure; the relay stores nothing
  persistently.
- **Relay/TURN availability and eclipse** are not solved by cryptography.
  A malicious relay can deny service or omit peers; multiple relays and
  manual signaling are mitigations, not guarantees.
- **Native media parsing** is a real attack surface; browser sandboxing
  does not protect mpv/GStreamer. Sandbox native players.
- **DoS at the edges we can’t fix**: an attacker who *is* your only route
  to a block can refuse to serve it (the scheduler routes around). An ISP
  can block UDP (WebRTC needs it; TURN-over-TCP is the escape hatch).
- **Legal note**: PeerWatch shares files among people who each must
  lawfully possess the content. The software is a tool. Use it accordingly.

---

## 11. Update policy

Security-relevant dependencies (`webrtc-rs`, `ed25519-dalek`, `x25519-dalek`,
`blake3`, `chacha20poly1305`, and on hosts ffmpeg/mpv/GStreamer) are pinned
and reviewed on upgrade. The protocol version in HELLO allows
mandatory-upgrade flags if a wire-level flaw is found. A deployed v2
implementation must ship a migration note before any breaking security
change.
