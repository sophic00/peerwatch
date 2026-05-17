// Package peer manages P2P connections in a full-mesh topology.
package peer

import (
	"crypto/rand"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/sophic00/peerwatch.git/internal/chunk"
	"github.com/sophic00/peerwatch.git/internal/protocol"
)

// Peer represents a remote peer in the swarm.
type Peer struct {
	ID   [16]byte
	Addr string

	mu       sync.RWMutex
	conn     net.Conn
	outCh    chan protocol.Message // buffered channel for the write loop
	bitfield []byte                // what chunks they have
	speed    float64               // bytes/sec rolling average
	inFlight map[uint32]time.Time  // chunk index → request time
	done     chan struct{}
}

// NewPeer creates a Peer from an established connection.
func NewPeer(conn net.Conn) *Peer {
	return &Peer{
		conn:     conn,
		Addr:     conn.RemoteAddr().String(),
		outCh:    make(chan protocol.Message, 64),
		inFlight: make(map[uint32]time.Time),
		done:     make(chan struct{}),
	}
}

// Start launches the read and write loops for this peer connection.
func (p *Peer) Start(handler func(*Peer, protocol.Message)) {
	go p.readLoop(handler)
	go p.writeLoop()
}

// Close shuts down the peer connection and stops the read/write loops.
func (p *Peer) Close() {
	select {
	case <-p.done:
		return // already closed
	default:
		close(p.done)
	}
	p.conn.Close()
}

// readLoop reads messages from the TCP connection and dispatches them.
// Runs in its own goroutine. Exits on error or when the peer is closed.
func (p *Peer) readLoop(handler func(*Peer, protocol.Message)) {
	defer p.Close()
	for {
		msg, err := protocol.ReadMessage(p.conn)
		if err != nil {
			select {
			case <-p.done:
				return // expected close
			default:
				log.Printf("peer %s: read error: %v", p.Addr, err)
				return
			}
		}
		handler(p, msg)
	}
}

// writeLoop writes queued messages to the TCP connection.
// Runs in its own goroutine. Only this goroutine writes to the connection,
// preventing concurrent write corruption.
func (p *Peer) writeLoop() {
	defer p.Close()
	for {
		select {
		case msg := <-p.outCh:
			if err := protocol.WriteMessage(p.conn, msg); err != nil {
				select {
				case <-p.done:
					return
				default:
					log.Printf("peer %s: write error: %v", p.Addr, err)
					return
				}
			}
		case <-p.done:
			return
		}
	}
}

// Send enqueues a message for the write loop. Non-blocking; drops if full.
func (p *Peer) Send(msg protocol.Message) {
	select {
	case p.outCh <- msg:
	case <-p.done:
	}
}

// SetBitfield replaces the peer's bitfield (called on BITFIELD message).
func (p *Peer) SetBitfield(bf []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bitfield = make([]byte, len(bf))
	copy(p.bitfield, bf)
}

// HasChunk checks if the peer has a specific chunk.
func (p *Peer) HasChunk(index int) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.bitfield == nil || index/8 >= len(p.bitfield) {
		return false
	}
	return p.bitfield[index/8]&(1<<(7-uint(index%8))) != 0
}

// MarkInFlight records that we've requested a chunk from this peer.
func (p *Peer) MarkInFlight(index uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inFlight[index] = time.Now()
}

// ClearInFlight removes a chunk from the in-flight set and updates speed.
func (p *Peer) ClearInFlight(index uint32, size int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if t, ok := p.inFlight[index]; ok {
		elapsed := time.Since(t).Seconds()
		if elapsed > 0 {
			// Exponential moving average (α=0.3)
			speed := float64(size) / elapsed
			p.speed = 0.7*p.speed + 0.3*speed
		}
		delete(p.inFlight, index)
	}
}

// InFlightCount returns the number of chunks currently requested from this peer.
func (p *Peer) InFlightCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.inFlight)
}

// Speed returns the estimated download speed in bytes/sec.
func (p *Peer) Speed() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.speed
}

// Done returns a channel that's closed when the peer is disconnected.
func (p *Peer) Done() <-chan struct{} {
	return p.done
}

// GeneratePeerID creates a random 16-byte peer identifier.
func GeneratePeerID() [16]byte {
	var id [16]byte
	rand.Read(id[:])
	return id
}

// ManifestToProto converts a chunk.Manifest to a protocol.ManifestMsg.
func ManifestToProto(m *chunk.Manifest) *protocol.ManifestMsg {
	msg := &protocol.ManifestMsg{
		FileName:   m.FileName,
		FileSize:   m.FileSize,
		ChunkSize:  m.ChunkSize,
		ChunkCount: uint32(m.ChunkCount),
		Chunks:     make([]protocol.ChunkInfo, m.ChunkCount),
	}
	for i, c := range m.Chunks {
		msg.Chunks[i] = protocol.ChunkInfo{
			Size: uint32(c.Size),
			Hash: c.Hash,
		}
	}
	return msg
}

// ProtoToManifest converts a protocol.ManifestMsg to a chunk.Manifest.
func ProtoToManifest(msg *protocol.ManifestMsg) *chunk.Manifest {
	m := &chunk.Manifest{
		FileName:   msg.FileName,
		FileSize:   msg.FileSize,
		ChunkSize:  msg.ChunkSize,
		ChunkCount: int(msg.ChunkCount),
		Chunks:     make([]chunk.ChunkMeta, msg.ChunkCount),
	}
	for i, c := range msg.Chunks {
		m.Chunks[i] = chunk.ChunkMeta{
			Size: int64(c.Size),
			Hash: c.Hash,
		}
	}
	return m
}

// FormatPeerID returns a short hex string for display (first 4 bytes).
func FormatPeerID(id [16]byte) string {
	return fmt.Sprintf("%x", id[:4])
}
