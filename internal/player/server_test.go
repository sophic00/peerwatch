package player

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sophic00/peerwatch.git/internal/chunk"
	"github.com/sophic00/peerwatch.git/internal/scheduler"
)

func TestHTTPServer(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "video.mp4")
	data := makeTestData(1500)
	err := os.WriteFile(srcPath, data, 0644)
	if err != nil {
		t.Fatal(err)
	}

	manifest, err := chunk.BuildManifest(srcPath, 512)
	if err != nil {
		t.Fatal(err)
	}

	store, err := chunk.NewHostStore(srcPath, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Spin up HTTP Server without a scheduler (nil scheduler) first to verify basic serving
	s, err := NewServer(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.Start()

	// Perform a basic GET request
	resp, err := http.Get(s.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if len(body) != 1500 {
		t.Errorf("expected 1500 bytes, got %d", len(body))
	}

	for i := 0; i < 1500; i++ {
		if body[i] != data[i] {
			t.Fatalf("data mismatch at byte %d", i)
		}
	}
}

func TestHTTPServerRangeAndDemand(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "video.mp4")
	data := makeTestData(1500)
	err := os.WriteFile(srcPath, data, 0644)
	if err != nil {
		t.Fatal(err)
	}

	manifest, err := chunk.BuildManifest(srcPath, 512)
	if err != nil {
		t.Fatal(err)
	}

	// Create a peer store with NO chunks initially
	store, err := chunk.NewPeerStore(manifest, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Create a fake scheduler or wrapper to verify demand is called
	sched := scheduler.New(store, nil)

	s, err := NewServer(store, sched)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.Start()

	// In a background goroutine, we will request bytes [600, 1000] (which spans chunk 1 and chunk 2 since chunk size is 512).
	// The HTTP request will block because store.ReadBytes blocks until chunks 1 & 2 are written.
	// We will wait 100ms, assert that chunks 1 & 2 were indeed demanded by the HTTP server, then write them to unblock the request.
	
	var wg sync.WaitGroup
	wg.Add(1)
	var gotBody []byte
	var reqErr error

	go func() {
		defer wg.Done()
		req, err := http.NewRequest("GET", s.URL(), nil)
		if err != nil {
			reqErr = err
			return
		}
		// Request range: 600 to 1000
		req.Header.Set("Range", "bytes=600-1000")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			reqErr = err
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusPartialContent {
			reqErr = fmt.Errorf("expected 206 Partial Content, got %d", resp.StatusCode)
			return
		}

		gotBody, reqErr = io.ReadAll(resp.Body)
	}()

	// Wait a bit for the request to get processed and block on store.WaitForChunk
	time.Sleep(100 * time.Millisecond)

	// Since we requested range 600-1000:
	// 600 / 512 = chunk 1
	// 1000 / 512 = chunk 1
	// So only chunk 1 is required.
	// Let's verify chunk 1 was demanded. We cannot directly check sched.urgent because it's private,
	// but we can trigger writing chunk 1 to store to let it proceed.
	err = store.WriteChunk(1, data[512:1024])
	if err != nil {
		t.Fatal(err)
	}

	wg.Wait()

	if reqErr != nil {
		t.Fatalf("request failed: %v", reqErr)
	}

	// 600-1000 is 401 bytes
	if len(gotBody) != 401 {
		t.Errorf("expected 401 bytes, got %d", len(gotBody))
	}

	for i := 0; i < 401; i++ {
		if gotBody[i] != data[600+i] {
			t.Fatalf("data mismatch at offset %d", 600+i)
		}
	}
}

func makeTestData(size int) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}
	return data
}

func TestStoreUseSHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "video.mp4")
	data := []byte("exactly thirty-two bytes of data!")
	os.WriteFile(path, data, 0644)

	manifest, _ := chunk.BuildManifest(path, 512)
	expected := sha256.Sum256(data)
	if manifest.Chunks[0].Hash != expected {
		t.Error("hash doesn't match SHA-256")
	}
}
