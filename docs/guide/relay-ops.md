---
tier: guide
---

# Relay & TURN Operations

`pw-relay` is a **100% stateless in-memory WebSocket mailbox**. It stores zero
files on disk and retains zero lobby/room history after clients disconnect.
Normative relay wire framing lives in [protocol.md](../spec/protocol.md) §11.

---

## 1. Running `pw-relay`

```sh
cargo build --release -p pw-relay
./target/release/pw-relay --listen 127.0.0.1:7878
```

Place behind a TLS reverse proxy (Caddy/nginx) so `https://` browser clients
connect over `wss://`:

```caddy
relay.example.org {
    reverse_proxy 127.0.0.1:7878
}
```

### 1.1 Relay limits (`relay.toml` / flags)

```toml
listen = "127.0.0.1:7878"
max_channels = 1000
max_peers_per_channel = 32
max_frame_bytes = 32768             # normative 32 KiB cap
max_signaling_bytes_per_s = 65536   # 64 KiB/s per alias
idle_timeout_s = 90
ping_interval_s = 30
max_conns_per_ip = 32
max_channels_per_ip = 64
auth_token = ""                     # optional bearer token
```

### 1.2 Privacy rules

The relay MUST NEVER persist envelopes to disk or log aliases, channel hashes,
or IPs at `info` level. Only aggregate operational counters (active channels,
connected peers, forwarded/dropped signal counts, error code totals) are
exported.

---

## 2. Running `coturn` (TURN)

10–15% of peer pairs sit behind symmetric NATs or UDP-blocking firewalls and
need TURN. Configure `coturn` with `use-auth-secret` (never static client
passwords) on both UDP 3478 and TLS 5349:

```conf
listening-port=3478
tls-listening-port=5349
fingerprint
use-auth-secret
static-auth-secret=REPLACE_WITH_LONG_RANDOM
realm=relay.example.org
total-quota=100
no-multicast-peers
no-cli
```

---

## 3. Manual signaling escape hatch

Because `SignalEnvelope` and `InviteEnvelope` are transport-agnostic, two
peers can always bootstrap without a relay via out-of-band offer/answer paste
(`peerwatch join --manual`), still executing the full invite proof and
DTLS-bound `AUTH`.
