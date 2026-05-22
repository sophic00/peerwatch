package player

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// ipcResponse represents a response from mpv's JSON-RPC interface.
type ipcResponse struct {
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
	ID    uint64          `json:"request_id"`
}

// Player controls an mpv process via its Unix socket IPC interface.
type Player struct {
	cmd      *exec.Cmd
	ipcPath  string
	tmpDir   string
	conn     net.Conn
	mu       sync.Mutex
	reqID    uint64
	pending  map[uint64]chan ipcResponse
	done     chan struct{}
	closed   bool
}

// NewPlayer creates a Player instance. It initializes the temporary directory
// for the Unix domain socket inside the current working directory.
func NewPlayer() (*Player, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get working directory: %w", err)
	}

	tmpDir, err := os.MkdirTemp(cwd, "peerwatch_mpv_")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir for mpv: %w", err)
	}

	ipcPath := filepath.Join(tmpDir, "mpv.sock")

	return &Player{
		ipcPath: ipcPath,
		tmpDir:  tmpDir,
		pending: make(map[uint64]chan ipcResponse),
		done:    make(chan struct{}),
	}, nil
}

// SetConnForTest injects a custom connection and starts the read loop for testing purposes.
func (p *Player) SetConnForTest(conn net.Conn) {
	p.mu.Lock()
	p.conn = conn
	p.mu.Unlock()
	go p.readLoop()
}

// Start launches the mpv process and connects to its IPC interface.
func (p *Player) Start(videoURL string) error {
	p.cmd = exec.Command("mpv",
		"--input-ipc-server="+p.ipcPath,
		"--cache=yes",
		"--cache-secs=10",
		"--demuxer-max-bytes=50MiB",
		"--idle=yes",         // stay open if file load takes time
		"--force-window=yes", // always keep window visible
		videoURL,
	)

	// Suppress mpv stderr/stdout to keep terminal logs clean, or redirect them
	p.cmd.Stdout = nil
	p.cmd.Stderr = nil

	if err := p.cmd.Start(); err != nil {
		p.cleanup()
		return fmt.Errorf("failed to start mpv: %w", err)
	}

	// Connect to the IPC socket
	if err := p.connectIPC(); err != nil {
		p.Close()
		return err
	}

	// Start the IPC reader loop
	go p.readLoop()

	return nil
}

func (p *Player) connectIPC() error {
	var conn net.Conn
	var err error

	// Retry dialing for up to 5 seconds
	for i := 0; i < 50; i++ {
		conn, err = net.Dial("unix", p.ipcPath)
		if err == nil {
			p.conn = conn
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("failed to connect to mpv IPC socket: %w", err)
}

func (p *Player) readLoop() {
	scanner := bufio.NewScanner(p.conn)
	for scanner.Scan() {
		line := scanner.Bytes()
		var msg struct {
			Error     string          `json:"error"`
			Data      json.RawMessage `json:"data"`
			RequestID uint64          `json:"request_id"`
			Event     string          `json:"event"`
		}

		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		if msg.RequestID != 0 {
			p.mu.Lock()
			ch, ok := p.pending[msg.RequestID]
			if ok {
				ch <- ipcResponse{
					Data:  msg.Data,
					Error: msg.Error,
					ID:    msg.RequestID,
				}
				delete(p.pending, msg.RequestID)
			}
			p.mu.Unlock()
		}
	}
}

// call sends a command to mpv and blocks until a response is received or timeout occurs.
func (p *Player) call(command []interface{}) (json.RawMessage, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("player is closed")
	}
	p.reqID++
	id := p.reqID
	ch := make(chan ipcResponse, 1)
	p.pending[id] = ch
	p.mu.Unlock()

	req := map[string]interface{}{
		"command":    command,
		"request_id": id,
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	reqBytes = append(reqBytes, '\n')

	p.mu.Lock()
	if p.conn == nil {
		p.mu.Unlock()
		return nil, fmt.Errorf("no connection to mpv IPC")
	}
	_, err = p.conn.Write(reqBytes)
	p.mu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("failed to write to IPC: %w", err)
	}

	select {
	case res := <-ch:
		if res.Error != "success" && res.Error != "" {
			return nil, fmt.Errorf("mpv returned error: %s", res.Error)
		}
		return res.Data, nil
	case <-time.After(2 * time.Second):
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, fmt.Errorf("timeout waiting for response from mpv")
	}
}

// GetPlaybackTime queries the current playback position in seconds.
func (p *Player) GetPlaybackTime() (float64, error) {
	res, err := p.call([]interface{}{"get_property", "time-pos"})
	if err != nil {
		return 0, err
	}

	if len(res) == 0 || string(res) == "null" {
		return 0, nil // playback hasn't started or is buffering
	}

	var val float64
	if err := json.Unmarshal(res, &val); err != nil {
		return 0, fmt.Errorf("failed to unmarshal time-pos: %w", err)
	}
	return val, nil
}

// GetDuration queries the video duration in seconds.
func (p *Player) GetDuration() (float64, error) {
	res, err := p.call([]interface{}{"get_property", "duration"})
	if err != nil {
		return 0, err
	}

	if len(res) == 0 || string(res) == "null" {
		return 0, nil // duration not determined yet
	}

	var val float64
	if err := json.Unmarshal(res, &val); err != nil {
		return 0, fmt.Errorf("failed to unmarshal duration: %w", err)
	}
	return val, nil
}

// IsPaused returns true if playback is currently paused.
func (p *Player) IsPaused() (bool, error) {
	res, err := p.call([]interface{}{"get_property", "pause"})
	if err != nil {
		return false, err
	}

	var val bool
	if err := json.Unmarshal(res, &val); err != nil {
		return false, fmt.Errorf("failed to unmarshal pause: %w", err)
	}
	return val, nil
}

// SetPaused plays or pauses the player.
func (p *Player) SetPaused(paused bool) error {
	_, err := p.call([]interface{}{"set_property", "pause", paused})
	return err
}

// Seek seeks to an absolute time in seconds.
func (p *Player) Seek(seconds float64) error {
	_, err := p.call([]interface{}{"set_property", "time-pos", seconds})
	return err
}

// SetSpeed sets the playback speed (e.g. 1.05 for +5% or 0.95 for -5%).
func (p *Player) SetSpeed(speed float64) error {
	_, err := p.call([]interface{}{"set_property", "speed", speed})
	return err
}

// Close gracefully closes the socket connection, kills the mpv process, and cleans up the temp files.
func (p *Player) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()

	// Terminate the socket connection
	if p.conn != nil {
		p.conn.Close()
	}

	// Terminate the mpv process
	if p.cmd != nil && p.cmd.Process != nil {
		// Try to exit cleanly first
		p.call([]interface{}{"quit"})
		// Give it a short moment, then force kill if needed
		done := make(chan struct{})
		go func() {
			p.cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
			p.cmd.Process.Kill()
		}
	}

	p.cleanup()
	return nil
}

func (p *Player) cleanup() {
	if p.tmpDir != "" {
		os.RemoveAll(p.tmpDir)
	}
}
