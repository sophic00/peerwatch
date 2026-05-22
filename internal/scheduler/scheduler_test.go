package scheduler

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sophic00/peerwatch.git/internal/chunk"
	"github.com/sophic00/peerwatch.git/internal/peer"
)

// TestSchedulerDrivenDownload is an integration test:
// host starts with a file → peer connects → scheduler downloads all chunks.
func TestSchedulerDrivenDownload(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "video.mp4")

	// 5120 bytes = 10 chunks of 512 bytes
	data := make([]byte, 5120)
	for i := range data {
		data[i] = byte(i % 251)
	}
	os.WriteFile(srcPath, data, 0644)

	manifest, err := chunk.BuildManifest(srcPath, 512)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ChunkCount != 10 {
		t.Fatalf("expected 10 chunks, got %d", manifest.ChunkCount)
	}

	// --- Host ---
	hostID := peer.GeneratePeerID()
	hostStore, err := chunk.NewHostStore(srcPath, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer hostStore.Close()

	hostSwarm := peer.NewSwarm(hostID, hostStore, manifest, true)
	defer hostSwarm.Close()

	if err := hostSwarm.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	go hostSwarm.StartBitfieldBroadcast(100 * time.Millisecond)

	hostAddr := hostSwarm.ListenAddr()

	// --- Peer ---
	peerID := peer.GeneratePeerID()
	peerSwarm := peer.NewSwarm(peerID, nil, nil, false)
	defer peerSwarm.Close()

	gotManifest, err := peerSwarm.ConnectToHost(hostAddr)
	if err != nil {
		t.Fatal(err)
	}

	peerDir := filepath.Join(dir, "peer")
	os.MkdirAll(peerDir, 0755)
	peerStore, err := chunk.NewPeerStore(gotManifest, peerDir)
	if err != nil {
		t.Fatal(err)
	}
	defer peerStore.Close()

	peerSwarm.SetStore(peerStore)

	// Create scheduler
	sched := New(peerStore, peerSwarm)
	defer sched.Stop()

	// Wire up piece receipt
	peerSwarm.OnPieceReceived = func(index uint32, data []byte) {
		if err := peerStore.WriteChunk(int(index), data); err != nil {
			t.Errorf("write chunk %d: %v", index, err)
			return
		}
		sched.OnPieceReceived(index)
	}

	go peerSwarm.StartBitfieldBroadcast(100 * time.Millisecond)

	// Let bitfield propagation happen
	time.Sleep(200 * time.Millisecond)

	// Start scheduler
	go sched.Run()

	// Wait for download to complete
	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out: got %d/10 chunks", peerStore.Count())
		case <-ticker.C:
			if peerStore.IsComplete() {
				goto done
			}
		}
	}
done:

	// Verify file integrity
	for i := 0; i < 10; i++ {
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

	t.Logf("scheduler-driven download complete: 10 chunks, file verified")
}
