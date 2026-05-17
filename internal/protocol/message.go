// Package protocol defines the wire protocol for peer-to-peer communication.
//
// Each message on the TCP connection is framed as:
//
//	┌──────────────────┬───────────────┬──────────────────┐
//	│ Length (4 bytes) │ Type (1 byte) │ Payload (N bytes)│
//	│ big-endian uint32│               │                  │
//	└──────────────────┴───────────────┴──────────────────┘
//
// Length includes the type byte and payload (i.e. length = 1 + len(payload)).
package protocol

// Message type identifiers.
const (
	MsgHandshake byte = 0x01
	MsgManifest  byte = 0x02
	MsgBitfield  byte = 0x03
	MsgHave      byte = 0x04
	MsgRequest   byte = 0x05
	MsgPiece     byte = 0x06
	MsgCancel    byte = 0x07
	MsgSync      byte = 0x08
	MsgPeerList  byte = 0x09
	MsgKeepalive byte = 0x0A
)

// Message is the interface implemented by all protocol messages.
type Message interface {
	Type() byte
}

// HandshakeMsg is exchanged when two peers first connect.
type HandshakeMsg struct {
	PeerID  [16]byte // Random UUID identifying this peer
	Version uint8    // Protocol version (currently 1)
}

func (m *HandshakeMsg) Type() byte { return MsgHandshake }

// ManifestMsg describes the video file being shared.
// Sent from host to joining peers after handshake.
type ManifestMsg struct {
	FileName   string
	FileSize   int64
	ChunkSize  int64
	ChunkCount uint32
	Chunks     []ChunkInfo
}

func (m *ManifestMsg) Type() byte { return MsgManifest }

// ChunkInfo holds metadata for a single chunk within the manifest.
type ChunkInfo struct {
	Size uint32
	Hash [32]byte // SHA-256
}

// BitfieldMsg communicates which chunks a peer currently has.
// Each bit represents one chunk (big-endian bit order within each byte).
// Bit 0 of byte 0 = chunk 0, bit 1 of byte 0 = chunk 1, etc.
type BitfieldMsg struct {
	Bitfield []byte
}

func (m *BitfieldMsg) Type() byte { return MsgBitfield }

// HaveMsg announces that this peer has acquired a new chunk.
// NOTE: Unused in v1. Peers instead resend their full bitfield periodically
// (every ~1s). Kept in the protocol for future optimization where real-time
// per-chunk announcements are needed.
type HaveMsg struct {
	ChunkIndex uint32
}

func (m *HaveMsg) Type() byte { return MsgHave }

// RequestMsg requests one or more chunks from a peer in a single message.
// The responder sends back individual PieceMsg for each requested chunk.
// Wire format: count uint32 + chunk_indices []uint32
type RequestMsg struct {
	ChunkIndices []uint32
}

func (m *RequestMsg) Type() byte { return MsgRequest }

// PieceMsg carries the actual chunk data in response to a RequestMsg.
type PieceMsg struct {
	ChunkIndex uint32
	Data       []byte
}

func (m *PieceMsg) Type() byte { return MsgPiece }

// CancelMsg cancels a previously sent RequestMsg.
type CancelMsg struct {
	ChunkIndex uint32
}

func (m *CancelMsg) Type() byte { return MsgCancel }

// Playback states for SyncMsg.
const (
	StatePaused  uint8 = 0
	StatePlaying uint8 = 1
)

// SyncMsg is broadcast by the host to synchronize playback across peers.
type SyncMsg struct {
	PlaybackTime float64 // Current playback position in seconds
	State        uint8   // StatePaused or StatePlaying
	UnixMs       int64   // Host's wall-clock time (for drift calculation)
}

func (m *SyncMsg) Type() byte { return MsgSync }

// PeerListMsg shares the addresses of known peers.
// Sent by host to new peers so they can connect to the full mesh.
type PeerListMsg struct {
	Addrs []string // Each addr is "ip:port"
}

func (m *PeerListMsg) Type() byte { return MsgPeerList }

// KeepaliveMsg is sent periodically to keep the connection alive.
type KeepaliveMsg struct{}

func (m *KeepaliveMsg) Type() byte { return MsgKeepalive }
