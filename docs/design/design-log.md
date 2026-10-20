---
tier: design
---

# Design Log

**Why PeerWatch v2 looks the way it does, and what it rejected.**

This is the home for every rejected alternative, every correction made during
the rewrite, and every trade-off that is genuinely a trade-off. The other
documents describe *what is*; this one explains *why not something else*.

Read this if you are about to change a constant, re-open a decision, or
wonder why an obviously simpler design was not chosen.

Two sections:

- **Part A — Decisions (D1–D28).** Each has the alternative that was
  considered and the reason it lost. Most of these were settled before any
  code existed, so treat a change here as a design change, not a cleanup.
- **Part B — Corrections.** Errors found during the v2 rewrite, with the
  reasoning that produced the fix. These are recorded because several were
  subtle enough to be re-introduced by a well-meaning refactor.

---

# Part A — Decisions

## Transport and platform

### D1. Rust for the whole stack

**Chosen:** one Rust core, compiled to native and `wasm32`.
**Rejected:** Go core + separate TypeScript client; Go-native with a thin
browser viewer.
**Why:** browser peers must run the *full* protocol — chunking, hashing,
scheduling, voting, syncing — because a browser tab uploads as well as
downloads. A thin browser viewer would be a different protocol participant
and the mesh would need two implementations of every rule. Rust gives one
implementation for both targets. Go's WASM footprint and GC pauses are poor
fits for a real-time P2P stack in a tab.

### D2. WebRTC as the only transport

**Chosen:** WebRTC for every peer pair; `webrtc-rs` natively, built-in
`RTCPeerConnection` in browsers.
**Rejected:** raw TCP/QUIC for native peers; WebTransport; a custom UDP
protocol.
**Why:** a web page cannot open a raw socket, so browsers can only do WebRTC.
Since browsers are full participants, native peers must speak WebRTC too, or
every room becomes two protocols with a bridge. WebTransport is
client–server over HTTP/3 and needs a relay, so it is a relay transport, not
a P2P one. DTLS encryption comes free, which removes a whole class of
hand-rolled crypto bugs.

### D3. Browser WebRTC on the main thread

**Chosen:** `RTCPeerConnection` and MSE on the main thread; core, crypto,
OPFS, and verification in a Worker.
**Rejected:** WebRTC inside the Worker (the "clean" split).
**Why:** `RTCPeerConnection` is a `Window`-scoped API. Exposing it in workers
is an open W3C issue and Firefox/Safari do not implement it. Conversely,
OPFS *synchronous* handles only exist in Workers. The split is therefore
forced by the platform, not chosen: network + player on the main thread,
CPU + storage in the Worker, bridged by transferable `ArrayBuffer`s.

### D4. Blind relay + per-peer signaling encryption

**Chosen:** self-hostable WebSocket mailbox; per-peer X25519 +
XChaCha20-Poly1305 `SignalEnvelope`.
**Rejected:** a room-wide signaling key; manual copy-paste only; a DHT.
**Why:** rooms of 3+ people need hands-free rendezvous. A room-wide key lets
any member read any other member's SDP and ICE candidates — including a
kicked member until the rekey completes. Per-peer encryption costs one extra
key exchange and removes that. Manual copy-paste remains implementable (the
envelope format is transport-agnostic) but is a poor default for 3+ people.
A DHT is the wrong shape for small, secret, ephemeral rooms.

### D5. Full mesh, ≤16 peers

**Chosen:** one connection per pair; target 5–16.
**Rejected:** partial-mesh overlay from day one; an SFU.
**Why:** `N(N−1)/2` connections is 45 at 10 peers, ~15 per client at 16 —
comfortable for desktop browsers and native processes. An SFU reintroduces
the media server the project exists to avoid. Partial mesh is designed-for
(the protocol does not assume connectivity) but not specified; see
`roadmap.md` §3.

---

## Media

### D6. fMP4 H.264/AAC as the canonical format

**Chosen:** fragmented MP4, H.264 + AAC; WebM/VP9/Opus as a secondary
profile only with unanimous codec support.
**Rejected:** share original files; transcode everything to WebM.
**Why:** MSE support for fMP4 H.264/AAC is the broadest combination across
Chrome, Firefox, Safari and WebKitGTK. mpv plays the identical bytes, so CLI
and desktop consume the same stream as the browser with no second
distribution. Sharing originals only works if every peer is a desktop with
mpv. Transcoding everything to WebM strands Safari/WebKitGTK peers, whose
MSE WebM support is partial.

### D7. Remux when possible, live transcode-ahead otherwise

**Chosen:** `-c copy` container swap; else a throttled `libx264 veryfast`
encoder running 60–120 s ahead of the playhead.
**Rejected:** transcode the whole file before the vote; no transcode at all.
**Why:** full pre-transcode is minutes of dead time for a 2 h film, which is
unacceptable for a watch party. No transcode strands every non-H.264 source.
Transcode-ahead trades a ~15 s head start and 30–70 % of one machine for
"any file works".

### D8. Room-wide codec intersection, not proposer-only validation

**Chosen:** the proposal is admissible only if *every connected peer* lists
`StreamParams.codecs` in `Caps.codecs`.
**Rejected:** validate against the proposer's own browser.
**Why:** proposer-only validation can start a stream that one peer cannot
decode, and that peer discovers it after the vote. The room is the unit of
compatibility. This is why `Caps.codecs` carries exact codec strings rather
than profile names — two peers can both support `FMP4` and disagree on
H.264 profile/level.

### D9. 32 KiB blocks, 512 KiB pieces, 4 s segments

**Chosen:** block 32 KiB, piece = 16 blocks, segment ≈ 4 s keyframe-aligned.
**Rejected:** 64 KiB blocks; pieces aligned to segments.
**Why:** a 32 KiB block plus a 4-hash Merkle proof plus envelope metadata
fits a 64 KiB DataChannel message with room to spare; 64 KiB blocks plus
overhead did not, and "the DataChannel max message size is
implementation-dependent" is not a foundation. 512 KiB pieces keep manifest
and bitfield overhead negligible. 4 s is the DASH/HLS seek-granularity sweet
spot. Pieces are deliberately *not* aligned to segments: a piece may span a
segment boundary, which the segment table records.

---

## Data plane

### D10. Per-block Merkle proofs, not per-piece hashes

**Chosen:** each `BLOCK` carries a Merkle path to its piece root.
**Rejected:** hash the assembled 512 KiB piece and ban "the sender".
**Why:** a piece's 16 blocks can come from 16 different peers. A piece-level
hash tells you the piece is bad but not who made it bad, so the only
available response is to ban everyone who contributed — which lets a single
malicious peer frame an honest one. Per-block proofs make blame exact and
cost ~0.4 % overhead.

**Consequence worth stating:** per-block proofs give exact *local*
attribution. They do not give transferable attribution — that needs the
sender's signature over the `BlockProof` (see C11).

### D11. Stable `stream_id`, append-only manifest, final `content_id`

**Chosen:** `stream_id` is a random 128-bit value fixed at proposal time;
`content_id` is assigned only by `MANIFEST_FINAL`.
**Rejected:** `content_id = BLAKE3(manifest)`.
**Why:** during a live transcode the manifest grows. Hashing a growing
manifest means the content ID changes every append, which invalidates every
in-flight request, every storage key, and every bitfield reference. A stable
`stream_id` for the data plane plus a final `content_id` for the cache key
decouples the two.

### D12. Deterministic CBOR

**Chosen:** RFC 8949 §4.2 deterministic encoding.
**Rejected:** bincode, protobuf, postcard.
**Why:** the protocol hashes objects (piece roots, proposals, certificates)
and those hashes must be stable across implementations. bincode is not
schema-evolution-safe and has no canonical form; protobuf's canonical form is
not specified by the language. CBOR is self-describing, tolerates unknown
keys, and has a well-defined canonical encoding.

### D13. Chunked manifest on a separate `bulk` channel

**Chosen:** signed head + 32 KiB chunks on `bulk`.
**Rejected:** one large manifest message on `control`.
**Why:** a 6 GB film's piece roots are ~400 KB and a 100 GB film's are
~6.4 MB. A single message of that size exceeds any reasonable envelope, and
putting it on `control` head-of-line-blocks `SYNC`, votes, and chat behind
it. A separate ordered channel keeps control latency independent of manifest
size.

### D14. Roaring bitfields, run-optimized

**Chosen:** Portable Roaring, with a sender requirement to emit run
containers for long runs.
**Rejected:** raw `N/8` bitfields; a generic compressor (gzip/zstd).
**Why:** a complete seeder's bitfield is one run (~15 B vs 1.5 KB raw), and
a sliding-window peer is ~470 B. But Roaring is **not** universally smaller:
an un-optimized dense chunk is an 8 KiB bitmap container, which is *larger*
than the 1.5 KB raw bitfield. So run-optimization is a normative sender
requirement, not a tuning knob. A generic compressor does not know about
piece-index structure and costs CPU every tick.

### D15. Broadcast the full bitfield every second

**Chosen:** full-state rebroadcast at 1 Hz.
**Rejected:** incremental `HAVE`/`HAVE_NOT` chatter.
**Why:** a lost incremental update leaves a peer permanently believing
something false until the next full sync, which needs its own repair
protocol. Full rebroadcast is self-correcting: one lost message costs 1 s of
staleness. At ~470 B/peer/s this is a few KB/s room-wide, which is cheap
enough that the simplicity wins.

### D16. Capacity-proportional weighted-random peer selection

**Chosen:** `p_i = c_i / Σc_j`, where `c_i = θ_i · max(0, 1 − u_i) · r_i`.
**Rejected:** `argmax(c_i)`; uniform random; round-robin; tit-for-tat.
**Why:** `argmax` herds — every requester independently picks the same "best"
peer (usually the Host), its queue inflates, all estimates go stale
together, and the herd stampedes elsewhere. Uniform random gives a 1 Mbps
peer as many requests as a 100 Mbps one. Tit-for-tat has good incentives but
no latency model. Weighted random spreads load proportional to capacity using
only local, stale-tolerant information, and smoothly avoids punished peers
as `r_i` decays.

An earlier formula, `c_i = θ_i / (1 + o_i) · r_i`, mixed units (bits/s
divided by a block count) and double-counted queueing, since `θ_i` is
already measured *while* `o_i` blocks are outstanding. The utilization form
is dimensionally consistent.

### D17. BDP-adaptive `Q`, clamped to [4, 64]

**Chosen:** `Q = clamp(⌈θ̂·RTT̂ / 32 KiB⌉, 4, 64)`.
**Rejected:** a fixed `Q` (8 or 16).
**Why:** throughput is capped by `in_flight_bytes / RTT`. A fixed `Q = 8` on
a 100 Mbps × 100 ms path caps it at ~20 Mbps, silently. The clamp floor of 4
covers LAN and broadband; the ceiling of 64 bounds memory and lets a single
peer's queue stay meaningful.

### D18. `CANCEL` kills queued requests only

**Chosen:** in-flight blocks complete and are stored anyway.
**Rejected:** purge everything on seek.
**Why:** a completed block is a valid block of the same stream, so it is
useful if the viewer seeks back. Only queued requests are pure waste. The
waste with `CANCEL` is bounded by the BDP (~31 KB at 5 Mbps × 50 ms); without
it, a seek drains 32–64 queued blocks per peer, which is 10–20 MB across a
10-peer mesh and 4–8 s of extra post-seek stall on a 2 Mbps path.

---

## Sync

### D19. One clock authority per `(stream, term)`

**Chosen:** the Host's transport state is the reference; a lease plus a term
number makes "one authority" true across host changes.
**Rejected:** averaging peer clocks; anyone-controls; host-only with no
escape.
**Why:** watch-party sync is a social feature ("we are at the same scene"),
not a fairness problem. One unambiguous reference converges instantly.
Averaging produces a reference nobody is watching. The escape hatch is
per-peer free-roam, not a second authority.

### D20. PI controller on playback rate, not a three-tier deadband

**Chosen:** `rate = clamp(1 − Kp·ê − Ki·∫ê, 0.9, 1.1)`, `Kp = 0.08`,
`Ki = 0.0016`, critically damped.
**Rejected:** the v1 three-tier scheme (seek / ±5 % / 1.0×); full PID.
**Why:** a deadband has a *permanent* standing error of up to ±0.5 s,
produces audible speed *steps* at threshold crossings, oscillates at
boundaries, and cannot cancel systematic clock skew — a 0.1 % audio-clock
offset manufactures 3.6 s/hour of drift that a P-only loop fights forever
(a P-only loop settles at `e* = ρ/Kp`, i.e. 12.5 ms here, which is small but
never goes away). The integrator drives steady-state error to zero. The D
term is excluded because position measurements carry RTT noise and
differentiation amplifies it.

### D21. Local receive anchor, no host wall clock

**Chosen:** `SYNC` carries media time only; followers anchor on their own
monotonic clock and compensate with `minRTT/2`.
**Rejected:** `host_wallclock_ms` in `SYNC`.
**Why:** two unsynchronized clocks with a few seconds of offset produce a
few seconds of sync error — fatal, and invisible until users complain.
Anchoring locally needs no clock synchronization at all; residual error is
RTT asymmetry (5–25 ms).

### D22. Free-roam as a sync state, not a membership state

**Chosen:** a roamer keeps downloading, verifying, uploading, and voting.
**Rejected:** treat a paused viewer as "left".
**Why:** someone grabbing food should not stop contributing bandwidth, and
should not lose their vote. Free-roam changes only whose playhead the
scheduler optimizes for.

---

## Authority

### D23. Certificates, not independent tallies

**Chosen:** a `VoteCertificate` over a frozen roster snapshot; every peer
verifies the same object.
**Rejected:** each peer tallying votes locally against "peers connected
now".
**Why:** a peer seeing 5 peers and a peer seeing 3 compute different
thresholds for the same votes, so honest peers produce conflicting results
from identical inputs. Freezing the roster in the proposal makes the
threshold identical everywhere; carrying the votes in one signed
certificate makes the outcome identical everywhere.

### D24. Takeover by challenge + majority ACK

**Chosen:** `TAKEOVER_CLAIM` → per-signer `CHALLENGE` → `TAKEOVER_ACK`s from
a strict majority → `HOST_LEASE` with a `TakeoverCertificate`.
**Rejected:** "lexicographically smallest complete peer takes over".
**Why:** peers disagree about who is complete, bitfields can be forged or
stale, and a partition can contain two "complete" peers. Deterministic
selection by peer ID is *not* authority and does not prevent split-brain. The
certificate costs a few round trips and actually prevents it.

### D25. Governance has no roles

**Chosen:** signed proposals, signed votes, certificates. The creator has no
privileges after genesis; the Host has two duties and no powers over people.
**Rejected:** moderator/veto models.
**Why:** the product rule is propose → vote → play. Any role that can
override a vote is a role that has to be trusted, and there is no server to
trust. A group that wants an iron-fisted host can simply not pass votes it
dislikes.

### D26. Cryptographic offenses skip the vote

**Chosen:** a bad block proof or a forged signature triggers an immediate
*local* ban.
**Rejected:** vote on every ban.
**Why:** math is not a judgment call. A bad block proof is objectively bad;
requiring a majority to act on it lets an attacker keep poisoning for the
duration of a vote. Votes are for judgment calls (kicks, what to watch).

### D27. Signed, expiring, usage-capped invites

**Chosen:** `InviteGrant` + `pw1_…` token; the invitee receives the current
room key over an encrypted `JOIN_ACCEPT`.
**Rejected:** one room-wide bearer token; passwords; PAKE.
**Why:** a room-wide token means any member can mint unlimited identities
with it and a kicked peer can rejoin forever. A password proves "you know a
secret", not "you are the same entity as yesterday". Signed grants make Sybil
cost real and make revocation possible.

### D28. Voice chat after the MVP

**Chosen:** M5, on the existing `RTCPeerConnection`s, capability-negotiated.
**Rejected:** in the MVP.
**Why:** data-channel-first keeps M1–M4 shippable. Media tracks slot into
existing connections without protocol changes, so deferring costs nothing
but a reserved message range.

---

# Part B — Corrections made during the v2 rewrite

Each entry is an error found while specifying v2. They are recorded because
several are subtle enough to be re-introduced by a plausible-looking
refactor.

## C1. "Every control message is signed, therefore impersonation is out"

**Wrong because:** a signature proves *authorship*, not *authority*. Any
member can sign a `SYNC`; only the lease holder's `SYNC` may move the room's
clock. Conflating the two makes every authority rule disappear.
**Fix:** separate authentication (Ed25519) from authorization (certificates
+ `term`), and make every receiver check the authority object
([security.md](security.md) §3, [protocol.md](../spec/protocol.md) §5).

## C2. "Smallest peer ID wins" host takeover

**Wrong because:** it is deterministic but not authoritative. Peers disagree
about completeness, bitfields lie, and a partition can have two "smallest
complete" peers. Determinism does not prevent split-brain when the input is
disputed.
**Fix:** D24.

## C3. Independent vote tallies

**Wrong because:** "strict majority of peers connected at tally time" makes
the threshold a function of local view, so honest peers legitimately
disagree.
**Fix:** D23.

## C4. `content_id = BLAKE3(manifest)` on a growing manifest

**Wrong because:** the identity changes on every append, invalidating
in-flight requests, storage keys, and bitfield references mid-stream.
**Fix:** D11.

## C5. `host_wallclock_ms` in `SYNC`

**Wrong because:** it mixes two unsynchronized clocks; a few seconds of
offset becomes a few seconds of desync.
**Fix:** D21.

## C6. Three-tier sync deadband

**Wrong because:** permanent standing error, audible speed steps, boundary
oscillation, and no way to cancel systematic skew.
**Fix:** D20.

## C7. `Q` as a fixed number, and unbounded app queues

**Wrong because:** `in_flight/RTT` caps throughput, so a fixed `Q` silently
caps fast paths; and a large app queue behind SCTP congestion control adds
latency without adding throughput.
**Fix:** D17, plus the `bufferedAmount` requirement in
[distribution.md](distribution.md) §4.3.

## C8. Piece-level hashing with "ban the sender"

**Wrong because:** a piece's blocks come from many peers, so the failure
cannot be attributed and the response punishes innocents.
**Fix:** D10.

## C9. 64 KiB blocks

**Wrong because:** a 64 KiB block plus a Merkle proof plus envelope metadata
does not fit a 64 KiB DataChannel message reliably.
**Fix:** D9.

## C10. A 1 MiB envelope cap and a "64 messages/s" limit

**Wrong because:** 64 × 1 MiB is 64 MB/s. Message-count limits without byte
budgets are not limits.
**Fix:** byte budgets per class ([protocol.md](../spec/protocol.md) §9).

## C11. Treating a `BlockProof` as transferable evidence

**Wrong because:** `{ bad_bytes, proof }` proves only that *some* bytes fail
the root — a property anyone can fabricate with arbitrary `bad_bytes`. It
identifies no peer.
**Fix:** the proof carries `sender_pubkey` + `sender_sig`; without a valid
signature it is a rumor and cannot contribute to a strike or ban
([protocol.md](../spec/protocol.md) §6.6).

## C12. Unbounded bitfield rebroadcast without a size guarantee

**Wrong because:** the assumption "Roaring is never larger than raw" is
false. An un-optimized dense chunk is an 8 KiB bitmap container, larger than
a 1.5 KB raw bitfield.
**Fix:** run-optimization as a normative sender requirement
([roaring-compression.md](../spec/roaring-compression.md) §3).

## C13. Rejecting a bitfield whose cardinality exceeds `total_pieces`

**Wrong because:** during a live transcode the Host's frontier legitimately
advances before a follower applies the append, so this strikes every honest
peer at the moment the feature is used.
**Fix:** an out-of-range bitfield is a manifest-version signal first; buffer
it, resync the manifest, then re-evaluate
([roaring-compression.md](../spec/roaring-compression.md) §6).

## C14. Rekey by "the remaining members generate a new key"

**Wrong because:** two members holding the same `KickCertificate` each
generate a *different* key for `epoch+1` and the room forks into two
incompatible epochs with the same number.
**Fix:** derive the next key from the previous key and the basis
([protocol.md](../spec/protocol.md) §4.4.1). Any member can publish it;
nobody chooses it.

## C15. Rekey as a one-shot push of wrapped keys

**Wrong because:** a member offline during the kick never receives its
wrapped key, cannot derive the new one, and cannot find the new relay
channel. It is permanently evicted with no error and no recovery.
**Fix:** derivation (C14) plus the epoch-independent lobby channel carrying
signed `LOBBY_BASIS` records
([protocol.md](../spec/protocol.md) §4.4.3, [connectivity.md](connectivity.md) §3.5).

## C16. "A kicked peer cannot rejoin"

**Wrong because:** with derived keys the kicked peer *can* derive the next
epoch key — it holds the previous one — and the lobby is public. The claim
was only true when the new key was freshly random, which C14/C15 replaced.
**Fix:** state plainly that the load-bearing barrier is the banlist plus
AUTH rejection, and that rotation is defence-in-depth against *passive*
capture ([protocol.md](../spec/protocol.md) §4.4.3,
[identity.md](identity.md) §3.3).

> **Open trade-off.** Cryptographic exclusion needs a fresh
> Diffie-Hellman among the remaining members, producing a key not derivable
> from the previous one. That is incompatible with offline catch-up: a peer
> that was not present cannot contribute to or learn the result. v2 chooses
> catch-up. A future revision that prioritises exclusion over offline
> recovery would have to drop the lobby and accept permanent eviction on
> rekey.

## C17. "The relay cannot correlate rooms to users"

**Wrong because:** it was both too strong and, in the other direction,
undermined by the format: `SignalEnvelope` carried `room: bytes .size 16` in
the *cleartext* header, so the relay could read the room identity directly.
**Fix:** `room_id` moves inside the encrypted plaintext; the header uses
`channel_hash` as its HKDF salt. The remaining claim is precise: no
cross-room linkage, no cross-epoch linkage, room *existence* and epoch
timing visible ([protocol.md](../spec/protocol.md) §11.2,
[security.md](security.md) §5 B).

## C18. Taking `signal_pub` from the relay's roster

**Wrong because:** the alias→`signal_pub` binding was relay-asserted, so a
malicious relay substitutes its own key, decrypts the SDP, and reads network
topology. AUTH still stops impersonation, so the hole was silent.
**Fix:** `signal_pub` lives in the host-signed `ROOM_STATE` roster; the
relay roster is a discovery hint only
([protocol.md](../spec/protocol.md) §7.2, §11.2).

## C19. AUTH's signature preimage specified twice

**Wrong because:** one section said the envelope `sig` is *replaced* by the
transcript signature, another said AUTH carries *both*.
**Fix:** AUTH carries both, and both must verify; `auth_sig` is the one that
proves key possession over the DTLS binding
([protocol.md](../spec/protocol.md) §1.3, §4.3).

## C20. `ManifestHead` without a `term`

**Wrong because:** §5.1 requires it to carry `(stream_id, term)`, but the
envelope has no `term` field and the head had no `term` either — so the one
message that bootstraps a stream could not be authority-checked.
**Fix:** `term` and `seq` are in the signed payload
([protocol.md](../spec/protocol.md) §6.3).

## C21. `Caps.profiles` as the codec gate

**Wrong because:** `FMP4` and `WEBM` are container profiles, not codecs.
Two peers can both support `FMP4` and disagree on H.264 profile/level, and
the protocol had no way to express that.
**Fix:** `Caps.codecs` carries exact codec strings, matched verbatim
([protocol.md](../spec/protocol.md) §2.1.1).

## C22. Write-then-verify in the storage path

**Wrong because:** it contradicted the `put_verified` contract and let an
attacker land arbitrary bytes on a victim's disk before any proof was
evaluated.
**Fix:** verify in the bounded assembly buffer, then write, then journal,
then advertise ([storage.md](storage.md) §1.3).

## C23. Piece-granular `PieceStore` with a block-granular data path

**Wrong because:** the scheduler requests, verifies, and hedges blocks; a
piece-granular store forces every platform adapter to reimplement piece
assembly, which is core logic.
**Fix:** block-granular trait with an explicit `PieceComplete` outcome that
is the only signal authorizing a bit to be advertised
([storage.md](storage.md) §3).

## C24. Fire-and-forget `AppendSegment`

**Wrong because:** MSE's `appendBuffer` is async with `updateend` and can
raise `QuotaExceededError`; an interface with no completion event forces
every adapter to invent its own queue and error path.
**Fix:** append tokens, `AppendDone`/`AppendRejected`, and a
`writable_bytes()` backpressure query ([frontends.md](frontends.md) §1.1,
[core-api.md](../spec/core-api.md) §4).

## C25. `P-only` skew error off by 100×

**Wrong because:** the stated figure was 2.5 s for a 0.1 % clock offset; the
correct value is `ρ/Kp` = 12.5 ms. The number mattered because it was used
to argue the integrator was unnecessary.
**Fix:** the integrator still matters (12.5 ms is permanent and systematic),
but for the right reason and at the right magnitude
([sync.md](sync.md) §3.3).

## C26. Critically-damped step response conflated with first-order

**Wrong because:** the 50 % settling time of a critically damped
second-order system is `2x/Kp` with `x = 1.678`, i.e. ~42 s here, not the
first-order `1/Kp`-based ~17 s. The error envelope constant is `2/Kp`, not
`1/Kp`.
**Fix:** [sync.md](sync.md) §3.2 and [performance.md](performance.md) §8.

## C27. Unauthenticated, un-rate-limited `CHALLENGE`

**Wrong because:** `CHALLENGE` is unsigned, so anyone could challenge a
takeover candidate for a block it legitimately does not hold and burn the 5 s
response window, failing a valid takeover.
**Fix:** challenges must be in-region; each `TAKEOVER_ACK` signer honours
only its own challenge nonce; rate and budget limits
([protocol.md](../spec/protocol.md) §5.3).

## C28. `MANIFEST_GET` served by everyone

**Wrong because:** one broadcast request in a 16-peer mesh yields 15
identical 32 KiB responses — 480 KiB of upload for a 32 KiB need, and a free
amplification vector.
**Fix:** designated responder, with an explicit `responder` field
([protocol.md](../spec/protocol.md) §7.3).

## C29. `REQUEST` entry limit vs outstanding-block limit

**Wrong because:** 64 entries × up to 16 blocks = 1024 blocks, against a
`Q ≤ 64` outstanding limit. The two limits were specified independently and
did not compose.
**Fix:** the block-mask total of one `REQUEST` must be ≤ `Q`
([protocol.md](../spec/protocol.md) §7.4).

## C30. Banlist omission as "an attack the room can detect"

**Wrong because:** no mechanism was specified for detecting it. The claim
was unactionable.
**Fix:** `banlist_complete` + `banlist_root`, with a required two-peer
cross-check and an objective contradiction check
([governance.md](governance.md) §4.1.1).

## C31. Position reported by four different messages

**Wrong because:** `SYNC`, `PRESENCE`, `READY`, and `STALL_NOTICE` all
carry media position, three of them signed, with overlapping semantics and
no single source of truth for "where is this peer".
**Fix:** `SYNC` (host) and `STALL_NOTICE` (local) keep position;
`PRESENCE` becomes an unsigned status-only heartbeat and `READY` folds into
it ([protocol.md](../spec/protocol.md) §7.2, §7.5).

## C32. Prose where a state machine was needed

**Wrong because:** the core is described as "pure state machines" and had
exactly one state diagram in five thousand lines, for the least central
machine.
**Fix:** peer-connection, room, stream, store, and governance diagrams in
[architecture.md](architecture.md) §5.2.
