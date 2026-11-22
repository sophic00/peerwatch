---
tier: guide
---

# Configuration Reference

Runtime knobs for the PeerWatch native daemon (`peerwatch`). Wire constants
(32 KiB block, ≈ 4 s segment, 64 KiB envelope) are normative in
[protocol.md](../spec/protocol.md) and are not user-configurable.

---

## 1. Locations & precedence

| Item | Path |
|---|---|
| Config | `$XDG_CONFIG_HOME/peerwatch/config.toml` |
| Identity | `$XDG_CONFIG_HOME/peerwatch/identity.key` (`0600`) |
| Cache | `$XDG_CACHE_HOME/peerwatch/` |
| Runtime control socket | `$XDG_RUNTIME_DIR/peerwatch/<pid>.sock` |

Precedence (highest first): CLI flags → `PEERWATCH_<SECTION>_<KEY>` env vars →
`config.toml` → defaults. Invalid values fail fast at startup.

---

## 2. Reference `config.toml`

```toml
profile = "default"                 # default | low-bandwidth | seed-box | metered

[relay]
url = "wss://relay.example.org/pw"
token = ""                          # optional bearer token
connect_timeout_ms = 5000
idle_ping_ms = 30000
backoff_initial_ms = 1000
backoff_max_ms = 30000

[ice]
stun = ["stun:stun.l.google.com:19302"]
turn = []                           # e.g. ["turn:turn.example.org:3478", "turns:turn.example.org:5349"]
turn_user = ""
turn_pass = ""
turn_secret = ""                    # coturn use-auth-secret HMAC secret
turn_ttl_s = 3600
ice_timeout_ms = 10000
consent_timeout_ms = 10000

[storage]
cache_dir = ""                      # default: $XDG_CACHE_HOME/peerwatch
cache_cap_gib = 20
pin = []                            # stream_ids to keep pinned
scrub_interval_s = 3600

[upload]
target_multiplier = 1.5
uplink_fraction = 0.8
low_upload = false
score_weighted = true

[scheduler]
urgent_window_ms = 10000
buffer_target_ms = 60000
q_min = 4
q_max = 64
sctp_low_watermark_bytes = 131072   # 128 KiB aggregate data + bulk
sctp_high_watermark_bytes = 262144  # 256 KiB aggregate data + bulk
hedge_sigma = 3.0
endgame_threshold = 0.95
backfill_enabled = true

[sync]
kp = 0.30
ki = 0.0225
rate_clamp = 0.05                   # ±5%
hard_seek_threshold_ms = 2000
antiwindup_band_ms = 1500
ewma_alpha = 0.25
min_rtt_window_ms = 30000
stall_low_ms = 2000
stall_resume_ms = 4000

[media]
ffmpeg = ""                         # auto-discover on PATH
ffprobe = ""                        # auto-discover on PATH
encoder = "auto_hw"                 # auto_hw | vaapi | nvenc | videotoolbox | qsv | libx264
preset = "veryfast"                 # libx264 fallback preset
crf = 23
maxrate = "12M"
bufsize = "24M"
audio_bitrate = "160k"
encode_ahead_ms = 120000
transcode_head_start_ms = 12000
probe_timeout_ms = 30000

[player]
mode = "web"                        # web (local browser MSE) | mpv | headless
local_ui_port = 0                   # 0 = random on 127.0.0.1
open_browser = true
mpv_path = ""
mpv_sandbox = "auto"                # auto | bubblewrap | firejail | none

[room]
default_mode = "democratic"         # democratic | collaborative
max_peers = 16
takeover_timeout_ms = 20000
buffering_gate_max_ms = 20000

[log]
level = "info"                      # error | warn | info | debug | trace
format = "pretty"                   # pretty | json
```

---

## 3. Browser overrides

Browser builds accept `?relay=<wss-url>` to point at a self-hosted relay
without rebuilding, and expose upload policy / metered toggles in the UI.
Browser builds MUST NOT accept `?turn=` URL overrides.
