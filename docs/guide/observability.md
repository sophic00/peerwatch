---
tier: guide
---

# Observability

What PeerWatch emits, what it deliberately does not, and how to answer
"why is this room slow?" without a debugger.

The roadmap's telemetry list ([roadmap.md](../roadmap.md) §4) is the
*measurement plan*; this document is the *instrumentation contract*.

---

## 1. Three channels

| Channel | Audience | Volume | Contains |
|---|---|---|---|
| Structured logs | operator | low | lifecycle events, errors, decisions |
| Metrics | operator / dashboards | constant | counters, gauges, histograms |
| Local trace | developer, bug reports | opt-in, high | per-message event trace |

They are separate on purpose. A metric is cheap and always on; a trace is
expensive and only when asked. Putting per-block detail in logs is how a
1 GB log file happens.

---

## 2. Structured logs

JSON when `log.format = "json"`, human-readable otherwise. Every line has:

```json
{"ts":"2025-01-14T10:22:31.442Z","level":"info","target":"pw_core::room",
 "room":"…","stream":"…","peer":"pw:vk3f7q…","msg":"host lease accepted",
 "term":4,"cert":"…"}
```

Rules:

- `room`, `stream`, and `peer` are **truncated identifiers** (`pw:vk3f7q…`,
  first 8 chars). A full key is never logged.
- No message bodies, no chat text, no media bytes, no keys, no SDP.
- Every log line about a peer action names the peer. "Banned a peer" is
  useless; "banned `pw:vk3f7q…` for bad block proof (piece 812, block 7)" is
  actionable.
- Errors use the codes from [errors.md](../spec/errors.md), e.g.
  `"code":"L_NET_NO_TURN"`. A log line without a code is a bug in the
  instrumentation.

Levels:

| Level | Use |
|---|---|
| `error` | something failed and the user will notice |
| `warn` | something degraded; the room still works |
| `info` | lifecycle: join, leave, lease, rekey, takeover, stream start/end |
| `debug` | decisions: scheduling choices, controller output, policy changes |
| `trace` | per-message. Never on in production; produces ~MB/s |

---

## 3. Metrics

Names are `peerwatch_<subsystem>_<name>_<unit>`. Prometheus text format for
the relay; an in-process registry for peers (exposed on a local socket when
`telemetry.enabled`).

### 3.1 Room and governance

| Metric | Type | Labels | Why |
|---|---|---|---|
| `peerwatch_room_peers` | gauge | — | roster size |
| `peerwatch_room_term` | gauge | — | current lease term |
| `peerwatch_room_epoch` | gauge | — | current key epoch |
| `peerwatch_room_state_version` | gauge | — | monotonic; a stall here means no host |
| `peerwatch_proposal_seconds` | histogram | `kind` | time from proposal to certificate |
| `peerwatch_proposal_failures_total` | counter | `reason` | `expired`, `no_quorum`, `contested` |
| `peerwatch_takeover_seconds` | histogram | — | host-loss detection to new lease |
| `peerwatch_takeover_failures_total` | counter | `reason` | `no_proof`, `no_quorum`, `challenge_timeout` |
| `peerwatch_rekey_seconds` | histogram | `trigger` | kick / rotation |
| `peerwatch_rekey_recovery_total` | counter | — | peers that needed the lobby |

`peerwatch_takeover_seconds` and `peerwatch_rekey_recovery_total` are the two
that tell you whether the design's hardest claims hold.

### 3.2 Data plane

| Metric | Type | Labels | Why |
|---|---|---|---|
| `peerwatch_swarm_pieces_have` | gauge | — | local completeness |
| `peerwatch_swarm_availability` | histogram | — | piece holder count distribution |
| `peerwatch_block_seconds` | histogram | `bucket` | fetch latency |
| `peerwatch_block_failures_total` | counter | `reason` | `timeout`, `bad_proof`, `cancelled` |
| `peerwatch_requests_in_flight` | gauge | `peer` | `Q` in practice |
| `peerwatch_scheduler_q` | histogram | — | adaptive `Q` distribution |
| `peerwatch_cancel_blocks_total` | counter | — | waste per seek |
| `peerwatch_hedge_total` | counter | — | tail-latency cost |
| `peerwatch_buffered_amount_bytes` | gauge | `peer`, `channel` | SCTP backpressure high-water |
| `peerwatch_upload_bytes_total` | counter | — | contribution |
| `peerwatch_download_bytes_total` | counter | — | |

`peerwatch_buffered_amount_bytes` is the one that catches the classic bug:
`Q` is small, everything looks fine, and latency is terrible because the app
queue is behind a congested SCTP association.

### 3.3 Sync

| Metric | Type | Labels | Why |
|---|---|---|---|
| `peerwatch_sync_drift_ms` | histogram | — | `e(t)`, the whole point |
| `peerwatch_sync_rate` | gauge | — | controller output |
| `peerwatch_sync_hard_seeks_total` | counter | `reason` | `post_stall`, `seek`, `return_to_live` |
| `peerwatch_sync_min_rtt_ms` | gauge | `peer` | RTT estimator health |
| `peerwatch_playback_stalls_total` | counter | — | user-visible |
| `peerwatch_playback_stall_ms` | histogram | — | user-visible |
| `peerwatch_free_roam_total` | counter | — | feature usage |

Target: `peerwatch_sync_drift_ms` p95 under 100 ms
([performance.md](../design/performance.md) §8). If it is not, the controller
gains or the RTT estimator are wrong, in that order.

### 3.4 Storage

| Metric | Type | Labels | Why |
|---|---|---|---|
| `peerwatch_store_bytes` | gauge | `tier` | hot / warm / native |
| `peerwatch_store_quota_bytes` | gauge | — | budget |
| `peerwatch_store_evictions_total` | counter | `reason` | LRU / quota / scrub |
| `peerwatch_store_scrub_failures_total` | counter | — | silent corruption |
| `peerwatch_store_fsync_seconds` | histogram | — | durability cost |
| `peerwatch_opfs_pack_count` | gauge | — | metadata pressure |

### 3.5 Media

| Metric | Type | Labels | Why |
|---|---|---|---|
| `peerwatch_transcode_speed_ratio` | gauge | — | `encoded_ms / wall_ms`; < 1.0 means underrun |
| `peerwatch_transcode_underruns_total` | counter | — | encoder fell behind |
| `peerwatch_transcode_cpu_percent` | gauge | — | the 30–70 % row in performance.md |
| `peerwatch_segment_seconds` | histogram | — | actual vs `target_segment_ms` |
| `peerwatch_probe_seconds` | histogram | — | cold-start component |
| `peerwatch_cold_start_seconds` | histogram | — | invite → first frame |

`peerwatch_transcode_speed_ratio` below 1.0 sustained is the single clearest
signal that a machine cannot host the stream it proposed.

### 3.6 Relay

| Metric | Type | Labels | Why |
|---|---|---|---|
| `peerwatch_relay_channels` | gauge | `kind` | epoch / invite / lobby |
| `peerwatch_relay_peers` | gauge | `kind` | |
| `peerwatch_relay_bytes_total` | counter | `direction` | |
| `peerwatch_relay_signals_total` | counter | `result` | `ok`, `dropped`, `rejected` |
| `peerwatch_relay_errors_total` | counter | `code` | from [errors.md](../spec/errors.md) §2 |
| `peerwatch_relay_lobby_records` | gauge | — | retention pressure |
| `peerwatch_relay_ws_duration_seconds` | histogram | — | churn |

The relay must never emit a metric label containing an alias, channel hash,
or IP. Aggregate only.

---

## 4. Local trace (opt-in)

`telemetry.enabled = true` writes a per-session event trace to
`$XDG_STATE_HOME/peerwatch/telemetry/<session>.cbor`. It is:

- **local only** — never uploaded, never phoned home;
- **opt-in** — off by default, one toggle;
- **bounded** — ring buffer, default 64 MiB, oldest dropped;
- **canonical CBOR** — the same encoding `pw-sim` compares against
  ([core-api.md](../spec/core-api.md) §9).

A trace records every `Event` and `Command` with timestamps and a
`seq`, plus the periodic core snapshot. That is enough to replay a session
in `pw-sim` and reproduce a scheduling or sync bug deterministically.

What a trace deliberately excludes: media bytes, chat bodies, keys, and full
peer public keys. A trace is safe to attach to a bug report without leaking
the film someone was watching or who they were watching it with.

**The bug-report workflow:**

```sh
pw status --trace-export > bug.cbor     # or copy the telemetry file
# attach bug.cbor + `pw status` output + config (secrets redacted)
```

A maintainer then runs `pw-sim replay bug.cbor` and gets the exact command
stream.

---

## 5. What we do not instrument, and why

| Not collected | Why |
|---|---|
| Chat contents | Not ours to collect |
| Stream titles / filenames | Identifies content; the trace records `stream_id` only |
| Full peer public keys in metrics | Linkability across rooms |
| IP addresses in metrics | Personal data; the relay logs them only for abuse handling, with a short retention |
| Anything at all, by default | Opt-in is the default for a reason |

A distributed system with no server has no natural place to aggregate
telemetry, which is a feature: there is no dashboards-to-be-sold path here.
Operators who want fleet metrics run their own relay and read its Prometheus
endpoint.

---

## 6. Alerting suggestions (relay operator)

If you run a relay, these four are worth a page:

| Alert | Condition | Meaning |
|---|---|---|
| `RelayErrorRate` | `rate(peerwatch_relay_errors_total[5m]) > 1/s` for 10 m | clients are failing |
| `RelayChannelLeak` | `peerwatch_relay_channels{kind="epoch"}` never decreases | idle GC is broken |
| `RelayLobbyPressure` | `peerwatch_relay_lobby_records > 400` | retention too long, or a room is churning |
| `RelayWSDuration` | p50 `ws_duration` < 10 s | clients are flapping, likely a bad ICE config |

The last one is the most useful in practice: a healthy relay sees long-lived
WebSocket connections. Short ones mean peers are connecting and giving up,
which is almost always a TURN or STUN misconfiguration on the client side.
