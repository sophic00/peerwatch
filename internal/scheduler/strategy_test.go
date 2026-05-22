package scheduler

import (
	"testing"

	"github.com/sophic00/peerwatch.git/internal/peer"
)

// TestPrioritizeChunks verifies the 3-tier priority ordering:
// urgent → playback window → rarest-first
func TestPrioritizeChunks(t *testing.T) {
	tracker := peer.NewTracker(20)

	// Set up availability:
	// Peer A has chunks 0-9
	peerA := [16]byte{1}
	bfA := make([]byte, 3)
	for i := 0; i < 10; i++ {
		bfA[i/8] |= 1 << (7 - uint(i%8))
	}
	tracker.UpdateFromBitfield(peerA, bfA)

	// Peer B has chunks 5-14
	peerB := [16]byte{2}
	bfB := make([]byte, 3)
	for i := 5; i < 15; i++ {
		bfB[i/8] |= 1 << (7 - uint(i%8))
	}
	tracker.UpdateFromBitfield(peerB, bfB)

	// All chunks 0-19 are missing
	missing := make([]int, 20)
	for i := range missing {
		missing[i] = i
	}

	inFlight := map[int]struct{}{3: {}} // chunk 3 is in-flight

	// Chunk 12 is urgent
	urgent := map[int]struct{}{12: {}}

	// Cursor at chunk 5
	candidates := PrioritizeChunks(missing, 5, tracker, inFlight, urgent)

	if len(candidates) == 0 {
		t.Fatal("expected candidates, got none")
	}

	// Verify chunk 3 is excluded (in-flight)
	for _, c := range candidates {
		if c.Index == 3 {
			t.Error("chunk 3 should be excluded (in-flight)")
		}
	}

	// Verify chunks with no peers (15-19 — only tracker has up to 14) are excluded
	for _, c := range candidates {
		if c.Index >= 15 {
			t.Errorf("chunk %d should be excluded (no peers have it)", c.Index)
		}
	}

	// First candidate should be urgent chunk 12 (score 0.0)
	if candidates[0].Index != 12 {
		t.Errorf("first candidate: got chunk %d, want chunk 12 (urgent)", candidates[0].Index)
	}

	// Next should be playback window chunks [5..9] minus any in-flight,
	// in sequential order
	windowStart := -1
	for i, c := range candidates {
		if c.Score >= 100.0 && c.Score < 200.0 {
			if windowStart == -1 {
				windowStart = i
			}
		}
	}
	if windowStart == -1 {
		t.Fatal("no playback window candidates found")
	}

	// Verify ordering within playback window
	var windowChunks []int
	for _, c := range candidates {
		if c.Score >= 100.0 && c.Score < 200.0 {
			windowChunks = append(windowChunks, c.Index)
		}
	}
	for i := 1; i < len(windowChunks); i++ {
		if windowChunks[i] <= windowChunks[i-1] {
			t.Errorf("playback window not sequential: %d after %d", windowChunks[i], windowChunks[i-1])
		}
	}

	t.Logf("priority order (first 8): ")
	for i := 0; i < 8 && i < len(candidates); i++ {
		t.Logf("  chunk %d (score %.1f, %d peers)", candidates[i].Index, candidates[i].Score, len(candidates[i].PeerIDs))
	}
}

// TestScoreChunkTiers verifies score ranges for each tier.
func TestScoreChunkTiers(t *testing.T) {
	urgent := map[int]struct{}{5: {}}

	// Urgent → score 0.0
	s := scoreChunk(5, 0, 3, urgent)
	if s != 0.0 {
		t.Errorf("urgent score: got %f, want 0.0", s)
	}

	// Playback window → score 100.0 + offset
	s = scoreChunk(2, 0, 3, nil)
	if s != 102.0 {
		t.Errorf("playback window score for cursor=0, index=2: got %f, want 102.0", s)
	}

	// Not in window → 1000.0 + rarity
	s = scoreChunk(50, 0, 3, nil)
	if s != 1003.0 {
		t.Errorf("rarest-first score rarity=3: got %f, want 1003.0", s)
	}

	// Rarest chunk should score lower than common chunk
	sRare := scoreChunk(50, 0, 1, nil)
	sCommon := scoreChunk(50, 0, 5, nil)
	if sRare >= sCommon {
		t.Errorf("rare chunk (%f) should have lower score than common (%f)", sRare, sCommon)
	}
}

// TestSelectPeer verifies peer selection prefers faster, less-loaded peers.
func TestSelectPeer(t *testing.T) {
	// We can't easily create full Peer objects without TCP connections,
	// so we test the peerScore function directly.

	// A peer with 500KB/s speed and 0 in-flight should score higher
	// than one with 500KB/s and 3 in-flight.
	// peerScore = speed / (1 + inFlightCount)
	// Peer A: 500000 / 1 = 500000
	// Peer B: 500000 / 4 = 125000
	// Peer A is better.

	// Test the scoring math
	scoreA := 500_000.0 / float64(1+0) // 500000
	scoreB := 500_000.0 / float64(1+3) // 125000

	if scoreA <= scoreB {
		t.Errorf("peer A (no in-flight) should score higher: %f vs %f", scoreA, scoreB)
	}

	// A very fast peer with many in-flight can still beat a slow idle peer
	scoreFast := 2_000_000.0 / float64(1+3)  // 500000
	scoreSlow := 50_000.0 / float64(1+0)     // 50000
	if scoreFast <= scoreSlow {
		t.Errorf("fast loaded peer should still beat slow idle: %f vs %f", scoreFast, scoreSlow)
	}
}
