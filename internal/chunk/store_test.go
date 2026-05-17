package chunk

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestHostStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "video.mp4")
	data := makeTestData(1500)
	os.WriteFile(path, data, 0644)

	manifest, _ := BuildManifest(path, 512)
	store, err := NewHostStore(path, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if !store.IsComplete() {
		t.Error("host store should be complete")
	}
	if got := store.Count(); got != 3 {
		t.Errorf("Count: got %d, want 3", got)
	}
	for i := 0; i < 3; i++ {
		if !store.HasChunk(i) {
			t.Errorf("HasChunk(%d) = false", i)
		}
	}

	chunk0, _ := store.ReadChunk(0)
	for i := 0; i < 512; i++ {
		if chunk0[i] != data[i] {
			t.Fatalf("data mismatch at byte %d", i)
		}
	}

	bf := store.Bitfield()
	if bf[0] != 0xE0 {
		t.Errorf("Bitfield[0]: got 0x%02x, want 0xE0", bf[0])
	}
}

func TestPeerStore(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "video.mp4")
	data := makeTestData(1500)
	os.WriteFile(srcPath, data, 0644)

	manifest, _ := BuildManifest(srcPath, 512)
	store, _ := NewPeerStore(manifest, dir)
	defer store.Close()

	if store.IsComplete() {
		t.Error("should not be complete initially")
	}
	if len(store.MissingChunks()) != 3 {
		t.Error("should have 3 missing chunks")
	}

	store.WriteChunk(1, data[512:1024])
	if !store.HasChunk(1) {
		t.Error("HasChunk(1) should be true")
	}
	if store.Count() != 1 {
		t.Errorf("Count: got %d, want 1", store.Count())
	}
}

func TestPeerStoreHashMismatch(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "video.mp4")
	data := makeTestData(1500)
	os.WriteFile(srcPath, data, 0644)

	manifest, _ := BuildManifest(srcPath, 512)
	store, _ := NewPeerStore(manifest, dir)
	defer store.Close()

	err := store.WriteChunk(0, make([]byte, 512))
	if err == nil {
		t.Error("expected hash mismatch error")
	}
}

func TestStoreWaitForChunk(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "video.mp4")
	data := makeTestData(1500)
	os.WriteFile(srcPath, data, 0644)

	manifest, _ := BuildManifest(srcPath, 512)
	store, _ := NewPeerStore(manifest, dir)
	defer store.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	arrived := make(chan struct{})

	go func() {
		defer wg.Done()
		store.WaitForChunk(0)
		close(arrived)
	}()

	time.Sleep(50 * time.Millisecond)
	select {
	case <-arrived:
		t.Fatal("unblocked too early")
	default:
	}

	store.WriteChunk(0, data[:512])
	select {
	case <-arrived:
	case <-time.After(time.Second):
		t.Fatal("did not unblock")
	}
	wg.Wait()
}

func TestStoreReadBytes(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "video.mp4")
	data := makeTestData(1500)
	os.WriteFile(srcPath, data, 0644)

	manifest, _ := BuildManifest(srcPath, 512)
	store, _ := NewHostStore(srcPath, manifest)
	defer store.Close()

	got, err := store.ReadBytes(500, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if got[i] != data[500+i] {
			t.Fatalf("mismatch at offset %d", 500+i)
		}
	}
}

func TestBitfieldHelpers(t *testing.T) {
	bf := makeBitfield(10)
	setBit(bf, 0)
	setBit(bf, 3)
	setBit(bf, 7)
	setBit(bf, 9)

	for _, idx := range []int{0, 3, 7, 9} {
		if !hasBit(bf, idx) {
			t.Errorf("hasBit(%d) = false", idx)
		}
	}
	if bf[0] != 0x91 {
		t.Errorf("bf[0]: got 0x%02x, want 0x91", bf[0])
	}
	if bf[1] != 0x40 {
		t.Errorf("bf[1]: got 0x%02x, want 0x40", bf[1])
	}
}

func TestStoreUseSHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "video.mp4")
	data := []byte("exactly thirty-two bytes of data!")
	os.WriteFile(path, data, 0644)

	manifest, _ := BuildManifest(path, 512)
	expected := sha256.Sum256(data)
	if manifest.Chunks[0].Hash != expected {
		t.Error("hash doesn't match SHA-256")
	}
}

func makeTestData(size int) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}
	return data
}
