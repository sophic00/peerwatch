package chunk

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Store manages chunk storage and tracks which chunks are available locally.
//
// For the host, it wraps the original video file (all chunks available).
// For peers, it creates a sparse file and fills it as chunks arrive.
type Store struct {
	mu       sync.RWMutex
	file     *os.File
	manifest *Manifest
	bitfield []byte
	count    int  // number of chunks we have
	isHost   bool

	// Signaling: waiters block until a specific chunk arrives.
	waitMu  sync.Mutex
	waiters map[int]chan struct{}
}

// NewHostStore creates a store for the host, backed by the original video file.
// All chunks are marked as available.
func NewHostStore(filePath string, manifest *Manifest) (*Store, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}

	bf := makeBitfield(manifest.ChunkCount)
	// Set all bits to 1 — host has everything
	for i := 0; i < manifest.ChunkCount; i++ {
		setBit(bf, i)
	}

	return &Store{
		file:     file,
		manifest: manifest,
		bitfield: bf,
		count:    manifest.ChunkCount,
		isHost:   true,
		waiters:  make(map[int]chan struct{}),
	}, nil
}

// NewPeerStore creates a store for a joining peer.
// It creates a sparse file of the video's full size at the given directory.
// All chunks are initially marked as missing.
func NewPeerStore(manifest *Manifest, dir string) (*Store, error) {
	filePath := filepath.Join(dir, manifest.FileName+".partial")

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("create sparse file: %w", err)
	}

	// Truncate to full size — on Linux this creates a sparse file
	// (no actual disk blocks allocated until data is written)
	if err := file.Truncate(manifest.FileSize); err != nil {
		file.Close()
		return nil, fmt.Errorf("truncate sparse file: %w", err)
	}

	return &Store{
		file:     file,
		manifest: manifest,
		bitfield: makeBitfield(manifest.ChunkCount),
		count:    0,
		isHost:   false,
		waiters:  make(map[int]chan struct{}),
	}, nil
}

// WriteChunk verifies and writes a chunk to the store.
// Returns an error if the SHA-256 hash doesn't match the manifest.
func (s *Store) WriteChunk(index int, data []byte) error {
	if index < 0 || index >= s.manifest.ChunkCount {
		return fmt.Errorf("chunk index %d out of range [0, %d)", index, s.manifest.ChunkCount)
	}

	if s.HasChunk(index) {
		return nil // already have it, skip
	}

	// Verify hash
	hash := sha256.Sum256(data)
	if hash != s.manifest.Chunks[index].Hash {
		return fmt.Errorf("chunk %d hash mismatch", index)
	}

	// Write to correct offset
	offset := int64(index) * s.manifest.ChunkSize
	if _, err := s.file.WriteAt(data, offset); err != nil {
		return fmt.Errorf("write chunk %d: %w", index, err)
	}

	// Update bitfield
	s.mu.Lock()
	setBit(s.bitfield, index)
	s.count++
	s.mu.Unlock()

	// Signal any goroutines waiting for this chunk
	s.waitMu.Lock()
	if ch, ok := s.waiters[index]; ok {
		close(ch)
		delete(s.waiters, index)
	}
	s.waitMu.Unlock()

	return nil
}

// ReadChunk reads a chunk from the store by index.
// The caller should ensure the chunk is available (HasChunk) before calling.
func (s *Store) ReadChunk(index int) ([]byte, error) {
	if index < 0 || index >= s.manifest.ChunkCount {
		return nil, fmt.Errorf("chunk index %d out of range [0, %d)", index, s.manifest.ChunkCount)
	}

	size := s.manifest.Chunks[index].Size
	offset := int64(index) * s.manifest.ChunkSize

	buf := make([]byte, size)
	n, err := s.file.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("read chunk %d: %w", index, err)
	}
	return buf[:n], nil
}

// ReadBytes reads an arbitrary byte range from the store.
// Blocks until all chunks covering the requested range are available.
// Used by the local HTTP server to serve Range requests to mpv.
func (s *Store) ReadBytes(offset, length int64) ([]byte, error) {
	if offset < 0 || offset >= s.manifest.FileSize {
		return nil, fmt.Errorf("offset %d out of range [0, %d)", offset, s.manifest.FileSize)
	}

	// Clamp to file size
	if offset+length > s.manifest.FileSize {
		length = s.manifest.FileSize - offset
	}

	// Determine which chunks cover this range
	startChunk := int(offset / s.manifest.ChunkSize)
	endChunk := int((offset + length - 1) / s.manifest.ChunkSize)

	// Wait for all required chunks
	for i := startChunk; i <= endChunk; i++ {
		s.WaitForChunk(i)
	}

	// Read from file
	buf := make([]byte, length)
	n, err := s.file.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("read bytes at %d: %w", offset, err)
	}
	return buf[:n], nil
}

// HasChunk returns true if the chunk at index is available locally.
func (s *Store) HasChunk(index int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return hasBit(s.bitfield, index)
}

// Bitfield returns a copy of the current bitfield.
func (s *Store) Bitfield() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bf := make([]byte, len(s.bitfield))
	copy(bf, s.bitfield)
	return bf
}

// Count returns the number of chunks available locally.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.count
}

// IsComplete returns true if all chunks are available.
func (s *Store) IsComplete() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.count == s.manifest.ChunkCount
}

// MissingChunks returns indices of all chunks we don't have.
func (s *Store) MissingChunks() []int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var missing []int
	for i := 0; i < s.manifest.ChunkCount; i++ {
		if !hasBit(s.bitfield, i) {
			missing = append(missing, i)
		}
	}
	return missing
}

// WaitForChunk blocks until the specified chunk is available.
func (s *Store) WaitForChunk(index int) {
	if s.HasChunk(index) {
		return
	}

	s.waitMu.Lock()
	// Re-verify under waitMu lock (reading the bitfield safely under mu.RLock)
	s.mu.RLock()
	has := hasBit(s.bitfield, index)
	s.mu.RUnlock()
	if has {
		s.waitMu.Unlock()
		return
	}

	ch, ok := s.waiters[index]
	if !ok {
		ch = make(chan struct{})
		s.waiters[index] = ch
	}
	s.waitMu.Unlock()

	<-ch
}

// Manifest returns the manifest associated with this store.
func (s *Store) Manifest() *Manifest {
	return s.manifest
}

// Close closes the underlying file.
func (s *Store) Close() error {
	return s.file.Close()
}

// FilePath returns the path to the underlying file.
func (s *Store) FilePath() string {
	return s.file.Name()
}

// --- Bitfield helpers ---
// Big-endian bit order: bit 0 of byte 0 is the most significant bit.

func makeBitfield(chunkCount int) []byte {
	return make([]byte, (chunkCount+7)/8)
}

func setBit(bf []byte, index int) {
	bf[index/8] |= 1 << (7 - uint(index%8))
}

func hasBit(bf []byte, index int) bool {
	return bf[index/8]&(1<<(7-uint(index%8))) != 0
}
