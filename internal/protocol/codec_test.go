package protocol

import (
	"bytes"
	"io"
	"math"
	"testing"
)

// TestCodecRoundtrip verifies encode→decode produces identical messages
// for every message type in the wire protocol.
func TestCodecRoundtrip(t *testing.T) {
	t.Run("Handshake", func(t *testing.T) {
		orig := &HandshakeMsg{
			PeerID:  [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
			Version: 1,
		}

		decoded := roundtrip(t, orig)
		got := decoded.(*HandshakeMsg)

		if got.PeerID != orig.PeerID {
			t.Errorf("PeerID: got %v, want %v", got.PeerID, orig.PeerID)
		}
		if got.Version != orig.Version {
			t.Errorf("Version: got %d, want %d", got.Version, orig.Version)
		}
	})

	t.Run("Manifest", func(t *testing.T) {
		orig := &ManifestMsg{
			FileName:   "test_movie.mp4",
			FileSize:   1073741824,
			ChunkSize:  524288,
			ChunkCount: 3,
			Chunks: []ChunkInfo{
				{Size: 524288, Hash: sha256Hash("chunk0")},
				{Size: 524288, Hash: sha256Hash("chunk1")},
				{Size: 100, Hash: sha256Hash("chunk2")},
			},
		}

		decoded := roundtrip(t, orig)
		got := decoded.(*ManifestMsg)

		if got.FileName != orig.FileName {
			t.Errorf("FileName: got %q, want %q", got.FileName, orig.FileName)
		}
		if got.FileSize != orig.FileSize {
			t.Errorf("FileSize: got %d, want %d", got.FileSize, orig.FileSize)
		}
		if got.ChunkSize != orig.ChunkSize {
			t.Errorf("ChunkSize: got %d, want %d", got.ChunkSize, orig.ChunkSize)
		}
		if got.ChunkCount != orig.ChunkCount {
			t.Errorf("ChunkCount: got %d, want %d", got.ChunkCount, orig.ChunkCount)
		}
		for i, c := range got.Chunks {
			if c.Size != orig.Chunks[i].Size {
				t.Errorf("Chunk[%d].Size: got %d, want %d", i, c.Size, orig.Chunks[i].Size)
			}
			if c.Hash != orig.Chunks[i].Hash {
				t.Errorf("Chunk[%d].Hash mismatch", i)
			}
		}
	})

	t.Run("Bitfield", func(t *testing.T) {
		orig := &BitfieldMsg{Bitfield: []byte{0b11001010, 0b00110101}}

		decoded := roundtrip(t, orig)
		got := decoded.(*BitfieldMsg)

		if !bytes.Equal(got.Bitfield, orig.Bitfield) {
			t.Errorf("Bitfield: got %v, want %v", got.Bitfield, orig.Bitfield)
		}
	})

	t.Run("Have", func(t *testing.T) {
		orig := &HaveMsg{ChunkIndex: 42}
		decoded := roundtrip(t, orig)
		got := decoded.(*HaveMsg)
		if got.ChunkIndex != orig.ChunkIndex {
			t.Errorf("ChunkIndex: got %d, want %d", got.ChunkIndex, orig.ChunkIndex)
		}
	})

	t.Run("Request", func(t *testing.T) {
		orig := &RequestMsg{ChunkIndices: []uint32{0, 42, 1337, 9999}}
		decoded := roundtrip(t, orig)
		got := decoded.(*RequestMsg)
		if len(got.ChunkIndices) != len(orig.ChunkIndices) {
			t.Fatalf("ChunkIndices count: got %d, want %d", len(got.ChunkIndices), len(orig.ChunkIndices))
		}
		for i, idx := range got.ChunkIndices {
			if idx != orig.ChunkIndices[i] {
				t.Errorf("ChunkIndices[%d]: got %d, want %d", i, idx, orig.ChunkIndices[i])
			}
		}
	})

	t.Run("Piece", func(t *testing.T) {
		data := make([]byte, 1024)
		for i := range data {
			data[i] = byte(i % 256)
		}
		orig := &PieceMsg{ChunkIndex: 7, Data: data}

		decoded := roundtrip(t, orig)
		got := decoded.(*PieceMsg)

		if got.ChunkIndex != orig.ChunkIndex {
			t.Errorf("ChunkIndex: got %d, want %d", got.ChunkIndex, orig.ChunkIndex)
		}
		if !bytes.Equal(got.Data, orig.Data) {
			t.Errorf("Data: got %d bytes, want %d bytes", len(got.Data), len(orig.Data))
		}
	})

	t.Run("Cancel", func(t *testing.T) {
		orig := &CancelMsg{ChunkIndex: 99}
		decoded := roundtrip(t, orig)
		got := decoded.(*CancelMsg)
		if got.ChunkIndex != orig.ChunkIndex {
			t.Errorf("ChunkIndex: got %d, want %d", got.ChunkIndex, orig.ChunkIndex)
		}
	})

	t.Run("Sync", func(t *testing.T) {
		orig := &SyncMsg{
			PlaybackTime: 123.456,
			State:        StatePlaying,
			UnixMs:       1715806000000,
		}

		decoded := roundtrip(t, orig)
		got := decoded.(*SyncMsg)

		if got.PlaybackTime != orig.PlaybackTime {
			t.Errorf("PlaybackTime: got %f, want %f", got.PlaybackTime, orig.PlaybackTime)
		}
		if got.State != orig.State {
			t.Errorf("State: got %d, want %d", got.State, orig.State)
		}
		if got.UnixMs != orig.UnixMs {
			t.Errorf("UnixMs: got %d, want %d", got.UnixMs, orig.UnixMs)
		}
	})

	t.Run("PeerList", func(t *testing.T) {
		orig := &PeerListMsg{
			Addrs: []string{"192.168.1.5:9876", "10.0.0.2:9877", "[::1]:9878"},
		}

		decoded := roundtrip(t, orig)
		got := decoded.(*PeerListMsg)

		if len(got.Addrs) != len(orig.Addrs) {
			t.Fatalf("Addrs count: got %d, want %d", len(got.Addrs), len(orig.Addrs))
		}
		for i, addr := range got.Addrs {
			if addr != orig.Addrs[i] {
				t.Errorf("Addrs[%d]: got %q, want %q", i, addr, orig.Addrs[i])
			}
		}
	})

	t.Run("Keepalive", func(t *testing.T) {
		orig := &KeepaliveMsg{}
		decoded := roundtrip(t, orig)
		if _, ok := decoded.(*KeepaliveMsg); !ok {
			t.Errorf("expected *KeepaliveMsg, got %T", decoded)
		}
	})
}

// TestSyncSpecialValues checks encoding of float64 edge cases.
func TestSyncSpecialValues(t *testing.T) {
	cases := []float64{0.0, 0.0, math.MaxFloat64, math.SmallestNonzeroFloat64}
	for _, v := range cases {
		orig := &SyncMsg{PlaybackTime: v, State: StatePaused, UnixMs: 0}
		decoded := roundtrip(t, orig).(*SyncMsg)
		if decoded.PlaybackTime != v {
			t.Errorf("PlaybackTime %v: got %v", v, decoded.PlaybackTime)
		}
	}
}

// TestManifestEmptyChunks verifies encoding a manifest with zero chunks.
func TestManifestEmptyFileName(t *testing.T) {
	orig := &ManifestMsg{
		FileName:   "",
		FileSize:   0,
		ChunkSize:  524288,
		ChunkCount: 0,
		Chunks:     nil,
	}

	decoded := roundtrip(t, orig).(*ManifestMsg)
	if decoded.FileName != "" {
		t.Errorf("FileName: got %q, want empty", decoded.FileName)
	}
	if decoded.ChunkCount != 0 {
		t.Errorf("ChunkCount: got %d, want 0", decoded.ChunkCount)
	}
}

// TestMessageTooLarge verifies that oversized messages are rejected.
func TestMessageTooLarge(t *testing.T) {
	var buf bytes.Buffer
	// Write a length that exceeds MaxMessageSize
	huge := &PieceMsg{ChunkIndex: 0, Data: make([]byte, MaxMessageSize+1)}
	if err := WriteMessage(&buf, huge); err != nil {
		// Encoding might succeed, reading should fail
		return
	}

	_, err := ReadMessage(&buf)
	if err == nil {
		t.Error("expected error for oversized message, got nil")
	}
}

// --- helpers ---

func roundtrip(t *testing.T, msg Message) Message {
	t.Helper()

	var buf bytes.Buffer
	if err := WriteMessage(&buf, msg); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	decoded, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if decoded.Type() != msg.Type() {
		t.Fatalf("Type: got 0x%02x, want 0x%02x", decoded.Type(), msg.Type())
	}

	return decoded
}

func sha256Hash(s string) [32]byte {
	var h [32]byte
	copy(h[:], []byte(s))
	return h
}

func BenchmarkWriteMessage(b *testing.B) {
	msg := &PieceMsg{
		ChunkIndex: 42,
		Data:       make([]byte, 524288), // 512KB
	}
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		err := WriteMessage(io.Discard, msg)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWriteMessageSmall(b *testing.B) {
	msg := &SyncMsg{
		PlaybackTime: 123.456,
		State:        StatePlaying,
		UnixMs:       1715806000000,
	}
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		err := WriteMessage(io.Discard, msg)
		if err != nil {
			b.Fatal(err)
		}
	}
}

