// Package chunk handles splitting files into fixed-size chunks,
// computing manifests, and managing chunk storage.
package chunk

import (
	"fmt"
	"io"
	"os"
)

// DefaultChunkSize is 512 KB — the default size for each chunk.
const DefaultChunkSize int64 = 524288

// Chunker reads fixed-size chunks from a file.
type Chunker struct {
	file      *os.File
	fileSize  int64
	chunkSize int64
}

// NewChunker creates a Chunker for the given file.
// The file must be opened for reading. The caller retains ownership
// of the file handle and is responsible for closing it.
func NewChunker(file *os.File, chunkSize int64) (*Chunker, error) {
	if chunkSize <= 0 {
		return nil, fmt.Errorf("chunk size must be positive, got %d", chunkSize)
	}

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}

	if info.Size() == 0 {
		return nil, fmt.Errorf("file is empty")
	}

	return &Chunker{
		file:      file,
		fileSize:  info.Size(),
		chunkSize: chunkSize,
	}, nil
}

// ChunkCount returns the total number of chunks.
// The last chunk may be smaller than chunkSize.
func (c *Chunker) ChunkCount() int {
	return int((c.fileSize + c.chunkSize - 1) / c.chunkSize)
}

// ChunkSize returns the size of a specific chunk.
// The last chunk may be smaller than the configured chunk size.
func (c *Chunker) ChunkSizeAt(index int) (int64, error) {
	if index < 0 || index >= c.ChunkCount() {
		return 0, fmt.Errorf("chunk index %d out of range [0, %d)", index, c.ChunkCount())
	}

	offset := int64(index) * c.chunkSize
	size := c.chunkSize
	if offset+size > c.fileSize {
		size = c.fileSize - offset
	}
	return size, nil
}

// ReadChunk reads the chunk at the given index.
// Returns the chunk data (which may be shorter than chunkSize for the last chunk).
func (c *Chunker) ReadChunk(index int) ([]byte, error) {
	size, err := c.ChunkSizeAt(index)
	if err != nil {
		return nil, err
	}

	offset := int64(index) * c.ConfiguredChunkSize()
	buf := make([]byte, size)
	n, err := c.file.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("read chunk %d at offset %d: %w", index, offset, err)
	}
	return buf[:n], nil
}

// FileSize returns the total file size in bytes.
func (c *Chunker) FileSize() int64 {
	return c.fileSize
}

// ConfiguredChunkSize returns the chunk size this chunker was configured with.
func (c *Chunker) ConfiguredChunkSize() int64 {
	return c.chunkSize
}
