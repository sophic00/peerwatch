package peer

import (
	"sync"
)

// Tracker aggregates chunk availability across all peers in the swarm.
// Used by the scheduler to find which peers have a given chunk
// and to determine rarity (for rarest-first scheduling).
type Tracker struct {
	mu         sync.RWMutex
	chunkCount int
	// availability[chunkIndex] = set of peer IDs that have this chunk
	availability []map[[16]byte]struct{}
}

// NewTracker creates a tracker for the given number of chunks.
func NewTracker(chunkCount int) *Tracker {
	avail := make([]map[[16]byte]struct{}, chunkCount)
	for i := range avail {
		avail[i] = make(map[[16]byte]struct{})
	}
	return &Tracker{
		chunkCount:   chunkCount,
		availability: avail,
	}
}

// UpdateFromBitfield updates a peer's chunk availability from their bitfield.
// Replaces any previous state for this peer.
func (t *Tracker) UpdateFromBitfield(peerID [16]byte, bitfield []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for i := 0; i < t.chunkCount; i++ {
		byteIdx := i / 8
		if byteIdx >= len(bitfield) {
			break
		}
		has := bitfield[byteIdx]&(1<<(7-uint(i%8))) != 0
		if has {
			t.availability[i][peerID] = struct{}{}
		} else {
			delete(t.availability[i], peerID)
		}
	}
}

// RemovePeer removes all availability data for a disconnected peer.
func (t *Tracker) RemovePeer(peerID [16]byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range t.availability {
		delete(t.availability[i], peerID)
	}
}

// Rarity returns how many peers have a given chunk.
// Lower = rarer = higher priority for rarest-first scheduling.
func (t *Tracker) Rarity(chunkIndex int) int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if chunkIndex < 0 || chunkIndex >= t.chunkCount {
		return 0
	}
	return len(t.availability[chunkIndex])
}

// PeerIDsWithChunk returns the IDs of peers that have a specific chunk.
func (t *Tracker) PeerIDsWithChunk(chunkIndex int) [][16]byte {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if chunkIndex < 0 || chunkIndex >= t.chunkCount {
		return nil
	}
	ids := make([][16]byte, 0, len(t.availability[chunkIndex]))
	for id := range t.availability[chunkIndex] {
		ids = append(ids, id)
	}
	return ids
}
