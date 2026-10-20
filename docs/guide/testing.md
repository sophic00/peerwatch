---
tier: guide
---

# Testing Strategy

The protocol is a distributed state machine with adversarial inputs. Testing
it only through real WebRTC and real browsers would be slow, flaky, and
would never cover partitions, clock skew, or malicious peers. The strategy is
layered: deterministic simulation first, then transport, then browsers, then
chaos.

The normative schema and limits are in [protocol.md](../spec/protocol.md); the
core's event/command contract is in [core-api.md](../spec/core-api.md); the
vector corpus and conformance classes are in [conformance.md](conformance.md).

---

## 1. Deterministic simulation (`pw-sim`)

A virtual network of N peers with:

- a **fake clock** (millisecond granularity, controllable jumps);
- a **fake network** (latency, jitter, loss, reordering, partitions, relay
  outages);
- a **fake store** (in-memory, injectable failures);
- scripted peer behaviours, including malicious ones.

Every core transition is synchronous and deterministic: the same seed must
produce the same event trace. This is the primary test harness for:

- host election, host loss, takeover certificates, rejoin;
- partition/heal (no two hosts in a term, no split-brain);
- vote certificates (frozen roster, conflicting votes, omitted votes);
- replay (old `SYNC`/`START_AT`/`MANIFEST_APPEND` rejected);
- scheduler invariants (no request beyond Q, no unverified upload, no
  starvation of the playback window);
- manifest append/version handling (gap → resync, stable `stream_id`);
- storage crash consistency (data-before-bitfield journaling).

### 1.1 Property tests

Use `proptest`/`quickcheck` for invariants such as:

- at most one accepted `HostLease` per `(stream, term)`;
- a peer never uploads a block that failed its proof;
- `availability[i]` never exceeds roster size;
- a verified piece is never evicted before it is durably journaled;
- certificate verification is monotonic (a higher valid term never loses to
  a lower one);
- `Q` and byte budgets are never exceeded.

### 1.2 Scenario tests

Scripted scenarios, each with an expected trace:

- 5 peers, proposal passes, host leaves, complete peer takes over;
- 5 peers, host leaves, no complete peer → stalled, no fake takeover;
- partition 3/2, both sides try to elect → only majority succeeds;
- kicked peer reconnects with old key/epoch → rejected;
- malicious peer sends bad block → exact sender banned, honest peers not;
- malicious peer sends valid-signature `SYNC` with wrong term → authority
  strike, not clock update;
- peer resumes with a persisted bitfield → actual bitfield sent, not zeros;
- live transcode appends during playback → no manifest gap, no stale
  content ID.

---

## 2. Fuzzing

Every parser that touches untrusted bytes is a fuzz target:

- deterministic CBOR decoder and every message payload;
- manifest head/chunk/append validation;
- Roaring bitmap decoder;
- Merkle proof verifier (malformed paths, wrong lengths, odd leaves);
- fMP4 box parser (`ftyp`/`moov`/`moof`/`mdat`, `tfdt`);
- WebM/EBML cluster parser;
- SDP/signal-envelope parser;
- invite token / join request parser;
- ffprobe JSON parser (native).

Fuzzers must assert “no panic, no unbounded allocation, no accept of an
invalid proof”, not just “no crash”. Run with a corpus and a time budget in
CI; run long campaigns before releases.

---

## 3. Unit and integration tests

- `pw-crypto`: AUTH transcript vectors, signature domain separation, HKDF
  test vectors, X25519/AEAD round trips, replay rejection.
- `pw-proto`: canonical CBOR vectors, hash preimages, manifest chunk hashes,
  last-piece/last-block edge cases, message size limits.
- `pw-core`: scheduler math (capacity, Q, hedging, cancel), sync controller
  (step response, clock skew, clamp/anti-windup), governance certificate
  selection.
- `pw-store`: sparse/append-only file, journal ordering, power-fail
  simulation (write then drop before fsync), OPFS pack-file index rebuild.
- `pw-signal-relay`: channel quotas, byte limits, alias handling, idle
  timeout, no persistence.

Golden vectors are checked into the repo so a schema change cannot silently
change `peer_id`, `proposal_id`, `manifest_hash`, or `content_id`.

---

## 4. Browser end-to-end tests

Use Playwright (Chromium, Firefox, WebKit) with the real web UI:

- two browser tabs join, propose, vote, play, pause, seek, free-roam;
- main-thread WebRTC + Worker core bridge (no WebRTC in Worker);
- MSE codec detection: fMP4 H.264/AAC default; WebM only when supported;
- autoplay gesture: no audio starts before a click;
- background tab: visibility → `AWAY`, resume/re-sync on return;
- OPFS pack files, quota pressure, `navigator.storage.persist()`;
- `SourceBuffer` eviction/`QuotaExceededError` recovery;
- XSS/CSP smoke test: no inline script, no remote script, Worker-only keys.

Desktop E2E (Tauri/WebKitGTK):

- MSE path with local media protocol;
- mpv fallback with GStreamer codecs missing;
- control RPC vs media stream separation (no large JSON IPC);
- native sandbox flags for mpv.

CLI E2E:

- daemon + `pw` control socket;
- mpv range server auth (wrong token rejected);
- `--no-tui` scripted room flow.

---

## 5. Chaos and load tests

Run against real transports, not the simulator:

- kill the relay mid-room (room continues, new joins fail clearly);
- kill TURN (affected pairs fail with a clear error, others continue);
- host process kill / network drop / host rejoin;
- 16-peer room, 1080p and 4K, simultaneous seeks;
- packet loss 1–5 %, 100–300 ms RTT, jitter;
- clock skew ±2 s between peers (sync must still converge);
- 30-minute soak with memory/CPU/`bufferedAmount` metrics;
- repeated join/leave churn and invite reuse attempts.

Record the metrics listed in [roadmap.md](../roadmap.md) §4 and compare them
with [performance.md](../design/performance.md). A failed assumption is a spec change,
not a test to silence.

---

## 6. CI matrix

| Job | What it runs |
|-----|--------------|
| `fmt` / `clippy` | all crates, `-D warnings` |
| `test-native` | unit + integration + `pw-sim` property tests |
| `test-wasm` | `wasm32-unknown-unknown` check for `pw-proto`, `pw-crypto`, `pw-core`, `pw-store` (wasm), `pw-signal-client`, `pw-transmux` |
| `fuzz-smoke` | short fuzz runs on every PR |
| `fuzz-long` | nightly/weekly long campaigns |
| `browser-e2e` | Playwright Chromium/Firefox/WebKit |
| `desktop-e2e` | Tauri/WebKitGTK + mpv fallback (Linux runner) |
| `deny` | license/advisory checks (`cargo-deny`) |
| `schema-vectors` | golden vectors unchanged unless explicitly updated |
| `docs-lint` | `scripts/lint_invariants.py` — invariants, links, orphans, tiers, `§` refs |
| `docs-catalog` | `scripts/gen_catalog.py --check` — message tables match the YAML |
| `conformance` | the full vector corpus against the reference implementation |

Two rules that have already earned their place:

- **A crate meant to be WASM-compatible MUST be checked with
  `--target wasm32-unknown-unknown`.** This is how accidental
  `axum`/`tokio`/`std::process` dependencies in `pw-signal-client` or
  `pw-media` get caught.
- **A documentation change MUST pass `docs-lint`.** The invariants listed in
  [README.md](../README.md) are only real if something enforces them; every
  one of them drifted at least once during the rewrite.

### 6.1 The `pw-sim` trace contract

`pw-sim` is the primary correctness harness, so its output format is part of
the contract ([core-api.md](../spec/core-api.md) §9):

- Traces are canonical CBOR, so they diff cleanly and can be committed.
- A trace is `(seed, config, scripted events) → (ordered commands, snapshots
  at checkpoints)`.
- The same seed MUST produce a byte-identical trace on every platform. A
  trace that differs between Linux and macOS is a determinism bug in the core
  (a `HashMap` iteration, a clock read, an entropy call), not a platform
  quirk.
- Trace files are the conformance vectors in `tests/conformance/sim/`
  ([conformance.md](conformance.md) §2.2).

A reproduction workflow worth building early: a user exports a local trace
([observability.md](observability.md) §4), a maintainer runs
`pw-sim replay bug.cbor`, and the exact command stream is reproduced without
the network, the video, or the peers.
