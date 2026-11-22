---
tier: design
---

# Governance — CRDT Room State, Dual Modes, and Kicks

A PeerWatch room has **no privileged admin roles**. The room creator holds no
special powers after signing `RoomGenesis`; the current Host holds only clock
and manifest authority for `(stream_id, term)`. Normative schemas live in
[protocol.md](../spec/protocol.md) §5.

---

## 1. Self-verifying CRDT room state

Durable room facts are not owned by a single Host snapshot — they are an
append-only join-semilattice of signed objects synchronized via `STATE_DIGEST`
and `STATE_SYNC`:

| Fact | Signed by | Merge rule |
|---|---|---|
| `RoomGenesis` | creator | unique per `room_id` |
| `MemberAdd` | any admitted member | set union (valid chain from genesis) |
| `KickCertificate` (`BanEntry`) | majority of frozen active roster | set union (self-verifying — no Host can omit a ban) |
| `PeerSignalKey` | the peer itself | max `seq` per `peer_pubkey` |
| `HostLease` | Host + authority proof | max `term`, tie-break by vote/ack count then smallest `lease_hash` |
| `QueueAdd` / `QueueRemove` | authoring peer (`COLLABORATIVE` mode) | add/remove set ordered by `(lamport, entry_id)` |

Every peer computes the identical membership, banlist, queue, and active Host
from the union of valid signed records, making partition heal and late joins
associative, commutative, and idempotent.

---

## 2. Active roster and quorum liveness

- **Admitted membership** is the full `MemberAdd` set minus `KickCertificate`s.
- **Active roster** tracks currently connected, authenticated peers:
  - A signed `BYE(PW_NORMAL)` prunes a leaving peer immediately.
  - Unclean drops are pruned after ICE/consent timeout.
- When a `Proposal` or `TakeoverClaim` is created, it freezes the current
  **active roster** in `roster_snapshot` so every verifier evaluates the exact
  same strict-majority threshold (`approvals > |roster_snapshot| / 2`) without
  being deadlocked by peers who left cleanly hours earlier.

---

## 3. Dual room modes

Set once in `RoomGenesis.mode` when creating a room:

### 3.1 `DEMOCRATIC` mode (default)

- Starting a video requires a `PROPOSAL(PLAY_VIDEO)` broadcast, optional
  `PROPOSAL_COMPAT` checks, signed `VOTE`s, and a strict-majority
  `VoteCertificate` over `roster_snapshot`.
- On passage, the proposer publishes `HOST_LEASE(term+1)` with the
  `VoteCertificate`, distributes `MANIFEST_HEAD` + chunks, runs the buffering
  gate (`READY` from peers or 20 s cap), and broadcasts `START_AT`.
- Only the current Host's play/pause/seek/rate controls drive `SYNC`.

### 3.2 `COLLABORATIVE` mode (shared queue & controls)

Designed for trusted friend groups binge-watching episodes without voting on
every file:

- Any connected member may enqueue a local file via `QUEUE_ADD` (including its
  initial `ManifestHead` and pipeline info) or remove their own entry via
  `QUEUE_REMOVE`.
- When the room is idle or the current stream reaches `Ended` (or a skip
  triggers), the owner of the head `QueueEntry` claims `HOST_LEASE(term+1)`
  with `authority: QueueClaim { entry_id }`, runs the buffering gate, and
  starts playback automatically.
- Any member may click Play / Pause / Seek / Rate: their client sends a signed
  `CONTROL_REQ` to the current Host, which updates playback and emits the
  authoritative `SYNC`. (Clock authority stays single-leader so playback never
  desyncs.)
- `KICK` still requires a strict-majority `VoteCertificate` in both modes.

---

## 4. Proposals, votes, and equivocation

- One key = one vote per `proposal_id`; votes are non-changeable.
- A `VoteCertificate` containing two votes from the same `voter_pubkey` is
  **invalid** and rejected outright.
- If a peer observes two conflicting valid signed votes from the same voter
  across separate messages, that voter's votes for that proposal are voided and
  the equivocator receives an authority strike (`PW_AUTHORITY_VIOLATION`).
- Competing valid certificates for one proposal converge deterministically:
  most valid votes wins, tie-broken by lexicographically smallest `cert_hash`.

---

## 5. Kicks vs local cryptographic bans

- **Social removal (`KICK`)**: passes via strict-majority `VoteCertificate`
  (`KickCertificate`). Every member inserts `BanEntry` into its CRDT banlist,
  closes the connection with `BYE(PW_BANNED)`, drops future signaling from that
  peer, and rejects future `AUTH` from its key ([identity.md](identity.md) §3).
- **Cryptographic offenses (bad `BLOCK` Merkle proof, invalid signature)**:
  never wait for a vote — the witnessing peer applies an **instant local ban**
  immediately over the authenticated DTLS connection. If the offender continues
  misbehaving toward others, the room can initiate a standard `KICK` vote.
