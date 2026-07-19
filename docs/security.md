# Security & Threat Model

PeerWatch is designed for **small groups of people who know each other**
(5–16), watching user-supplied video over an adversarial internet. This
document states what we defend against, how, and (just as importantly) what
we explicitly do not.

---

## 1. Assets and adversaries

**Assets:** the room's privacy (who's watching what), the integrity of the
video bytes each peer plays, the integrity of room governance (votes, kicks,
clock authority), availability (a griefer shouldn't be able to freeze the
room), and each peer's identity continuity.

**Adversaries considered:**

- **A. Outsider on the internet** — doesn't have the token, tries to join or snoop.
- **B. The signaling relay operator** — sees envelopes, tries to read/join/correlate.
- **C. Network MITM** — ISP-level observer/censor between peers.
- **D. Malicious room member** — has the token, poisons data, forges votes, floods, griefs.
- **E. Malicious *content*** — a proposed file crafted to exploit media parsers.

## 2. Defense layers (and which adversary each defeats)

```
L0  Transport encryption & authentication — DTLS (free with WebRTC)     → C, A
L1  Room authorization — invite-token room_psk in the AUTH handshake    → A, B
L2  Blind signaling — AEAD-encrypted envelopes, hashed channel names    → B
L3  Identity & signatures — Ed25519 on every control message            → D (forgery)
L4  Data integrity — SHA-256 piece hashes committed in the manifest     → D (poisoning)
L5  Behavioral defense — scoring, rate limits, strikes, auto-bans       → D (availability)
L6  Social governance — majority kick votes                             → D (judgment calls)
```

The design goal: **cryptography should handle everything cryptography *can*
handle. Votes only decide taste.**

## 3. Automatic elimination of bad peers (the L5 machinery)

Every peer maintains, per connected peer: throughput/latency estimates
(scheduler inputs), a **reliability score** `r_i ∈ [0,1]`, and a strike
count. Rules, in escalating order:

| Offense | Evidence | Response |
|---------|----------|----------|
| **Piece fails SHA-256 vs manifest** | objective, recomputable | **Instant local ban.** Poisoning is unambiguous, there is no innocent explanation. The bad piece is dropped, the piece is re-fetched from someone else. |
| **Signature failure on any control message** | objective | Instant local ban (forgery or corruption). |
| **Protocol violation** (malformed/oversized/out-of-state messages) | objective | Strike. **3 strikes = ban**. |
| **Rate abuse** (request floods, message floods beyond caps) | objective | Requests deprioritized → choked → dropped. Strikes accrue. |
| **Timeouts/flakiness** | statistical | `r_i` decays → scheduler smoothly avoids the peer ([distribution.md](distribution.md) §4.2). Recovers slowly on good behavior. |
| **Freeloading** (never uploads) | statistical | *Not* bannable, browser peers on metered links are legitimate. Served last and slowest only. |

**Gossip:** a peer that auto-bans someone broadcasts a signed
`BAN_REPORT { offender, reason, evidence }`. For poisoning, the evidence
(piece index + claimed hash) is **independently verifiable** by any peer
that downloads that piece, so liars expose themselves by triggering L4. A
target that accrues reports from distinct signers becomes a `KICK` proposal
candidate automatically surfaced by the UI.

**Why local bans suffice as the foundation:** a ban is a *refusal to
interact*. One honest peer banning an attacker protects that peer fully.
Room-wide ejection (for social offenses) is the kick vote's job
([governance.md](governance.md)).

## 4. Per-adversary analysis

**A. Outsider.** Cannot find the room (channel name = hash of room ID),
cannot read signaling (AEAD under token-derived key), cannot complete AUTH
(room_psk required), cannot MITM the transport (DTLS with
fingerprint-pinned certs, and our transcript binds those fingerprints,
[identity.md](identity.md)). Residual: *token theft* → treat tokens like
passwords. Share over secure channels.

**B. Relay operator.** Sees: channel hashes, envelope sizes/timing, peer
counts. Cannot read, join, or impersonate. Residual: metadata correlation
(who talks when), mitigatable in principle with padding/fixed-rate
signaling, **out of v1 scope**, documented.

**C. Network MITM.** Sees encrypted DTLS datagrams. Can infer *that* peers
talk and roughly how much (video-sized flows). Cannot see content, inject,
or downgrade. Residual: traffic analysis, same class as any encrypted
P2P. Documented.

**D. Malicious member.** Faces L3–L6: can't forge (signatures), can't
poison (hashes), can't flood (caps → strikes → bans), can't impersonate the
Host's clock (signed SYNC), can't stuff ballots (one key one vote, tally
recomputed by all). Remaining power: being annoying *within* the rules
(off-topic proposals, leaving at dramatic moments), which is what kick
votes are for.

**E. Malicious content.** The hardest one, honestly. A crafted video could
target ffmpeg/mpv/GStreamer decoder bugs. Mitigations: (1) browser peers
decode in the browser's sandboxed media pipeline (strong). (2) transcode/
remux happens on the *proposer's* machine, peers never run ffmpeg on
untrusted input. What they receive is the already-normalized fMP4/WebM
stream. (3) keep mpv/GStreamer updated. (4) the trust norm: don't accept
proposals from people you wouldn't hand a video file to. This residual risk
is inherent to *any* user-supplied-media tool and is stated plainly.

## 5. What the signatures cover (and don't)

Signed: AUTH, PROPOSAL, VOTE, VOTE_RESULT, SYNC, START_AT,
SOURCE_TAKEOVER, KICK_NOTICE, BAN_REPORT, CHAT, MANIFEST (+APPEND/FINAL).
**Unsigned: data-plane blocks**, deliberately. A block's authenticity *is*
its piece hash (L4). Per-block signatures would cost CPU for zero added
security, since a forged block fails the piece hash exactly like a corrupted
one.

## 6. Honest limitations (read before relying on this)

- **Sybil reality**: a kicked user holding the token can return with a
  fresh key (new peer ID). At 5–16-person social scale this is manageable
  (re-kick is cheap, fresh keys are visibly strangers), but PeerWatch rooms
  are **not** resistant to a determined infiltrator with a leaked token.
  Token hygiene is the real perimeter. Expiring/rotating tokens are a
  possible extension.
- **No real-world identity**: a peer ID means "same key", not "Alice".
  Optional TOFU known-peers list and fingerprint comparison are planned
  niceties, not v1 ([identity.md](identity.md) §5).
- **Key theft = identity theft** (same class as SSH keys). Passphrase-at-
  rest for native keys is planned.
- **Metadata at the relay** (who's in which channel, when) exists wherever
  the relay runs. Self-hosting minimizes exposure, the relay stores nothing
  persistently.
- **DoS at the edges we can't fix**: an attacker who *is* your only route
  to a piece can refuse to serve it (the scheduler routes around). An ISP
  can block UDP (WebRTC needs it, TURN-over-TCP config is the escape hatch).
- **Legal note**: PeerWatch shares files among people who each must
  lawfully possess the content. The software is a tool. Use it accordingly.

## 7. Update policy

Security-relevant dependencies (webrtc-rs, dalek suites, blake3/sha2, and
on hosts ffmpeg/mpv/GStreamer) are pinned and reviewed on upgrade. The
protocol version in `HELLO` allows mandatory-upgrade flags if a wire-level
flaw is ever found.
