package chunk

import (
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// Manifest is the table of contents for a shared video file.
// It contains per-chunk metadata (size and SHA-256 hash) that peers
// use to verify chunk integrity after download.
type Manifest struct {
	FileName   string
	FileSize   int64
	ChunkSize  int64
	ChunkCount int
	Chunks     []ChunkMeta
}

// ChunkMeta holds metadata for a single chunk.
type ChunkMeta struct {
	Size int64
	Hash [32]byte // SHA-256 digest
}

// BuildManifest reads the file at filePath, splits it into chunks of chunkSize
// bytes, and computes a SHA-256 hash for each chunk.
//
// This is called once by the host at startup. Progress is logged to stdout.
func BuildManifest(filePath string, chunkSize int64) (*Manifest, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	chunker, err := NewChunker(file, chunkSize)
	if err != nil {
		return nil, fmt.Errorf("create chunker: %w", err)
	}

	count := chunker.ChunkCount()
	m := &Manifest{
		FileName:   filepath.Base(filePath),
		FileSize:   chunker.FileSize(),
		ChunkSize:  chunkSize,
		ChunkCount: count,
		Chunks:     make([]ChunkMeta, count),
	}

	start := time.Now()
	for i := range count {
		data, err := chunker.ReadChunk(i)
		if err != nil {
			return nil, fmt.Errorf("read chunk %d: %w", i, err)
		}

		m.Chunks[i] = ChunkMeta{
			Size: int64(len(data)),
			Hash: sha256.Sum256(data),
		}

		// Log progress every 500 chunks
		if (i+1)%500 == 0 || i == count-1 {
			elapsed := time.Since(start)
			log.Printf("  hashing: %d/%d chunks (%.1f%%) [%v]",
				i+1, count, float64(i+1)/float64(count)*100, elapsed.Round(time.Millisecond))
		}
	}

	return m, nil
}
