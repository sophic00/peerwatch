---
tier: guide
---

# Configuration Reference

Every knob PeerWatch has, in one place. Values marked **default** are what
you get with no config file. Constants that are part of the wire protocol are
**not** here — those are normative in [protocol.md](../spec/protocol.md) §9 and
changing them is a protocol change, not a setting.

---

## 1. Where configuration lives

| Platform | Path |
|---|---|
| CLI / desktop | `$XDG_CONFIG_HOME/peerwatch/config.toml` (default `~/.config/peerwatch/config.toml`) |
| CLI / desktop cache | `$XDG_CACHE_HOME/peerwatch/` (default `~/.cache/peerwatch/`) |
| CLI runtime socket | `$XDG_RUNTIME_DIR/peerwatch/<pid>.sock` |
| Browser | No file. A subset is exposed in the UI and stored in IndexedDB. |

Precedence, highest first:

1. command-line flags (`pw daemon --relay …`)
2. environment variables (`PEERWATCH_RELAY_URL`)
3. `config.toml`
4. built-in defaults

The daemon logs the effective configuration (with secrets redacted) once at
startup at `info` level. If you are debugging, that line is the truth.

---

## 2. Full reference

```toml
# ─── signaling ────────────────────────────────────────────────────────────
[relay]
url = "wss://relay.example.org/pw"   # required unless using manual signaling
token = ""                           # optional bearer token for private relays
connect_timeout_ms = 5000
idle_ping_ms = 30000                 # keepalive; must be < relay idle timeout
backoff_initial_ms = 1000
backoff_max_ms = 30000
lobby_enabled = true                 # subscribe to the epoch-independent lobby

# ─── NAT traversal ────────────────────────────────────────────────────────
[ice]
stun = ["stun:stun.l.google.com:19302"]
turn = []                            # e.g. ["turn:turn.example.org:3478"]
turn_user = ""
turn_pass = ""                       # prefer turn_secret over a static password
turn_secret = ""                     # coturn use-auth-secret shared secret
turn_ttl_s = 3600                    # generated credential lifetime
ice_timeout_ms = 10000
consent_timeout_ms = 10000

# ─── identity ─────────────────────────────────────────────────────────────
[identity]
# keyfile = "~/.config/peerwatch/identity.key"   # default; mode 0600
passphrase_file = ""                 # planned: passphrase-at-rest (M5)

# ─── storage ──────────────────────────────────────────────────────────────
[storage]
cache_dir = ""                       # default: $XDG_CACHE_HOME/peerwatch
cache_cap_gib = 20                   # global LRU cap across streams
pin = []                             # stream_ids to never evict
preallocate = true                   # ftruncate when total_size is known
scrub_interval_s = 3600              # background full scrub period
journal_fsync_batch = 8              # pieces per durability barrier

# ─── upload policy ────────────────────────────────────────────────────────
[upload]
# target = 1.5x video bitrate, capped by measured uplink
target_multiplier = 1.5
uplink_fraction = 0.8                # never use more than this share of measured up
low_upload = false                   # set LOW_UPLOAD cap flag (metered)
serve_unknown = true                 # answer requests from unrated peers
score_weighted = true                # allocate by requester score

# ─── scheduler ────────────────────────────────────────────────────────────
[scheduler]
urgent_window_ms = 10000
buffer_target_ms = 60000
q_min = 4                            # normative floor, see protocol.md §9
q_max = 64                           # normative ceiling
hedge_sigma = 3.0                    # re-request at μ + 3σ
endgame_threshold = 0.95             # duplicate requests above this completion
backfill_enabled = true
bitfield_interval_ms = 1000

# ─── sync ─────────────────────────────────────────────────────────────────
[sync]
kp = 0.08                            # normative gains, see sync.md §3.2
ki = 0.0016
rate_clamp = 0.10                    # ±10 %
hard_seek_threshold_ms = 5000
antiwindup_band_ms = 2000
ewma_alpha = 0.2
min_rtt_window_ms = 30000
stall_low_ms = 2000                  # pause below this buffer
stall_resume_ms = 4000               # resume at this buffer

# ─── media ────────────────────────────────────────────────────────────────
[media]
ffmpeg = ""                          # default: discover on PATH
ffprobe = ""                         # default: discover on PATH
preset = "veryfast"                  # x264 preset for transcode-ahead
crf = 23
maxrate = "12M"
bufsize = "24M"
audio_bitrate = "160k"
target_segment_ms = 4000             # encoder max GOP
encode_ahead_ms = 120000             # throttle ceiling
transcode_head_start_ms = 15000
probe_timeout_ms = 30000

# ─── playback ─────────────────────────────────────────────────────────────
[player]
prefer = "auto"                      # auto | mse | mpv
mpv_path = ""                        # default: discover on PATH
mpv_sandbox = "auto"                 # auto | bubblewrap | firejail | none
range_server_port = 0                # 0 = random
mse_buffer_ahead_ms = 60000
mse_buffer_evict_ms = 30000

# ─── room defaults ────────────────────────────────────────────────────────
[room]
max_peers = 16
rekey_grace_ms = 30000
challenge_rate_ms = 10000
challenge_budget = 8
takeover_timeout_ms = 30000
buffering_gate_max_ms = 20000

# ─── logging / telemetry ──────────────────────────────────────────────────
[log]
level = "info"                       # error|warn|info|debug|trace
format = "pretty"                    # pretty | json
file = ""                            # default: stderr
[telemetry]
enabled = false                      # opt-in, local only, see observability.md
dir = ""                             # default: $XDG_STATE_HOME/peerwatch/telemetry
```

---

## 3. Environment variables

Every `[section].key` has a variable: `PEERWATCH_<SECTION>_<KEY>`, uppercase,
dots and dashes to underscores.

```sh
PEERWATCH_RELAY_URL=wss://relay.example.org/pw
PEERWATCH_ICE_STUN='["stun:stun.example.org:3478"]'   # JSON array
PEERWATCH_STORAGE_CACHE_CAP_GIB=50
PEERWATCH_LOG_LEVEL=debug
```

Arrays and tables are JSON. Scalars are parsed as TOML scalars.

The three variables that matter most operationally:

| Variable | Why you would set it |
|---|---|
| `PEERWATCH_RELAY_URL` | point at a self-hosted relay without editing a file |
| `PEERWATCH_ICE_TURN` | fix the "cannot connect to peer" error for symmetric NATs |
| `PEERWATCH_LOG_LEVEL` | get a usable bug report out of a user |

---

## 4. Browser configuration

A browser tab cannot read a config file. It exposes:

| Setting | Where | Default |
|---|---|---|
| Relay URL | build-time constant, overridable by `?relay=` | the project default |
| STUN/TURN | build-time constant | public STUN |
| Storage budget | `navigator.storage.estimate()` × 0.5 | 50 % of quota |
| Upload policy | UI toggle (metered) | on, `LOW_UPLOAD` if `navigator.connection.saveData` |
| Transcode | not available | — |
| mpv | not available | — |

`?relay=` is deliberately the only query-string override: it lets you test a
self-hosted relay without a rebuild, and it is the only setting a user can
meaningfully change without a settings UI. A build MUST NOT accept a
`?turn=` override, because a link could then route your traffic through an
attacker's TURN server.

---

## 5. Operational defaults worth knowing

These are the values people are surprised by:

- **`cache_cap_gib = 20`.** A peer is not an archive. A 100 GB stream on a
  20 GiB cap is a sliding window, and the peer advertises
  `storage_budget_mb` accordingly ([storage.md](../design/storage.md) §1.4).
- **`target_multiplier = 1.5`.** Upload target is 1.5× the video bitrate,
  never more than 80 % of measured uplink. A 20 Mbps host serving an 8 Mbps
  stream injects at ~2.5× realtime, which is how the swarm reaches full
  redundancy while watching.
- **`q_max = 64`** and **`rate_clamp = 0.10`** are normative. Changing them
  is a protocol change: the first bounds peer memory, the second bounds
  audible pitch shift.
- **`scrub_interval_s = 3600`.** A full scrub re-hashes every stored piece
  against its root. It is I/O-bound and deliberately infrequent; the sampled
  cross-check at startup covers the common case.
- **`mpv_sandbox = "auto"`** tries bubblewrap, then firejail, then runs
  unsandboxed with a warning. Native media parsing is a real attack surface
  ([security.md](../design/security.md) §5 E).

---

## 6. Profiles

Presets that set many keys at once. `profile` is a top-level key; explicit
keys override it.

```toml
profile = "default"    # default | low-bandwidth | seed-box | metered
```

| Key | `default` | `low-bandwidth` | `seed-box` | `metered` |
|---|---|---|---|---|
| `upload.target_multiplier` | 1.5 | 1.0 | 4.0 | 0.5 |
| `upload.low_upload` | false | false | false | true |
| `storage.cache_cap_gib` | 20 | 5 | 500 | 2 |
| `scheduler.buffer_target_ms` | 60000 | 120000 | 60000 | 90000 |
| `scheduler.backfill_enabled` | true | false | true | false |
| `player.mse_buffer_ahead_ms` | 60000 | 120000 | 60000 | 90000 |
| `media.preset` | veryfast | ultrafast | veryfast | ultrafast |

`seed-box` is for a machine that will sit in a room as a permanent seeder: it
keeps everything and uploads aggressively. `low-bandwidth` trades startup
latency for fewer concurrent requests.

---

## 7. Validation

Configuration is validated at startup. Invalid values are a **hard error**
with the offending key and the accepted range; there are no silent
corrections, because a silently clamped `q_max` or `rate_clamp` produces
behaviour nobody can explain later.

The three cross-field checks:

1. `ice.turn` non-empty ⇒ `turn_user` + (`turn_pass` | `turn_secret`).
2. `relay.url` set ⇒ `relay.idle_ping_ms` < the relay's idle timeout (90 s
   default); warn if ≥ 60 s.
3. `media.crf` ∈ [0, 51] and `media.maxrate` parses as a bitrate.

---

## 8. What is *not* configurable, and why

| Not configurable | Why |
|---|---|
| Block/piece/segment sizes | Wire constants; peers would disagree ([protocol.md](../spec/protocol.md) §6.1) |
| `max_peers` > 16 | Untested; partial mesh is not specified ([roadmap.md](../roadmap.md) §3) |
| Envelope max | Negotiated per connection ([protocol.md](../spec/protocol.md) §1.4) |
| Signature algorithms | Interop |
| `kp`/`ki` outside ±20 % of nominal | A different controller is a different sync spec |
| Disabling verification | Not a feature |

The `[sync] kp/ki` entry is the odd one: it is exposed for tuning on unusual
hardware, but a peer using wildly different gains will visibly lead or lag
the room. Treat a change here as a design change and record it in
[design-log.md](../design/design-log.md).
