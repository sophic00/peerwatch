---
tier: guide
---

# Conformance

PeerWatch calls itself a normative specification. This document defines what
that means for an implementation: exactly which observable behaviours are
required, how they are tested, and how a third party proves compliance
without reading the reference source.

If you are writing an independent v2 implementation, this is the document to
read after [protocol.md](../spec/protocol.md).

---

## 1. Conformance classes

An implementation may claim any of these independently. They are cumulative
within a class but not across classes.

| Class | Requires | Typical implementer |
|---|---|---|
| **C1 — Wire** | encode/decode, hashes, Merkle proofs, canonical CBOR | any participant |
| **C2 — Peer** | C1 + AUTH, invite join, relay signaling, full data plane | a viewer/seeder |
| **C3 — Authority** | C2 + leases, certificates, takeover, rekey, governance | a full room member |
| **C4 — Source** | C3 + media pipeline (segmenter, remux, transcode-ahead) | a Host that proposes files |
| **C5 — Relay** | relay protocol only; no media, no room state | a self-hoster |

A browser-only viewer that never proposes is C3. A CLI that proposes is C4.
The relay is the only component that is not a peer.

---

## 2. Test corpus

Everything is driven by checked-in vectors, not by prose. The corpus lives in
`tests/conformance/` and is canonical CBOR so it diffs cleanly.

```
tests/conformance/
├── cbor/                  canonical encoding vectors
├── hashes/                domain-separated preimages -> expected digest
├── merkle/                piece roots + proofs, incl. odd leaf counts
├── auth/                  transcript -> auth_tag + auth_sig
├── envelope/              sign/verify, replay rejection
├── certificates/          valid + every invalid shape
├── manifest/              head/chunk/append, last-piece edge cases
├── roaring/               bitfields incl. adversarial encodings
├── signaling/             SignalEnvelope, InviteEnvelope, LOBBY_BASIS
├── media/                 fMP4 + WebM samples with expected segment tables
└── sim/                   scripted peer scenarios -> command traces
```

### 2.1 Vector format

Every vector is one canonical-CBOR map:

```cbor
{
  "name": "merkle/piece-root-odd-tail",
  "spec": "protocol.md#6.6",
  "input": { ... },           // exactly what the spec says to hash
  "expect": { "piece_root": h'...' },   // or "error": "PW_CBOR_INVALID"
  "notes": "17 blocks; odd leaf promoted unchanged"
}
```

Rules:

- A vector names the spec section it tests. A vector with no section is a
  test of nothing in particular and will be rejected in review.
- `expect` is either the exact output or an `error` from
  [errors.md](../spec/errors.md).
- **No vector may be updated to make a test pass.** If an implementation
  disagrees with a vector, either the implementation is wrong or the spec
  changed — and a spec change requires updating
  [design-log.md](../design/design-log.md) first.

### 2.2 Determinism vectors (`sim/`)

The simulator vectors are the strongest check, because they test behaviour
rather than arithmetic:

```cbor
{
  "name": "sim/host-leave-takeover",
  "spec": "sync.md#6",
  "seed": 0x0123456789abcdef,
  "config": { "peers": 5, "loss": 0.02, "rtt_ms": 40 },
  "script": [
    { "at_ms": 0,     "do": "join_all" },
    { "at_ms": 1000,  "do": "propose", "peer": 0, "stream": "..." },
    { "at_ms": 3000,  "do": "vote_all", "approve": true },
    { "at_ms": 20000, "do": "kill", "peer": 0 },
    { "at_ms": 40000, "do": "assert_host", "peer": 1, "term": 2 }
  ],
  "expect_trace_hash": h'...'
}
```

An implementation is conformant on this vector if it produces the same
ordered command trace and the same snapshots at each `assert_*`
([core-api.md](../spec/core-api.md) §9).

---

## 3. Mandatory behaviours

These are the ones a plausible implementation gets wrong. Each is tested by
a named vector.

### 3.1 Encoding

| # | Requirement | Spec |
|---|---|---|
| E1 | Reject non-canonical CBOR (indefinite length, non-shortest int, unsorted keys) | §1.1 |
| E2 | Enforce depth 16 and collection length 4096 **before** allocating | §1.1 |
| E3 | Hash only canonical output; never hash a re-serialization | §3 |
| E4 | Unknown map keys are ignored; unknown optional types are ignored | §10 |
| E5 | Unknown *required* type ⇒ `BYE(PW_UNSUPPORTED_MESSAGE)` | §10 |

E3 is the subtle one: an implementation that decodes to a struct and
re-encodes before hashing will produce a different `manifest_hash` the moment
it drops an unknown field.

### 3.2 Identity and authority

| # | Requirement | Spec |
|---|---|---|
| A1 | Verify both the envelope `sig` and the AUTH `auth_sig` | §1.3, §4.3 |
| A2 | AUTH transcript binds both DTLS fingerprints, both nonces, both keys, and the epoch | §4.3 |
| A3 | Reject a valid signature from a non-host for host-only types (`PW_AUTHORITY_VIOLATION`, not forgery) | §5.1, §8 |
| A4 | Reject a `HostLease` whose `prev_lease_hash` does not chain | §5.1 |
| A5 | A `VoteCertificate` with two votes from one voter is **invalid** | §5.2 |
| A6 | A certificate's `result` must match its recomputed tally | §5.2 |
| A7 | Recompute a `REKEY` key from the basis and reject a mismatch | §4.4.2 |
| A8 | Accept epoch `e` and `e+1` during `rekey_grace`, then drop `e` | §4.4.4 |

A5 and A7 are the two that prevent a fork. An implementation that resolves
conflicting votes by picking one, or that accepts the first `REKEY` it sees,
will pass a happy-path test and split a room under adversarial input.

### 3.3 Data plane

| # | Requirement | Spec |
|---|---|---|
| P1 | Verify each block against `piece_root` before it reaches the store | §6.6 |
| P2 | Reject a block whose length is wrong for its index | §6.7 |
| P3 | Never upload a block that has not been verified | §6.6 |
| P4 | `REQUEST` block-mask total ≤ `Q` | §7.4 |
| P5 | Set a bitfield bit only after the piece is durable | storage.md §1.3 |
| P6 | A `BlockProof` without a valid `sender_sig` never contributes to a strike | §6.6 |
| P7 | Serve a `MANIFEST_CHUNK` only as the designated responder | §7.3 |
| P8 | Answer a `CHALLENGE` only for in-region blocks | §5.3 |
| P9 | An out-of-range bitfield triggers a manifest resync, not a strike | roaring §6 |

P5 is the one that produces silent data loss: an implementation that sets the
bit on write instead of after fsync will advertise pieces it loses on a
crash, and peers will re-request them forever.

### 3.4 Sync

| # | Requirement | Spec |
|---|---|---|
| S1 | Anchor `SYNC` on local receive time; never read the host's wall clock | sync.md §2 |
| S2 | Accumulate the reference incrementally; do not multiply elapsed time by the current rate | sync.md §2 |
| S3 | Hard-seek above 5 s; run the PI loop below it | sync.md §3.4 |
| S4 | Anti-windup: integrate only while \|e\| < 2 s and the rate is not clamped | sync.md §3.4 |
| S5 | Distinguish `FREE_ROAM` (choice) from `STARVING` (fault) | sync.md §4–5 |

S2 is subtle: the simple closed form is correct only for constant rate, and
the host's rate changes constantly because the controller is always
correcting. An implementation using the closed form will chase its own
corrections.

### 3.5 Replay and limits

| # | Requirement | Spec |
|---|---|---|
| R1 | Highest accepted `seq` per `(author, stream, type, term)` | §8 |
| R2 | `ROOM_STATE` accepted only on a higher `(term, state_version)` | §8 |
| R3 | `CHAT` dedups by `msg_id`; orders by `(lamport, ts_ms, peer_id)` | §7.6 |
| R4 | Byte budgets per class, not just message counts | §9 |
| R5 | `bufferedAmount` respected in addition to `Q` | §9 |
| R6 | Media parser limits applied before allocation | §9.1 |

R4 is the classic mistake: "64 messages/s" with a 64 KiB envelope is
4 MB/s, which is a denial of service.

---

## 4. Interop matrix

Two implementations are interoperable at a class if every pairwise test in
`tests/conformance/interop/` passes. The tests are deliberately asymmetric:

| Test | What it catches |
|---|---|
| A joins B's room | invite, join, AUTH, roster |
| A proposes, B votes, A hosts | proposal, certificate, lease, manifest |
| B proposes, A hosts | role symmetry (the proposer becomes Host, whoever they are) |
| A loses the host, B takes over | takeover across implementations |
| A kicks B | kick certificate, rekey, lobby catch-up |
| A is offline during B's kick, then returns | **lobby recovery** — the most likely interop failure |
| A sends a malformed block to B | blame attribution, ban |
| A is a C1-only implementation | graceful `BYE`, no crash |

The lobby-recovery row exists because it is the newest mechanism and the one
with the most room for divergent interpretation. If two implementations
disagree about whether a returning peer should be admitted, they are not
interoperable regardless of everything else.

---

## 5. Reporting

An implementation claims conformance in its README as:

```
PeerWatch conformance: C4 (v2), corpus 2025-01-14, 412/412 vectors,
interop verified against pw-reference 0.1.0
```

Requirements for the claim:

- the corpus commit hash is named;
- every vector passes, or the failures are listed explicitly with a spec
  reference — partial conformance is legitimate, silent partial conformance
  is not;
- the interop matrix was run against at least one other implementation.

The reference implementation publishes its own result in CI on every
release, so the claim is checkable in both directions.

---

## 6. What conformance does not cover

- **Performance.** A conformant implementation may be slow. The numbers in
  [performance.md](../design/performance.md) are a design model, not a
  requirement.
- **UI.** There is no conformance requirement on presentation.
- **Storage layout.** `storage.md` specifies crash-consistency *ordering* and
  an adapter *trait*, not a file format. An implementation may store pieces
  however it likes as long as P5 holds.
- **Which codecs.** C1–C3 peers may support any subset of codecs; they
  declare it in `Caps.codecs` and the room's intersection decides. A peer
  that supports only H.264 is conformant.
- **Room size.** 5–16 is the tested envelope. Working at 32 is not
  non-conformance, it is just untested
  ([roadmap.md](../roadmap.md) §3).
