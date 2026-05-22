package scheduler

import (
	"sort"

	"github.com/sophic00/peerwatch.git/internal/peer"
)

// maxInFlightPerPeer is the maximum number of concurrent chunk requests
// allowed per peer. Beyond this, the peer is considered saturated.
const maxInFlightPerPeer = 4

// PlaybackWindow is the number of sequential chunks ahead of the cursor
// that are prioritized for playback continuity.
const PlaybackWindow = 30

// chunkCandidate holds a chunk index and its priority score.
// Lower score = higher priority.
type chunkCandidate struct {
	Index    int
	Score    float64
	PeerIDs [][16]byte // peers that have this chunk
}

// PrioritizeChunks returns chunk indices sorted by download priority.
//
// The priority scheme is:
//  1. Urgent demand (chunks the HTTP server/player needs RIGHT NOW)
//  2. Playback window: chunks [cursor, cursor+PlaybackWindow) in sequential order
//  3. Rarest-first: among remaining missing chunks, prefer those held by fewer peers
//
// Chunks already in the store or already in-flight are excluded.
func PrioritizeChunks(
	missing []int,
	cursor int,
	tracker *peer.Tracker,
	inFlight map[int]struct{},
	urgent map[int]struct{},
) []chunkCandidate {
	candidates := make([]chunkCandidate, 0, len(missing))

	for _, idx := range missing {
		// Skip chunks already in-flight
		if _, ok := inFlight[idx]; ok {
			continue
		}

		peerIDs := tracker.PeerIDsWithChunk(idx)
		// Skip chunks that no peer has (can't download them)
		if len(peerIDs) == 0 {
			continue
		}

		score := scoreChunk(idx, cursor, tracker.Rarity(idx), urgent)
		candidates = append(candidates, chunkCandidate{
			Index:   idx,
			Score:   score,
			PeerIDs: peerIDs,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score < candidates[j].Score
	})

	return candidates
}

// scoreChunk computes a priority score for a chunk.
// Lower score = higher priority.
//
// Score tiers:
//   - Urgent demand: 0.0 (absolute highest priority)
//   - Playback window: 100.0 + position_in_window (sequential near cursor)
//   - Rarest-first:    1000.0 + rarity (fewer holders = lower score)
func scoreChunk(index, cursor, rarity int, urgent map[int]struct{}) float64 {
	// Tier 0: urgent demand from HTTP server
	if _, ok := urgent[index]; ok {
		return 0.0
	}

	// Tier 1: playback window [cursor, cursor+PlaybackWindow)
	if index >= cursor && index < cursor+PlaybackWindow {
		// Sequential position within the window (0.0 for cursor chunk)
		return 100.0 + float64(index-cursor)
	}

	// Tier 2: rarest-first for all other chunks
	// Lower rarity = fewer peers have it = more urgent to grab
	return 1000.0 + float64(rarity)
}

// peerScore computes a score for how good a peer is for downloading.
// Higher score = better peer to download from.
//
// score = speed / (1 + inFlightCount)
//
// This naturally balances load: fast peers get more requests, but each
// additional in-flight request reduces the peer's attractiveness.
func peerScore(p *peer.Peer) float64 {
	speed := p.Speed()
	if speed <= 0 {
		speed = 100_000 // default 100KB/s for peers with no speed data
	}
	return speed / float64(1+p.InFlightCount())
}

// SelectPeer picks the best peer to download a chunk from.
// Returns nil if no suitable peer is available (all saturated or disconnected).
func SelectPeer(peerIDs [][16]byte, swarm peerLookup) *peer.Peer {
	var best *peer.Peer
	var bestScore float64

	for _, id := range peerIDs {
		p := swarm.GetPeer(id)
		if p == nil {
			continue // peer disconnected
		}

		// Skip saturated peers
		if p.InFlightCount() >= maxInFlightPerPeer {
			continue
		}

		score := peerScore(p)
		if best == nil || score > bestScore {
			best = p
			bestScore = score
		}
	}

	return best
}

// peerLookup is an interface for looking up peers by ID.
// Satisfied by *peer.Swarm.
type peerLookup interface {
	GetPeer(id [16]byte) *peer.Peer
}
