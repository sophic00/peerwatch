package protocol

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sync"
)

// MaxMessageSize is the maximum allowed message size (16 MB).
// This limits memory usage when reading from untrusted peers.
// The largest expected message is a PIECE (512KB chunk + header ≈ 524KB).
const MaxMessageSize = 16 * 1024 * 1024

var bufferPool = sync.Pool{
	New: func() interface{} {
		// Pre-allocate a capacity large enough for a PieceMsg payload (512KB + headers)
		return bytes.NewBuffer(make([]byte, 0, 524288))
	},
}

// WriteMessage encodes and writes a framed message to w.
// Format: [4-byte length][1-byte type][payload]
func WriteMessage(w io.Writer, msg Message) error {
	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufferPool.Put(buf)

	// Pre-allocate 5 bytes for the framed header: [4-byte length][1-byte type]
	var headerDummy [5]byte
	buf.Write(headerDummy[:])

	switch m := msg.(type) {
	case *HandshakeMsg:
		encodeHandshake(buf, m)
	case *ManifestMsg:
		encodeManifest(buf, m)
	case *BitfieldMsg:
		encodeBitfield(buf, m)
	case *HaveMsg:
		encodeUint32Msg(buf, m.ChunkIndex)
	case *RequestMsg:
		encodeRequest(buf, m)
	case *PieceMsg:
		encodePiece(buf, m)
	case *CancelMsg:
		encodeUint32Msg(buf, m.ChunkIndex)
	case *SyncMsg:
		encodeSync(buf, m)
	case *PeerListMsg:
		encodePeerList(buf, m)
	case *KeepaliveMsg:
		// empty payload
	default:
		return fmt.Errorf("unknown message type: %T", msg)
	}

	data := buf.Bytes()
	wireLength := uint32(len(data) - 4) // length = type byte (1) + payload length

	// Write length prefix and type byte directly into pre-allocated header space
	binary.BigEndian.PutUint32(data[0:4], wireLength)
	data[4] = msg.Type()

	// Write the entire coalesced packet in a single write call
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write message: %w", err)
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
		return decodeRequestBatch(payload)
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
	var tmp [20]byte
	binary.BigEndian.PutUint16(tmp[0:2], uint16(len(m.FileName)))
	buf.Write(tmp[0:2])
	buf.WriteString(m.FileName)

	binary.BigEndian.PutUint64(tmp[0:8], uint64(m.FileSize))
	binary.BigEndian.PutUint64(tmp[8:16], uint64(m.ChunkSize))
	binary.BigEndian.PutUint32(tmp[16:20], m.ChunkCount)
	buf.Write(tmp[0:20])

	for _, c := range m.Chunks {
		binary.BigEndian.PutUint32(tmp[0:4], c.Size)
		buf.Write(tmp[0:4])
		buf.Write(c.Hash[:])
	}
}

func encodeBitfield(buf *bytes.Buffer, m *BitfieldMsg) {
	buf.Write(m.Bitfield)
}

func encodeUint32Msg(buf *bytes.Buffer, v uint32) {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], v)
	buf.Write(tmp[:])
}

func encodePiece(buf *bytes.Buffer, m *PieceMsg) {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], m.ChunkIndex)
	buf.Write(tmp[:])
	buf.Write(m.Data)
}

func encodeSync(buf *bytes.Buffer, m *SyncMsg) {
	var tmp [17]byte
	binary.BigEndian.PutUint64(tmp[0:8], math.Float64bits(m.PlaybackTime))
	tmp[8] = m.State
	binary.BigEndian.PutUint64(tmp[9:17], uint64(m.UnixMs))
	buf.Write(tmp[:])
}

func encodePeerList(buf *bytes.Buffer, m *PeerListMsg) {
	encodeUint32Msg(buf, uint32(len(m.Addrs)))
	var tmp [2]byte
	for _, addr := range m.Addrs {
		binary.BigEndian.PutUint16(tmp[:], uint16(len(addr)))
		buf.Write(tmp[:])
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

func encodeRequest(buf *bytes.Buffer, m *RequestMsg) {
	encodeUint32Msg(buf, uint32(len(m.ChunkIndices)))
	for _, idx := range m.ChunkIndices {
		encodeUint32Msg(buf, idx)
	}
}

func decodeRequestBatch(data []byte) (*RequestMsg, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("request message too short: %d bytes", len(data))
	}
	r := bytes.NewReader(data)

	var count uint32
	if err := binary.Read(r, binary.BigEndian, &count); err != nil {
		return nil, fmt.Errorf("read request count: %w", err)
	}

	indices := make([]uint32, count)
	for i := uint32(0); i < count; i++ {
		if err := binary.Read(r, binary.BigEndian, &indices[i]); err != nil {
			return nil, fmt.Errorf("read request index %d: %w", i, err)
		}
	}

	return &RequestMsg{ChunkIndices: indices}, nil
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
