package chunk
import (
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
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

type hashResult struct {
	index int
	size  int64
	hash  [32]byte
	err   error
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

	numWorkers := max(min(runtime.NumCPU(), count), 1)
	jobs := make(chan int, numWorkers*2)
	results := make(chan hashResult, numWorkers*2)
	var wg sync.WaitGroup
	for range numWorkers {
		wg.Go(func() {
			buf := make([]byte, chunkSize)
			for idx := range jobs {
				n, err := chunker.ReadChunk(idx, buf)
				if err != nil {
					results <- hashResult{index: idx, err: err}
					continue
				}

				results <- hashResult{
					index: idx,
					size:  int64(n),
					hash:  sha256.Sum256(buf[:n]),
				}
			}
		})
	}
	// Feed jobs in background
	go func() {
		for i := range count {
			jobs <- i
		}
		close(jobs)
	}()

	// Wait in background and close results once all workers finish (draining safety)
	go func() {
		wg.Wait()
		close(results)
	}()

	start := time.Now()
	var firstErr error

	// Collect exactly 'count' results to guarantee no workers block on channel write
	for i := range count {
		res := <-results
		if res.err != nil && firstErr == nil {
			firstErr = res.err
		}

		if firstErr == nil {
			m.Chunks[res.index] = ChunkMeta{
				Size: res.size,
				Hash: res.hash,
			}
		}

		// Log progress
		if (i+1)%500 == 0 || i == count-1 {
			elapsed := time.Since(start)
			log.Printf("  hashing: %d/%d chunks (%.1f%%) [%v]",
				i+1, count, float64(i+1)/float64(count)*100, elapsed.Round(time.Millisecond))
		}
	}

	if firstErr != nil {
		return nil, fmt.Errorf("hashing failed: %w", firstErr)
	}

	return m, nil
}
