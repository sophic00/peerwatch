package peer

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/sophic00/peerwatch.git/internal/chunk"
	"github.com/sophic00/peerwatch.git/internal/protocol"
)

// Swarm manages the full mesh of peer connections.
//
// For the host, it listens for incoming connections and sends manifest/peer list.
// For joiners, it connects to the host, receives the manifest, then connects
// to all other peers to form the mesh.
type Swarm struct {
	mu       sync.RWMutex
	selfID   [16]byte
	isHost   bool
	store    *chunk.Store
	manifest *chunk.Manifest
	tracker  *Tracker
	peers    map[[16]byte]*Peer
	listener net.Listener

	// Callbacks for higher layers
	OnPieceReceived func(index uint32, data []byte) // called when a PIECE arrives
	OnManifest      func(manifest *chunk.Manifest)   // called when joiner receives manifest
	OnSyncReceived  func(msg *protocol.SyncMsg)      // called when joiner receives a SYNC message from host

	done chan struct{}
}

// NewSwarm creates a new swarm.
//
// For a host swarm, store and manifest must be non-nil (the host has the
// file from the start). For a joiner swarm, both may be nil — the joiner
// receives the manifest from the host and creates the store afterward.
func NewSwarm(selfID [16]byte, store *chunk.Store, manifest *chunk.Manifest, isHost bool) *Swarm {
	if isHost {
		if store == nil {
			panic("peerwatch: host swarm requires a non-nil store")
		}
		if manifest == nil {
			panic("peerwatch: host swarm requires a non-nil manifest")
		}
	}

	var tracker *Tracker
	if manifest != nil {
		tracker = NewTracker(manifest.ChunkCount)
	}
	return &Swarm{
		selfID:   selfID,
		isHost:   isHost,
		store:    store,
		manifest: manifest,
		tracker:  tracker,
		peers:    make(map[[16]byte]*Peer),
		done:     make(chan struct{}),
	}
}

// SetStore sets the chunk store. Used by joiners who create the store
// after receiving the manifest from the host.
//
// Panics if called on a host swarm (host must provide store at creation).
func (s *Swarm) SetStore(store *chunk.Store) {
	if s.isHost {
		panic("peerwatch: SetStore must not be called on a host swarm")
	}
	if store == nil {
		panic("peerwatch: SetStore called with nil store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store = store
}

// Listen starts accepting incoming peer connections on the given address.
//
// Panics if called on a non-host swarm.
func (s *Swarm) Listen(addr string) error {
	if !s.isHost {
		panic("peerwatch: Listen must only be called on a host swarm")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.listener = ln
	log.Printf("listening on %s", ln.Addr())

	go s.acceptLoop()
	return nil
}

// Connect dials a remote peer and performs the handshake.
// Used by joiners to connect to the host and other peers.
func (s *Swarm) Connect(addr string) (*Peer, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}

	p := NewPeer(conn)

	// Send our handshake
	if err := protocol.WriteMessage(conn, &protocol.HandshakeMsg{
		PeerID:  s.selfID,
		Version: 1,
	}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("send handshake: %w", err)
	}

	// Read their handshake
	msg, err := protocol.ReadMessage(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read handshake: %w", err)
	}
	hs, ok := msg.(*protocol.HandshakeMsg)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("expected handshake, got %T", msg)
	}
	p.ID = hs.PeerID

	// If we're the joiner connecting to host, read manifest first
	// The host sends: MANIFEST → BITFIELD → PEER_LIST
	// Other peers send: BITFIELD only

	// Start read/write loops
	p.Start(s.handleMessage)

	// Send our bitfield
	if s.store != nil {
		p.Send(&protocol.BitfieldMsg{Bitfield: s.store.Bitfield()})
	}

	s.addPeer(p)
	log.Printf("connected to peer %s (%s)", FormatPeerID(p.ID), p.Addr)

	return p, nil
}

// ConnectToHost connects to the host, receives manifest + peer list,
// then connects to all other peers.
//
// Panics if called on a host swarm — the host does not connect to itself.
func (s *Swarm) ConnectToHost(hostAddr string) (*chunk.Manifest, error) {
	if s.isHost {
		panic("peerwatch: ConnectToHost must not be called on a host swarm")
	}
	conn, err := net.DialTimeout("tcp", hostAddr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial host %s: %w", hostAddr, err)
	}

	// Handshake
	if err := protocol.WriteMessage(conn, &protocol.HandshakeMsg{
		PeerID:  s.selfID,
		Version: 1,
	}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("send handshake: %w", err)
	}

	msg, err := protocol.ReadMessage(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read handshake: %w", err)
	}
	hs, ok := msg.(*protocol.HandshakeMsg)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("expected handshake, got %T", msg)
	}

	p := NewPeer(conn)
	p.ID = hs.PeerID
	log.Printf("connected to host %s (%s)", FormatPeerID(p.ID), p.Addr)

	// Read MANIFEST
	msg, err = protocol.ReadMessage(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	manifestMsg, ok := msg.(*protocol.ManifestMsg)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("expected manifest, got %T", msg)
	}

	manifest := ProtoToManifest(manifestMsg)
	s.manifest = manifest
	s.tracker = NewTracker(manifest.ChunkCount)

	// Read BITFIELD
	msg, err = protocol.ReadMessage(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read host bitfield: %w", err)
	}
	if bf, ok := msg.(*protocol.BitfieldMsg); ok {
		p.SetBitfield(bf.Bitfield)
		s.tracker.UpdateFromBitfield(p.ID, bf.Bitfield)
	}

	// Read PEER_LIST
	msg, err = protocol.ReadMessage(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read peer list: %w", err)
	}
	peerList, ok := msg.(*protocol.PeerListMsg)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("expected peer list, got %T", msg)
	}

	// Start host peer's read/write loops
	p.Start(s.handleMessage)

	// Send our bitfield to host
	if s.store != nil {
		p.Send(&protocol.BitfieldMsg{Bitfield: s.store.Bitfield()})
	}

	s.addPeer(p)

	// Connect to other peers from the peer list
	for _, addr := range peerList.Addrs {
		go func(addr string) {
			peer, err := s.Connect(addr)
			if err != nil {
				log.Printf("failed to connect to peer %s: %v", addr, err)
				return
			}
			_ = peer
		}(addr)
	}

	return manifest, nil
}

// Broadcast sends a message to all connected peers.
func (s *Swarm) Broadcast(msg protocol.Message) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.peers {
		p.Send(msg)
	}
}

// RequestChunks sends a batch chunk request to a specific peer.
func (s *Swarm) RequestChunks(peerID [16]byte, indices []uint32) {
	s.mu.RLock()
	p, ok := s.peers[peerID]
	s.mu.RUnlock()
	if !ok {
		return
	}

	for _, idx := range indices {
		p.MarkInFlight(idx)
	}
	p.Send(&protocol.RequestMsg{ChunkIndices: indices})
}

// Tracker returns the chunk availability tracker.
func (s *Swarm) Tracker() *Tracker {
	return s.tracker
}

// PeerCount returns the number of connected peers.
func (s *Swarm) PeerCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.peers)
}

// ListenAddr returns the address the swarm is listening on.
// Returns empty string if not listening.
func (s *Swarm) ListenAddr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// GetPeer returns a peer by ID.
func (s *Swarm) GetPeer(id [16]byte) *Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.peers[id]
}

// Peers returns a snapshot of all connected peers.
func (s *Swarm) Peers() []*Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	peers := make([]*Peer, 0, len(s.peers))
	for _, p := range s.peers {
		peers = append(peers, p)
	}
	return peers
}

// StartBitfieldBroadcast periodically sends our bitfield to all peers.
// This replaces per-chunk HAVE messages — simpler and self-correcting.
//
// This is a blocking function. The caller should run it in a goroutine:
//
//	go swarm.StartBitfieldBroadcast(1 * time.Second)
//
// Returns when the swarm is closed.
func (s *Swarm) StartBitfieldBroadcast(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if s.store != nil {
				s.Broadcast(&protocol.BitfieldMsg{Bitfield: s.store.Bitfield()})
			}
		case <-s.done:
			return
		}
	}
}

// Close shuts down the swarm: stops listener, disconnects all peers.
func (s *Swarm) Close() {
	select {
	case <-s.done:
		return
	default:
		close(s.done)
	}

	if s.listener != nil {
		s.listener.Close()
	}

	s.mu.Lock()
	for _, p := range s.peers {
		p.Close()
	}
	s.mu.Unlock()
}

// --- internal ---

func (s *Swarm) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				log.Printf("accept error: %v", err)
				continue
			}
		}
		go s.handleIncoming(conn)
	}
}

func (s *Swarm) handleIncoming(conn net.Conn) {
	// Read their handshake
	msg, err := protocol.ReadMessage(conn)
	if err != nil {
		log.Printf("incoming handshake read error: %v", err)
		conn.Close()
		return
	}
	hs, ok := msg.(*protocol.HandshakeMsg)
	if !ok {
		log.Printf("incoming: expected handshake, got %T", msg)
		conn.Close()
		return
	}

	p := NewPeer(conn)
	p.ID = hs.PeerID

	// Send our handshake back
	if err := protocol.WriteMessage(conn, &protocol.HandshakeMsg{
		PeerID:  s.selfID,
		Version: 1,
	}); err != nil {
		log.Printf("send handshake to %s: %v", p.Addr, err)
		conn.Close()
		return
	}

	// If we're the host, send manifest + bitfield + peer list
	if s.isHost {
		// MANIFEST
		if err := protocol.WriteMessage(conn, ManifestToProto(s.manifest)); err != nil {
			log.Printf("send manifest to %s: %v", p.Addr, err)
			conn.Close()
			return
		}

		// BITFIELD
		if err := protocol.WriteMessage(conn, &protocol.BitfieldMsg{
			Bitfield: s.store.Bitfield(),
		}); err != nil {
			log.Printf("send bitfield to %s: %v", p.Addr, err)
			conn.Close()
			return
		}

		// PEER_LIST — addresses of all other connected peers
		peerAddrs := s.peerAddrs()
		if err := protocol.WriteMessage(conn, &protocol.PeerListMsg{
			Addrs: peerAddrs,
		}); err != nil {
			log.Printf("send peer list to %s: %v", p.Addr, err)
			conn.Close()
			return
		}
	} else {
		// Non-host peer: just send bitfield
		if s.store != nil {
			if err := protocol.WriteMessage(conn, &protocol.BitfieldMsg{
				Bitfield: s.store.Bitfield(),
			}); err != nil {
				log.Printf("send bitfield to %s: %v", p.Addr, err)
				conn.Close()
				return
			}
		}
	}

	// Start the peer's read/write loops
	p.Start(s.handleMessage)
	s.addPeer(p)

	log.Printf("peer joined: %s (%s) [%d peers total]",
		FormatPeerID(p.ID), p.Addr, s.PeerCount())
}

func (s *Swarm) handleMessage(from *Peer, msg protocol.Message) {
	switch m := msg.(type) {
	case *protocol.BitfieldMsg:
		from.SetBitfield(m.Bitfield)
		if s.tracker != nil {
			s.tracker.UpdateFromBitfield(from.ID, m.Bitfield)
		}

	case *protocol.RequestMsg:
		// Serve requested chunks
		for _, idx := range m.ChunkIndices {
			if s.store != nil && s.store.HasChunk(int(idx)) {
				data, err := s.store.ReadChunk(int(idx))
				if err != nil {
					log.Printf("read chunk %d for %s: %v", idx, FormatPeerID(from.ID), err)
					continue
				}
				from.Send(&protocol.PieceMsg{
					ChunkIndex: idx,
					Data:       data,
				})
			}
		}

	case *protocol.PieceMsg:
		// Pass to the callback (scheduler/store will handle it)
		from.ClearInFlight(m.ChunkIndex, len(m.Data))
		if s.OnPieceReceived != nil {
			s.OnPieceReceived(m.ChunkIndex, m.Data)
		}

	case *protocol.HaveMsg:
		// Unused in v1 but handle gracefully for forward compatibility
		// Update the peer's bitfield if we receive one
		from.mu.Lock()
		if from.bitfield != nil {
			idx := int(m.ChunkIndex)
			if idx/8 < len(from.bitfield) {
				from.bitfield[idx/8] |= 1 << (7 - uint(idx%8))
			}
		}
		from.mu.Unlock()
		if s.tracker != nil {
			s.tracker.UpdateFromBitfield(from.ID, from.bitfield)
		}

	case *protocol.SyncMsg:
		if s.OnSyncReceived != nil {
			s.OnSyncReceived(m)
		}

	case *protocol.PeerListMsg:
		// Connect to any new peers we don't already know
		for _, addr := range m.Addrs {
			if !s.hasAddr(addr) {
				go func(addr string) {
					if _, err := s.Connect(addr); err != nil {
						log.Printf("connect to peer %s: %v", addr, err)
					}
				}(addr)
			}
		}

	case *protocol.KeepaliveMsg:
		// no-op

	case *protocol.CancelMsg:
		// TODO: cancel in-progress chunk read

	default:
		log.Printf("unhandled message type %T from %s", msg, FormatPeerID(from.ID))
	}
}

func (s *Swarm) addPeer(p *Peer) {
	s.mu.Lock()
	s.peers[p.ID] = p
	s.mu.Unlock()

	// Monitor for disconnection
	go func() {
		<-p.Done()
		s.mu.Lock()
		delete(s.peers, p.ID)
		s.mu.Unlock()
		if s.tracker != nil {
			s.tracker.RemovePeer(p.ID)
		}
		log.Printf("peer disconnected: %s (%s) [%d peers remaining]",
			FormatPeerID(p.ID), p.Addr, s.PeerCount())
	}()
}

func (s *Swarm) peerAddrs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	addrs := make([]string, 0, len(s.peers))
	for _, p := range s.peers {
		addrs = append(addrs, p.Addr)
	}
	return addrs
}

func (s *Swarm) hasAddr(addr string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.peers {
		if p.Addr == addr {
			return true
		}
	}
	return false
}
