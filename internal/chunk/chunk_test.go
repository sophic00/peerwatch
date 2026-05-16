package chunk

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

func TestChunkerBasic(t *testing.T) {
	// Create a temp file with known content
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bin")

	// 1500 bytes with chunk size 512 = 3 chunks (512 + 512 + 476)
	data := make([]byte, 1500)
	for i := range data {
		data[i] = byte(i % 251) // prime modulus for variety
	}
	os.WriteFile(path, data, 0644)

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	c, err := NewChunker(file, 512)
	if err != nil {
		t.Fatal(err)
	}

	// Check chunk count
	if got := c.ChunkCount(); got != 3 {
		t.Errorf("ChunkCount: got %d, want 3", got)
	}

	// Check file size
	if got := c.FileSize(); got != 1500 {
		t.Errorf("FileSize: got %d, want 1500", got)
	}

	// Read each chunk and verify sizes
	chunk0, err := c.ReadChunk(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunk0) != 512 {
		t.Errorf("chunk 0 size: got %d, want 512", len(chunk0))
	}

	chunk1, err := c.ReadChunk(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunk1) != 512 {
		t.Errorf("chunk 1 size: got %d, want 512", len(chunk1))
	}

	chunk2, err := c.ReadChunk(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunk2) != 476 {
		t.Errorf("chunk 2 (last) size: got %d, want 476", len(chunk2))
	}

	// Verify content matches original data
	reassembled := append(append(chunk0, chunk1...), chunk2...)
	for i := range data {
		if reassembled[i] != data[i] {
			t.Fatalf("data mismatch at byte %d: got %d, want %d", i, reassembled[i], data[i])
		}
	}
}

func TestChunkerExactMultiple(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exact.bin")

	// Exactly 1024 bytes with chunk size 512 = 2 chunks
	data := make([]byte, 1024)
	os.WriteFile(path, data, 0644)

	file, _ := os.Open(path)
	defer file.Close()

	c, _ := NewChunker(file, 512)
	if got := c.ChunkCount(); got != 2 {
		t.Errorf("ChunkCount: got %d, want 2", got)
	}

	// Both chunks should be exactly 512 bytes
	for i := 0; i < 2; i++ {
		chunk, _ := c.ReadChunk(i)
		if len(chunk) != 512 {
			t.Errorf("chunk %d size: got %d, want 512", i, len(chunk))
		}
	}
}

func TestChunkerOutOfRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.bin")
	os.WriteFile(path, []byte("hello"), 0644)

	file, _ := os.Open(path)
	defer file.Close()

	c, _ := NewChunker(file, 512)

	if _, err := c.ReadChunk(-1); err == nil {
		t.Error("expected error for negative index")
	}
	if _, err := c.ReadChunk(1); err == nil {
		t.Error("expected error for index >= ChunkCount")
	}
}

func TestChunkerEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.bin")
	os.WriteFile(path, []byte{}, 0644)

	file, _ := os.Open(path)
	defer file.Close()

	_, err := NewChunker(file, 512)
	if err == nil {
		t.Error("expected error for empty file")
	}
}

func TestBuildManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "video.mp4")

	data := make([]byte, 1500)
	for i := range data {
		data[i] = byte(i % 251)
	}
	os.WriteFile(path, data, 0644)

	manifest, err := BuildManifest(path, 512)
	if err != nil {
		t.Fatal(err)
	}

	if manifest.FileName != "video.mp4" {
		t.Errorf("FileName: got %q, want %q", manifest.FileName, "video.mp4")
	}
	if manifest.FileSize != 1500 {
		t.Errorf("FileSize: got %d, want 1500", manifest.FileSize)
	}
	if manifest.ChunkCount != 3 {
		t.Errorf("ChunkCount: got %d, want 3", manifest.ChunkCount)
	}

	// Verify hashes are correct
	expectedHash0 := sha256.Sum256(data[:512])
	if manifest.Chunks[0].Hash != expectedHash0 {
		t.Error("chunk 0 hash mismatch")
	}

	expectedHash2 := sha256.Sum256(data[1024:])
	if manifest.Chunks[2].Hash != expectedHash2 {
		t.Error("chunk 2 (last) hash mismatch")
	}
}
