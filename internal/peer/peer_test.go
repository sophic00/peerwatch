package peer

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sophic00/peerwatch.git/internal/chunk"
)

// TestSwarmHostPeerTransfer is an integration test:
// host starts with a file → peer connects → peer downloads all chunks.
func TestSwarmHostPeerTransfer(t *testing.T) {
	// Create test file
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "video.mp4")

	// 2560 bytes = 5 chunks of 512 bytes
	data := make([]byte, 2560)
	for i := range data {
		data[i] = byte(i % 251)
	}
	os.WriteFile(srcPath, data, 0644)

	// Build manifest
	manifest, err := chunk.BuildManifest(srcPath, 512)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ChunkCount != 5 {
		t.Fatalf("expected 5 chunks, got %d", manifest.ChunkCount)
	}

	// --- Host setup ---
	hostID := GeneratePeerID()
	hostStore, err := chunk.NewHostStore(srcPath, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer hostStore.Close()

	hostSwarm := NewSwarm(hostID, hostStore, manifest, true)
	defer hostSwarm.Close()

	if err := hostSwarm.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}

	// Start host bitfield broadcast
	go hostSwarm.StartBitfieldBroadcast(200 * time.Millisecond)

	// Get the actual listen address (port 0 → random port)
	hostAddr := hostSwarm.listener.Addr().String()

	// --- Peer setup ---
	peerID := GeneratePeerID()
	peerSwarm := NewSwarm(peerID, nil, nil, false)
	defer peerSwarm.Close()

	// Connect to host, get manifest
	gotManifest, err := peerSwarm.ConnectToHost(hostAddr)
	if err != nil {
		t.Fatal(err)
	}

	if gotManifest.FileName != "video.mp4" {
		t.Errorf("manifest filename: got %q, want %q", gotManifest.FileName, "video.mp4")
	}
	if gotManifest.ChunkCount != 5 {
		t.Errorf("manifest chunks: got %d, want 5", gotManifest.ChunkCount)
	}

	// Create peer store
	peerDir := filepath.Join(dir, "peer")
	os.MkdirAll(peerDir, 0755)
	peerStore, err := chunk.NewPeerStore(gotManifest, peerDir)
	if err != nil {
		t.Fatal(err)
	}
	defer peerStore.Close()

	peerSwarm.SetStore(peerStore)

	// Wire up chunk receipt
	peerSwarm.OnPieceReceived = func(index uint32, data []byte) {
		if err := peerStore.WriteChunk(int(index), data); err != nil {
			t.Errorf("write chunk %d: %v", index, err)
		}
	}

	// Start peer bitfield broadcast
	go peerSwarm.StartBitfieldBroadcast(200 * time.Millisecond)

	// Give the connection time to establish
	time.Sleep(100 * time.Millisecond)

	// Request all 5 chunks in one batch
	peers := peerSwarm.Peers()
	if len(peers) == 0 {
		t.Fatal("no peers connected")
	}

	hostPeer := peers[0]
	peerSwarm.RequestChunks(hostPeer.ID, []uint32{0, 1, 2, 3, 4})

	// Wait for all chunks to arrive
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out: got %d/5 chunks", peerStore.Count())
		case <-ticker.C:
			if peerStore.IsComplete() {
				goto done
			}
		}
	}
done:

	// Verify the downloaded file matches the original
	for i := 0; i < 5; i++ {
		origChunk := data[i*512 : (i+1)*512]
		gotChunk, err := peerStore.ReadChunk(i)
		if err != nil {
			t.Fatalf("read chunk %d: %v", i, err)
		}
		origHash := sha256.Sum256(origChunk)
		gotHash := sha256.Sum256(gotChunk)
		if origHash != gotHash {
			t.Errorf("chunk %d hash mismatch", i)
		}
	}

	t.Logf("transfer complete: 5 chunks, file verified")
}

// TestTrackerRarity verifies the availability tracker.
func TestTrackerRarity(t *testing.T) {
	tracker := NewTracker(10)

	peerA := [16]byte{1}
	peerB := [16]byte{2}

	// Peer A has chunks 0, 1, 2
	bfA := make([]byte, 2)
	bfA[0] = 0b11100000 // bits 0,1,2
	tracker.UpdateFromBitfield(peerA, bfA)

	// Peer B has chunks 1, 2, 3
	bfB := make([]byte, 2)
	bfB[0] = 0b01110000 // bits 1,2,3
	tracker.UpdateFromBitfield(peerB, bfB)

	// Chunk 0: only A has it → rarity 1
	if r := tracker.Rarity(0); r != 1 {
		t.Errorf("rarity(0): got %d, want 1", r)
	}
	// Chunk 1: A and B → rarity 2
	if r := tracker.Rarity(1); r != 2 {
		t.Errorf("rarity(1): got %d, want 2", r)
	}
	// Chunk 3: only B → rarity 1
	if r := tracker.Rarity(3); r != 1 {
		t.Errorf("rarity(3): got %d, want 1", r)
	}
	// Chunk 5: nobody → rarity 0
	if r := tracker.Rarity(5); r != 0 {
		t.Errorf("rarity(5): got %d, want 0", r)
	}

	// Remove peer A
	tracker.RemovePeer(peerA)
	if r := tracker.Rarity(0); r != 0 {
		t.Errorf("after remove A, rarity(0): got %d, want 0", r)
	}
	if r := tracker.Rarity(1); r != 1 {
		t.Errorf("after remove A, rarity(1): got %d, want 1", r)
	}
}
