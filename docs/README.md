---
tier: index
---

# PeerWatch v3 Documentation

PeerWatch is a serverless, peer-to-peer watch party for 5–16 people. A group
joins an ephemeral room, selects videos democratically or via a shared
collaborative queue, and streams them BitTorrent-style over encrypted WebRTC
while playing back in lockstep.

There is **no media server**, **no accounts**, and **no persistent server
state**. The only optional infrastructure is a tiny stateless signaling relay
(`pw-relay`), STUN, and optional TURN.

---

## Documentation tiers & precedence

| Tier | Directory | Scope | Precedence |
|---|---|---|---|
| **Normative** | [`spec/`](spec/) | wire schema, Core API, error codes, limits | **wins** |
| **Design** | [`design/`](design/) | architecture, rationale, math, threat model | defers to `spec/` |
| **Guide** | [`guide/`](guide/) | configuration, relay ops, testing | defers to both |

### Reading order

| # | Document | Tier | Scope |
|---|---|---|---|
| 1 | [architecture.md](design/architecture.md) | design | System map, 6-crate layout, two v1 frontends, 5 state machines |
| 2 | [protocol.md](spec/protocol.md) | **normative** | Canonical CBOR, 2-level segment/block model, CRDT state, message catalog, limits, stateless relay |
| 3 | [core-api.md](spec/core-api.md) | **normative** | Sans-I/O `Event`/`Command` boundary, aggregate SCTP backpressure, UI & media contracts |
| 4 | [errors.md](spec/errors.md) | **normative** | `PW_`, `RL_`, and `L_` error codes and strike ladder |
| 5 | [connectivity.md](design/connectivity.md) | design | `str0m` ↔ WebRTC, ICE/STUN/TURN, stateless relay, deadlock-free join |
| 6 | [identity.md](design/identity.md) | design | Ed25519 + X25519, single-sig AUTH, encrypted invites, Option B kick exclusion |
| 7 | [media-pipeline.md](design/media-pipeline.md) | design | fMP4/WebM, dynamic codec check, HW transcode-ahead, seekable transcode, WebVTT subtitles |
| 8 | [distribution.md](design/distribution.md) | design | Segment Merkle trees, compact raw/RLE + frontier availability, scheduler math |
| 9 | [storage.md](design/storage.md) | design | RAM-first serving decoupled from crash-safe disk/OPFS segment persistence |
| 10 | [sync.md](design/sync.md) | design | Min-delay reference filter, retuned PI controller (±5%, 2 s seek), Free-Roam, 2-step takeover |
| 11 | [governance.md](design/governance.md) | design | CRDT fact set, `DEMOCRATIC` & `COLLABORATIVE` modes, active-roster pruning, kicks |
| 12 | [security.md](design/security.md) | design | Threat model, defense layers, local-only block blame, honest limits |
| 13 | [frontends.md](design/frontends.md) | design | Browser WASM app & Native Daemon (local Web UI + `mpv`); Tauri deferred to v2 |
| 14 | [performance.md](design/performance.md) | design | Bandwidth, manifest, cold-start, CPU/RAM, and sync accuracy budgets |
| 15 | [design-log.md](design/design-log.md) | design | Retained foundations (D1–D20) and v3 architectural redesign decisions (D21–D32) |
| 16 | [config.md](guide/config.md) | guide | Daemon configuration, env vars, defaults |
| 17 | [relay-ops.md](guide/relay-ops.md) | guide | Self-hosting stateless `pw-relay` and `coturn` |
| 18 | [testing.md](guide/testing.md) | guide | `pw-sim` simulation, golden vectors, fuzzing, `str0m` ↔ browser E2E, CI |
| 19 | [roadmap.md](roadmap.md) | design | Milestones M0–M4 (with early `str0m` ↔ browser MSE spike) |

---

## Core v3 invariants (CI-linted)

- **2-level data granularity**: **Segment** ≈ 4 s keyframe-aligned (`0` = init,
  `1..=N` = media, integrity/availability unit) → **Block** = 32 KiB
  (request/transfer/Merkle leaf). Default max envelope = 64 KiB.
- **Stable stream identity**: `stream_id` is fixed at proposal/queue time;
  `content_id` is derived only on `MANIFEST_FINAL`.
- **Canonical wire format**: fragmented MP4 (H.264/AAC) default + optional
  WebVTT subtitles; WebM (VP9/Opus) when unanimous.
- **CRDT room state**: self-verifying union of `RoomGenesis`, `MemberAdd`,
  `KickCertificate`, `PeerSignalKey`, `HostLease`, and `QueueEvent`s.
- **Dual room modes**: `DEMOCRATIC` (vote per video, Host controls) and
  `COLLABORATIVE` (shared queue + `CONTROL_REQ` through Host).
- **Option B kicks**: single `room_secret`, zero epoch rotation, 100% stateless
  relay (no lobby); verified `KickCertificate` banlist enforced at signaling
  and `AUTH`; DTLS + ephemeral X25519 provide forward secrecy.
- **Transport**: **`str0m`** (native Sans-I/O WebRTC) ↔ `RTCPeerConnection`
  (browser main thread), with aggregate SCTP `bufferedAmount` pacing.
- **Frontends (v1)**: Browser App (`web/` + `pw-wasm`) and Native Daemon
  (`peerwatch`: local Web UI in real browser + optional `mpv`/CLI/TUI). Tauri
  desktop shell moves to **v2**.

---

## Maintaining docs

```sh
python3 scripts/gen_catalog.py              # re-render protocol.md §7 tables
python3 scripts/gen_catalog.py --check      # verify catalog is up to date
python3 scripts/lint_invariants.py          # invariants, links, orphans, tiers, §refs
```
