package player

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestPlayerIPC(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mpv_test_")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	socketPath := filepath.Join(tmpDir, "mpv_test.sock")

	// Start a Unix socket server stub to act as mpv
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	var wg sync.WaitGroup
	wg.Add(1)

	// Stub server loop
	go func() {
		defer wg.Done()
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			var req struct {
				Command []interface{} `json:"command"`
				ID      uint64        `json:"request_id"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
				return
			}

			// Respond based on the command
			var resp []byte
			if len(req.Command) > 0 {
				cmdName := req.Command[0].(string)
				switch cmdName {
				case "get_property":
					prop := req.Command[1].(string)
					if prop == "time-pos" {
						resp, _ = json.Marshal(map[string]interface{}{
							"error":      "success",
							"data":       42.5,
							"request_id": req.ID,
						})
					} else if prop == "pause" {
						resp, _ = json.Marshal(map[string]interface{}{
							"error":      "success",
							"data":       true,
							"request_id": req.ID,
						})
					} else if prop == "duration" {
						resp, _ = json.Marshal(map[string]interface{}{
							"error":      "success",
							"data":       120.0,
							"request_id": req.ID,
						})
					}
				case "set_property":
					resp, _ = json.Marshal(map[string]interface{}{
						"error":      "success",
						"data":       nil,
						"request_id": req.ID,
					})
				}
			}

			resp = append(resp, '\n')
			conn.Write(resp)
		}
	}()

	// Initialize the Player with our custom socket path and mock setup
	p := &Player{
		ipcPath: socketPath,
		pending: make(map[uint64]chan ipcResponse),
		done:    make(chan struct{}),
	}

	// Dial the mock socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	p.conn = conn
	defer p.Close()

	// Start reading
	go p.readLoop()

	// Test GetPlaybackTime
	t.Run("GetPlaybackTime", func(t *testing.T) {
		val, err := p.GetPlaybackTime()
		if err != nil {
			t.Fatal(err)
		}
		if val != 42.5 {
			t.Errorf("expected 42.5, got %f", val)
		}
	})

	// Test GetDuration
	t.Run("GetDuration", func(t *testing.T) {
		val, err := p.GetDuration()
		if err != nil {
			t.Fatal(err)
		}
		if val != 120.0 {
			t.Errorf("expected 120.0, got %f", val)
		}
	})

	// Test IsPaused
	t.Run("IsPaused", func(t *testing.T) {
		paused, err := p.IsPaused()
		if err != nil {
			t.Fatal(err)
		}
		if !paused {
			t.Error("expected paused to be true")
		}
	})

	// Test SetPaused
	t.Run("SetPaused", func(t *testing.T) {
		err := p.SetPaused(false)
		if err != nil {
			t.Fatal(err)
		}
	})

	// Test Seek
	t.Run("Seek", func(t *testing.T) {
		err := p.Seek(120.0)
		if err != nil {
			t.Fatal(err)
		}
	})

	// Test SetSpeed
	t.Run("SetSpeed", func(t *testing.T) {
		err := p.SetSpeed(1.05)
		if err != nil {
			t.Fatal(err)
		}
	})

	// Close client connection and socket listener to unblock mock server goroutine
	p.Close()
	l.Close()
	wg.Wait()
}
