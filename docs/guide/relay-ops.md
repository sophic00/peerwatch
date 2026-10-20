---
tier: guide
---

# Relay Operations

Self-hosting the signaling relay and TURN. The relay is the only piece of
infrastructure PeerWatch needs, and it is deliberately small enough to run on
the cheapest VPS you can find.

The relay's *protocol* is normative in [protocol.md](../spec/protocol.md) §11.
This document is about running one.

---

## 1. What you are running

Two independent services:

| Service | Crate | Purpose | Required? |
|---|---|---|---|
| Signaling relay | `pw-signal-relay` | WebSocket mailbox: SDP/ICE exchange, invite bootstrap, lobby | Yes, unless you use manual signaling |
| TURN | `coturn` (external) | relay media for pairs that cannot hole-punch | Strongly recommended |

The relay never sees media. TURN sees encrypted media it cannot read. Neither
is in the data path for a peer pair that punches through its NATs.

Sizing: a relay for 20 concurrent 16-peer rooms moves well under 1 MB/s and
needs < 100 MB RAM. It is not a video server.

---

## 2. Signaling relay

### 2.1 Minimal deployment

```sh
cargo build --release -p pw-signal-relay
./target/release/pw-signal-relay --listen 0.0.0.0:7878
```

Put it behind a TLS terminator (Caddy, nginx, or the relay's own
`--tls-cert`/`--tls-key`). WebRTC clients must reach it over `wss://` from an
`https://` page — mixed content is blocked, and there is no fallback.

```
# Caddyfile
relay.example.org {
    reverse_proxy 127.0.0.1:7878
}
```

Clients then set:

```toml
[relay]
url = "wss://relay.example.org/pw"
```

### 2.2 Configuration

```toml
[relay]
listen = "0.0.0.0:7878"
max_channels = 1000
max_peers_per_channel = 32
max_frame_bytes = 32768          # SignalEnvelope / InviteEnvelope
max_basis_bytes = 1024           # LOBBY_BASIS
max_signaling_bytes_per_s = 65536
idle_timeout_s = 90
ping_interval_s = 30
lobby_retention_s = 86400        # 24 h
lobby_max_records = 512
max_conns_per_ip = 32
max_channels_per_ip = 64
auth_token = ""                  # optional bearer token
```

Every value here maps to a normative limit in
[protocol.md](../spec/protocol.md) §11.4. Lowering one is fine; raising
`max_frame_bytes` above 32 KiB breaks clients that size their buffers to the
spec.

### 2.3 The three channel kinds

| Kind | Derivation | Who may subscribe | Lifetime |
|---|---|---|---|
| Invite | `BLAKE3("pw-invite-channel-v2" ‖ room_id ‖ invite_id)` | anyone holding the invite token | until the invite is used or expires |
| Epoch | `BLAKE3("pw-channel-v2" ‖ room_id ‖ epoch)` | anyone who can derive it — i.e. anyone with the room key | one epoch |
| Lobby | `BLAKE3("pw-lobby-v2" ‖ room_id)` | anyone who knows `room_id` | room lifetime + retention |

The relay cannot tell these apart, by design: all three are 32 opaque bytes.
It must therefore apply the *same* quota to all of them, which is why
`max_peers_per_channel` (32) is larger than a room (16) — the epoch channel
holds the current members, the lobby holds them too, and the invite channel
holds joiners.

> **The lobby is a real exposure.** Anyone who learns `room_id` can read
> lobby metadata: that the room exists, its current epoch, and when it last
> rekeyed. `room_id` is only shared with invitees, so this is the same
> exposure class as the invite itself — but an operator should know it is
> there. Set `lobby_retention_s = 0` to disable the lobby entirely; the
> consequence is that any member offline during a rekey is permanently
> evicted ([design-log.md](../design/design-log.md) C15).

### 2.4 What the relay must never do

- **Persist anything to disk.** Not channels, not envelopes, not basis
  records. A relay that logs envelope contents has broken the threat model.
- **Log aliases, channel hashes, or IPs at `info`.** Aggregate counters only
  ([observability.md](observability.md) §3.6).
- **Inspect, re-encode, or reorder envelopes.** `RelaySignal.envelope` is
  opaque bytes.
- **Assert `signal_pub` as truth.** The relay publishes a roster as a
  discovery hint; clients take the authenticated value from the host-signed
  `ROOM_STATE` ([protocol.md](../spec/protocol.md) §11.2). A relay that
  wanted to MITM would have to substitute a key, which produces a loud
  decryption failure rather than a silent splice.

### 2.5 Abuse handling

The relay cannot distinguish a griefer from a legitimate room, because it
cannot read anything. So abuse handling is structural:

| Abuse | Mitigation |
|---|---|
| Connection flood | `max_conns_per_ip`, per-IP rate limit |
| Channel flood | `max_channels_per_ip` |
| Byte flood per alias | `max_signaling_bytes_per_s` |
| Lobby record flood | `lobby_max_records`, retention |
| Reconnect storms | exponential backoff is client-side; the relay just closes |

If you need to ban an IP, that is a firewall rule. There is no per-room
moderation at the relay, because the relay has no rooms it can see.

---

## 3. TURN (coturn)

Roughly 10–15 % of peer pairs cannot hole-punch: symmetric NATs, most
corporate firewalls, some mobile carriers. Without TURN those pairs fail with
`L_NET_NO_TURN` ([errors.md](../spec/errors.md) §3.4). TURN is not optional
polish; it is the difference between "works for most people" and "works".

### 3.1 Minimal coturn

```conf
# /etc/coturn/turnserver.conf
listening-port=3478
tls-listening-port=5349
fingerprint
use-auth-secret
static-auth-secret=REPLACE_WITH_LONG_RANDOM
realm=relay.example.org
total-quota=100
bps-capacity=0
no-multicast-peers
no-cli
cert=/etc/letsencrypt/live/relay.example.org/fullchain.pem
pkey=/etc/letsencrypt/live/relay.example.org/privkey.pem
```

`use-auth-secret` means credentials are time-limited HMACs, not a static
password checked into a client build. A static password is a permanent,
shareable, unrevokable key to your relay's bandwidth.

### 3.2 Client credentials

The client derives them from the shared secret:

```
username   = "<expiry-unix-ts>:<peer-id>"
password   = base64(HMAC-SHA1(secret, username))
```

Set `turn_secret` in `config.toml` and let the client generate them
([config.md](config.md) §2), or run a tiny `/turn-credentials` endpoint and
hand out short-lived credentials. `turn_ttl_s` defaults to 1 hour.

### 3.3 TURN over TCP/TLS

WebRTC prefers UDP. When a network blocks UDP entirely, `turns:` on port 5349
is the escape hatch. Configure both:

```toml
[ice]
turn = ["turn:relay.example.org:3478", "turns:relay.example.org:5349"]
```

A peer that can only reach `turns:` will be slower but will work. Without it,
that peer cannot join at all.

### 3.4 Bandwidth budget

TURN relays media, so it is the only infrastructure with real bandwidth cost.
Estimate:

```
relayed_pairs  ≈ peers × 0.15          (the symmetric-NAT fraction)
relayed_bytes  ≈ relayed_pairs × bitrate × duration × 2   (both directions)
```

A 10-peer room watching a 2 h 8 Mbps film with 1.5 relayed pairs is roughly
`1.5 × 8 Mbps × 7200 s × 2 ≈ 21 GB`. Budget for it, or cap it with
`total-quota`.

---

## 4. Client-facing deployment checklist

- [ ] Relay reachable at `wss://…` with a valid certificate
- [ ] `PEERWATCH_RELAY_URL` set, or `[relay] url` in config
- [ ] STUN reachable (`stun:` on 3478/UDP)
- [ ] TURN reachable over UDP **and** TLS (3478/udp, 5349/tcp)
- [ ] `use-auth-secret` with a rotating secret, not a static password
- [ ] Relay metrics scraped; the four alerts in
      [observability.md](observability.md) §6 configured
- [ ] Relay logs contain no aliases, hashes, or IPs at `info`
- [ ] `lobby_retention_s` chosen deliberately (default 24 h)

Verification, from a client:

```sh
pw doctor
```

`pw doctor` checks relay reachability, STUN/TURN responses, credential
validity, local identity key permissions, ffmpeg presence, and mpv sandbox
availability, and prints a fix hint per failure. It is the first thing to ask
a user for.

---

## 5. Running without a relay

The signal envelope format is transport-agnostic, so manual signaling is
possible: two peers exchange SDP/ICE blobs out of band.

```sh
pw join --manual
# prints a ~2 KB token; paste it to a member
# paste their answer token back
```

This is supported for two reasons: it makes the relay genuinely optional
(no single point of failure or observation), and it is the escape hatch when
a relay is blocked. It is two round trips per join, so it is not the default
for rooms of 3+.

A manual join skips the invite channel but still runs the full invite proof,
`JOIN_ACCEPT`, and AUTH — manual signaling replaces the transport for the
bootstrap, not the authorization.

---

## 6. Upgrading

The relay is stateless, so upgrade is restart. The only consideration is that
restarting drops every WebSocket, which:

- does **not** break established meshes — the relay is only needed for new
  joins and reconnects ([connectivity.md](../design/connectivity.md) §6);
- does interrupt lobby catch-up, so a peer that is mid-rekey and offline
  during the restart waits for `lobby_retention` (it will not lose data; the
  basis records are re-derivable from any member).

Roll a restart during low activity. There is no drain mode in v1; clients
reconnect with backoff.
