package protocol

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// MaxMessageSize is the maximum allowed message size (16 MB).
// This limits memory usage when reading from untrusted peers.
// The largest expected message is a PIECE (512KB chunk + header ≈ 524KB).
const MaxMessageSize = 16 * 1024 * 1024

// WriteMessage encodes and writes a framed message to w.
// Format: [4-byte length][1-byte type][payload]
func WriteMessage(w io.Writer, msg Message) error {
	var payload bytes.Buffer

	switch m := msg.(type) {
	case *HandshakeMsg:
		encodeHandshake(&payload, m)
	case *ManifestMsg:
		encodeManifest(&payload, m)
	case *BitfieldMsg:
		encodeBitfield(&payload, m)
	case *HaveMsg:
		encodeUint32Msg(&payload, m.ChunkIndex)
	case *RequestMsg:
		encodeUint32Msg(&payload, m.ChunkIndex)
	case *PieceMsg:
		encodePiece(&payload, m)
	case *CancelMsg:
		encodeUint32Msg(&payload, m.ChunkIndex)
	case *SyncMsg:
		encodeSync(&payload, m)
	case *PeerListMsg:
		encodePeerList(&payload, m)
	case *KeepaliveMsg:
		// empty payload
	default:
		return fmt.Errorf("unknown message type: %T", msg)
	}

	// Write length prefix: type byte (1) + payload length
	length := uint32(1 + payload.Len())
	if err := binary.Write(w, binary.BigEndian, length); err != nil {
		return fmt.Errorf("write length: %w", err)
	}

	// Write type byte
	if _, err := w.Write([]byte{msg.Type()}); err != nil {
		return fmt.Errorf("write type: %w", err)
	}

	// Write payload
	if payload.Len() > 0 {
		if _, err := w.Write(payload.Bytes()); err != nil {
			return fmt.Errorf("write payload: %w", err)
		}
	}

	return nil
}

// ReadMessage reads a single framed message from r.
func ReadMessage(r io.Reader) (Message, error) {
	// Read length prefix
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, fmt.Errorf("read length: %w", err)
	}

	if length == 0 {
		return nil, fmt.Errorf("message length is zero")
	}

	if length > MaxMessageSize {
		return nil, fmt.Errorf("message too large: %d bytes (max %d)", length, MaxMessageSize)
	}

	// Read type + payload
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("read message body: %w", err)
	}

	msgType := data[0]
	payload := data[1:]

	switch msgType {
	case MsgHandshake:
		return decodeHandshake(payload)
	case MsgManifest:
		return decodeManifest(payload)
	case MsgBitfield:
		return decodeBitfield(payload)
	case MsgHave:
		return decodeHave(payload)
	case MsgRequest:
		return decodeRequest(payload)
	case MsgPiece:
		return decodePiece(payload)
	case MsgCancel:
		return decodeCancel(payload)
	case MsgSync:
		return decodeSync(payload)
	case MsgPeerList:
		return decodePeerList(payload)
	case MsgKeepalive:
		return &KeepaliveMsg{}, nil
	default:
		return nil, fmt.Errorf("unknown message type: 0x%02x", msgType)
	}
}

// --- Encoders ---

func encodeHandshake(buf *bytes.Buffer, m *HandshakeMsg) {
	buf.Write(m.PeerID[:])
	buf.WriteByte(m.Version)
}

func encodeManifest(buf *bytes.Buffer, m *ManifestMsg) {
	// filename: length-prefixed string
	binary.Write(buf, binary.BigEndian, uint16(len(m.FileName)))
	buf.WriteString(m.FileName)
	binary.Write(buf, binary.BigEndian, m.FileSize)
	binary.Write(buf, binary.BigEndian, m.ChunkSize)
	binary.Write(buf, binary.BigEndian, m.ChunkCount)
	for _, c := range m.Chunks {
		binary.Write(buf, binary.BigEndian, c.Size)
		buf.Write(c.Hash[:])
	}
}

func encodeBitfield(buf *bytes.Buffer, m *BitfieldMsg) {
	buf.Write(m.Bitfield)
}

func encodeUint32Msg(buf *bytes.Buffer, v uint32) {
	binary.Write(buf, binary.BigEndian, v)
}

func encodePiece(buf *bytes.Buffer, m *PieceMsg) {
	binary.Write(buf, binary.BigEndian, m.ChunkIndex)
	buf.Write(m.Data)
}

func encodeSync(buf *bytes.Buffer, m *SyncMsg) {
	binary.Write(buf, binary.BigEndian, math.Float64bits(m.PlaybackTime))
	buf.WriteByte(m.State)
	binary.Write(buf, binary.BigEndian, m.UnixMs)
}

func encodePeerList(buf *bytes.Buffer, m *PeerListMsg) {
	binary.Write(buf, binary.BigEndian, uint32(len(m.Addrs)))
	for _, addr := range m.Addrs {
		binary.Write(buf, binary.BigEndian, uint16(len(addr)))
		buf.WriteString(addr)
	}
}

// --- Decoders ---

func decodeHandshake(data []byte) (*HandshakeMsg, error) {
	if len(data) < 17 {
		return nil, fmt.Errorf("handshake too short: %d bytes", len(data))
	}
	m := &HandshakeMsg{
		Version: data[16],
	}
	copy(m.PeerID[:], data[:16])
	return m, nil
}

func decodeManifest(data []byte) (*ManifestMsg, error) {
	r := bytes.NewReader(data)

	var fnLen uint16
	if err := binary.Read(r, binary.BigEndian, &fnLen); err != nil {
		return nil, fmt.Errorf("read filename length: %w", err)
	}

	fnBytes := make([]byte, fnLen)
	if _, err := io.ReadFull(r, fnBytes); err != nil {
		return nil, fmt.Errorf("read filename: %w", err)
	}

	m := &ManifestMsg{
		FileName: string(fnBytes),
	}

	if err := binary.Read(r, binary.BigEndian, &m.FileSize); err != nil {
		return nil, fmt.Errorf("read file size: %w", err)
	}
	if err := binary.Read(r, binary.BigEndian, &m.ChunkSize); err != nil {
		return nil, fmt.Errorf("read chunk size: %w", err)
	}
	if err := binary.Read(r, binary.BigEndian, &m.ChunkCount); err != nil {
		return nil, fmt.Errorf("read chunk count: %w", err)
	}

	m.Chunks = make([]ChunkInfo, m.ChunkCount)
	for i := uint32(0); i < m.ChunkCount; i++ {
		if err := binary.Read(r, binary.BigEndian, &m.Chunks[i].Size); err != nil {
			return nil, fmt.Errorf("read chunk %d size: %w", i, err)
		}
		if _, err := io.ReadFull(r, m.Chunks[i].Hash[:]); err != nil {
			return nil, fmt.Errorf("read chunk %d hash: %w", i, err)
		}
	}

	return m, nil
}

func decodeBitfield(data []byte) (*BitfieldMsg, error) {
	bf := make([]byte, len(data))
	copy(bf, data)
	return &BitfieldMsg{Bitfield: bf}, nil
}

func decodeHave(data []byte) (*HaveMsg, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("have message too short: %d bytes", len(data))
	}
	return &HaveMsg{
		ChunkIndex: binary.BigEndian.Uint32(data[:4]),
	}, nil
}

func decodeRequest(data []byte) (*RequestMsg, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("request message too short: %d bytes", len(data))
	}
	return &RequestMsg{
		ChunkIndex: binary.BigEndian.Uint32(data[:4]),
	}, nil
}

func decodePiece(data []byte) (*PieceMsg, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("piece message too short: %d bytes", len(data))
	}
	chunkData := make([]byte, len(data)-4)
	copy(chunkData, data[4:])
	return &PieceMsg{
		ChunkIndex: binary.BigEndian.Uint32(data[:4]),
		Data:       chunkData,
	}, nil
}

func decodeCancel(data []byte) (*CancelMsg, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("cancel message too short: %d bytes", len(data))
	}
	return &CancelMsg{
		ChunkIndex: binary.BigEndian.Uint32(data[:4]),
	}, nil
}

func decodeSync(data []byte) (*SyncMsg, error) {
	if len(data) < 17 {
		return nil, fmt.Errorf("sync message too short: %d bytes", len(data))
	}
	return &SyncMsg{
		PlaybackTime: math.Float64frombits(binary.BigEndian.Uint64(data[:8])),
		State:        data[8],
		UnixMs:       int64(binary.BigEndian.Uint64(data[9:17])),
	}, nil
}

func decodePeerList(data []byte) (*PeerListMsg, error) {
	r := bytes.NewReader(data)

	var count uint32
	if err := binary.Read(r, binary.BigEndian, &count); err != nil {
		return nil, fmt.Errorf("read peer count: %w", err)
	}

	m := &PeerListMsg{
		Addrs: make([]string, count),
	}

	for i := uint32(0); i < count; i++ {
		var addrLen uint16
		if err := binary.Read(r, binary.BigEndian, &addrLen); err != nil {
			return nil, fmt.Errorf("read addr %d length: %w", i, err)
		}
		addrBytes := make([]byte, addrLen)
		if _, err := io.ReadFull(r, addrBytes); err != nil {
			return nil, fmt.Errorf("read addr %d: %w", i, err)
		}
		m.Addrs[i] = string(addrBytes)
	}

	return m, nil
}
