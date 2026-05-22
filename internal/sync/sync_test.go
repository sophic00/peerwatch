package sync

import (
	"bufio"
	"encoding/json"
	"math"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sophic00/peerwatch.git/internal/player"
	"github.com/sophic00/peerwatch.git/internal/protocol"
)

type mockMpvServer struct {
	mu           sync.Mutex
	playbackTime float64
	paused       bool
	speed        float64
	lastSeek     float64
	conn         net.Conn
}

func (m *mockMpvServer) GetTime() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.playbackTime
}

func (m *mockMpvServer) SetTime(t float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.playbackTime = t
}

func (m *mockMpvServer) GetPaused() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.paused
}

func (m *mockMpvServer) SetPaused(p bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paused = p
}

func (m *mockMpvServer) GetSpeed() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.speed
}

func (m *mockMpvServer) GetLastSeek() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastSeek
}

func (m *mockMpvServer) CloseConn() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conn != nil {
		m.conn.Close()
	}
}

func TestSyncManager(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sync_test_")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	socketPath := filepath.Join(tmpDir, "mpv_test.sock")

	l, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	srv := &mockMpvServer{
		playbackTime: 10.0,
		paused:       false,
		speed:        1.0,
	}

	var wg sync.WaitGroup
	wg.Add(1)

	// Stub server loop
	go func() {
		defer wg.Done()
		conn, err := l.Accept()
		if err != nil {
			return
		}
		srv.mu.Lock()
		srv.conn = conn
		srv.mu.Unlock()
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

			var resp []byte
			if len(req.Command) > 0 {
				cmdName := req.Command[0].(string)
				switch cmdName {
				case "get_property":
					prop := req.Command[1].(string)
					srv.mu.Lock()
					var val interface{}
					if prop == "time-pos" {
						val = srv.playbackTime
					} else if prop == "pause" {
						val = srv.paused
					} else if prop == "speed" {
						val = srv.speed
					}
					srv.mu.Unlock()

					resp, _ = json.Marshal(map[string]interface{}{
						"error":      "success",
						"data":       val,
						"request_id": req.ID,
					})
				case "set_property":
					prop := req.Command[1].(string)
					val := req.Command[2]
					srv.mu.Lock()
					if prop == "pause" {
						srv.paused = val.(bool)
					} else if prop == "speed" {
						srv.speed = val.(float64)
					} else if prop == "time-pos" {
						srv.playbackTime = val.(float64)
						srv.lastSeek = val.(float64)
					}
					srv.mu.Unlock()

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

	// Initialize Player and inject connection
	p, err := player.NewPlayer()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	clientConn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()
	p.SetConnForTest(clientConn)

	// Create SyncManager
	sm := NewSyncManager(nil, p, false)

	// Wait briefly for player initialization
	time.Sleep(50 * time.Millisecond)

	// 1. Test Drift: Within tolerance window (e.g. drift = 0.2s)
	t.Run("DriftWithinTolerance", func(t *testing.T) {
		srv.SetTime(10.2)
		srv.SetPaused(false)

		msg := &protocol.SyncMsg{
			PlaybackTime: 10.0,
			State:        protocol.StatePlaying,
			UnixMs:       time.Now().UnixNano() / int64(time.Millisecond),
		}

		sm.handleSync(msg)
		time.Sleep(20 * time.Millisecond)

		if speed := srv.GetSpeed(); speed != 1.0 {
			t.Errorf("expected speed 1.0, got %f", speed)
		}
	})

	// 2. Test Drift: Behind (e.g. peer = 9.0s, host = 10.0s -> drift = -1.0s)
	t.Run("DriftBehind", func(t *testing.T) {
		srv.SetTime(9.0)
		srv.SetPaused(false)

		msg := &protocol.SyncMsg{
			PlaybackTime: 10.0,
			State:        protocol.StatePlaying,
			UnixMs:       time.Now().UnixNano() / int64(time.Millisecond),
		}

		sm.handleSync(msg)
		time.Sleep(20 * time.Millisecond)

		if speed := srv.GetSpeed(); speed != 1.05 {
			t.Errorf("expected speed 1.05, got %f", speed)
		}
	})

	// 3. Test Drift: Ahead (e.g. peer = 11.0s, host = 10.0s -> drift = +1.0s)
	t.Run("DriftAhead", func(t *testing.T) {
		srv.SetTime(11.0)
		srv.SetPaused(false)

		msg := &protocol.SyncMsg{
			PlaybackTime: 10.0,
			State:        protocol.StatePlaying,
			UnixMs:       time.Now().UnixNano() / int64(time.Millisecond),
		}

		sm.handleSync(msg)
		time.Sleep(20 * time.Millisecond)

		if speed := srv.GetSpeed(); speed != 0.95 {
			t.Errorf("expected speed 0.95, got %f", speed)
		}
	})

	// 4. Test Drift: Hard Seek (e.g. peer = 6.0s, host = 10.0s -> drift = -4.0s)
	t.Run("DriftHardSeek", func(t *testing.T) {
		srv.SetTime(6.0)
		srv.SetPaused(false)

		msg := &protocol.SyncMsg{
			PlaybackTime: 10.0,
			State:        protocol.StatePlaying,
			UnixMs:       time.Now().UnixNano() / int64(time.Millisecond),
		}

		sm.handleSync(msg)
		time.Sleep(20 * time.Millisecond)

		if lastSeek := srv.GetLastSeek(); math.Abs(lastSeek-10.0) > 0.1 {
			t.Errorf("expected absolute seek to ~10.0, got %f", lastSeek)
		}
		if speed := srv.GetSpeed(); speed != 1.0 {
			t.Errorf("expected speed reset to 1.0, got %f", speed)
		}
	})

	// 5. Test State: Host Paused
	t.Run("HostPaused", func(t *testing.T) {
		srv.SetTime(10.0)
		srv.SetPaused(false)

		msg := &protocol.SyncMsg{
			PlaybackTime: 8.5,
			State:        protocol.StatePaused,
			UnixMs:       time.Now().UnixNano() / int64(time.Millisecond),
		}

		sm.handleSync(msg)
		time.Sleep(20 * time.Millisecond)

		if !srv.GetPaused() {
			t.Error("expected local player to be paused")
		}
		if lastSeek := srv.GetLastSeek(); lastSeek != 8.5 {
			t.Errorf("expected pause seek to 8.5, got %f", lastSeek)
		}
	})

	// 6. Test State: Host Resumed
	t.Run("HostResumed", func(t *testing.T) {
		srv.SetTime(8.5)
		srv.SetPaused(true)

		msg := &protocol.SyncMsg{
			PlaybackTime: 8.5,
			State:        protocol.StatePlaying,
			UnixMs:       time.Now().UnixNano() / int64(time.Millisecond),
		}

		sm.handleSync(msg)
		time.Sleep(20 * time.Millisecond)

		if srv.GetPaused() {
			t.Error("expected local player to be resumed")
		}
	})

	// Explicitly close all connections to guarantee unblocking
	clientConn.Close()
	srv.CloseConn()
	l.Close()
	wg.Wait()
}
