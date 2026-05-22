// Package scheduler orchestrates chunk downloads across the peer swarm.
//
// It runs a continuous loop that:
//  1. Checks for urgent demand from the HTTP server (player needs data NOW)
//  2. Fills the playback window sequentially
//  3. Uses rarest-first for remaining capacity
//  4. Selects the best peer for each chunk based on speed and load
//  5. Sends batch REQUEST messages and tracks in-flight state
package scheduler

import (
	"log"
	"sync"
	"time"

	"github.com/sophic00/peerwatch.git/internal/chunk"
	"github.com/sophic00/peerwatch.git/internal/peer"
)

// maxTotalInFlight is the maximum number of chunk requests in-flight globally.
const maxTotalInFlight = 16

// Scheduler manages the chunk download loop.
type Scheduler struct {
	mu       sync.Mutex
	store    *chunk.Store
	swarm    *peer.Swarm
	cursor   int // playback cursor (chunk index)
	inFlight map[int]struct{}

	// Urgent demand: chunks the HTTP server needs immediately.
	// Pushed by the player layer, consumed by the scheduler loop.
	urgentMu sync.Mutex
	urgent   map[int]struct{}

	done chan struct{}
}

// New creates a new scheduler.
func New(store *chunk.Store, swarm *peer.Swarm) *Scheduler {
	return &Scheduler{
		store:    store,
		swarm:    swarm,
		inFlight: make(map[int]struct{}),
		urgent:   make(map[int]struct{}),
		done:     make(chan struct{}),
	}
}

// SetCursor updates the playback cursor position.
// Called by the player/sync layer as playback progresses.
func (s *Scheduler) SetCursor(chunkIndex int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cursor = chunkIndex
}

// Demand marks a chunk as urgently needed (e.g., HTTP server is blocking on it).
// The scheduler will prioritize this chunk above all others.
func (s *Scheduler) Demand(chunkIndex int) {
	if s.store.HasChunk(chunkIndex) {
		return // already have it
	}
	s.urgentMu.Lock()
	s.urgent[chunkIndex] = struct{}{}
	s.urgentMu.Unlock()
}

// OnPieceReceived should be called when a chunk has been received and written
// to the store. It removes the chunk from in-flight and urgent sets.
func (s *Scheduler) OnPieceReceived(index uint32) {
	idx := int(index)
	s.mu.Lock()
	delete(s.inFlight, idx)
	s.mu.Unlock()

	s.urgentMu.Lock()
	delete(s.urgent, idx)
	s.urgentMu.Unlock()
}

// Run starts the scheduler loop. Blocks until Stop is called or the
// download completes.
//
// The caller should run this in a goroutine:
//
//	go scheduler.Run()
func (s *Scheduler) Run() {
	// Use a short ticker for responsive scheduling.
	// Each tick: assess state, pick chunks, assign to peers, send requests.
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if s.store.IsComplete() {
				log.Printf("scheduler: download complete (%d chunks)", s.store.Manifest().ChunkCount)
				return
			}
			s.tick()
		case <-s.done:
			return
		}
	}
}

// Stop signals the scheduler to exit.
func (s *Scheduler) Stop() {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
}

// InFlightCount returns the number of chunks currently in-flight.
func (s *Scheduler) InFlightCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.inFlight)
}

// tick runs one scheduling cycle.
func (s *Scheduler) tick() {
	s.mu.Lock()
	cursor := s.cursor
	inFlightSnap := make(map[int]struct{}, len(s.inFlight))
	for k := range s.inFlight {
		inFlightSnap[k] = struct{}{}
	}
	s.mu.Unlock()

	// How many more requests can we send?
	budget := maxTotalInFlight - len(inFlightSnap)
	if budget <= 0 {
		return
	}

	// Snapshot urgent set
	s.urgentMu.Lock()
	urgentSnap := make(map[int]struct{}, len(s.urgent))
	for k := range s.urgent {
		urgentSnap[k] = struct{}{}
	}
	s.urgentMu.Unlock()

	// Get missing chunks and prioritize
	missing := s.store.MissingChunks()
	if len(missing) == 0 {
		return
	}

	tracker := s.swarm.Tracker()
	if tracker == nil {
		return
	}

	candidates := PrioritizeChunks(missing, cursor, tracker, inFlightSnap, urgentSnap)
	if len(candidates) == 0 {
		return
	}

	// Assign chunks to peers, batching by peer for efficiency.
	// peerBatches maps peer ID → list of chunk indices to request.
	peerBatches := make(map[[16]byte][]uint32)

	for _, c := range candidates {
		if budget <= 0 {
			break
		}

		p := SelectPeer(c.PeerIDs, s.swarm)
		if p == nil {
			continue // no available peer for this chunk
		}

		peerBatches[p.ID] = append(peerBatches[p.ID], uint32(c.Index))

		s.mu.Lock()
		s.inFlight[c.Index] = struct{}{}
		s.mu.Unlock()

		budget--
	}

	// Send batch requests
	for peerID, indices := range peerBatches {
		s.swarm.RequestChunks(peerID, indices)
	}
}
