package token

import (
	"testing"
)

func TestTokenRoundtrip(t *testing.T) {
	orig := &Token{
		Host:       "192.168.1.5:9876",
		RoomID:     "abcd1234",
		FileName:   "movie.mp4",
		FileSize:   734003200,
		ChunkCount: 1400,
	}

	encoded := orig.Encode()

	// Should have pw_ prefix
	if encoded[:3] != "pw_" {
		t.Errorf("missing pw_ prefix: %s", encoded[:10])
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if decoded.Host != orig.Host {
		t.Errorf("Host: got %q, want %q", decoded.Host, orig.Host)
	}
	if decoded.RoomID != orig.RoomID {
		t.Errorf("RoomID: got %q, want %q", decoded.RoomID, orig.RoomID)
	}
	if decoded.FileName != orig.FileName {
		t.Errorf("FileName: got %q, want %q", decoded.FileName, orig.FileName)
	}
	if decoded.FileSize != orig.FileSize {
		t.Errorf("FileSize: got %d, want %d", decoded.FileSize, orig.FileSize)
	}
	if decoded.ChunkCount != orig.ChunkCount {
		t.Errorf("ChunkCount: got %d, want %d", decoded.ChunkCount, orig.ChunkCount)
	}
}

func TestTokenDecodeWithoutPrefix(t *testing.T) {
	orig := &Token{Host: "10.0.0.1:8080", RoomID: "test", FileName: "a.mkv", FileSize: 100, ChunkCount: 1}
	encoded := orig.Encode()

	// Strip prefix and decode
	stripped := encoded[3:]
	decoded, err := Decode(stripped)
	if err != nil {
		t.Fatalf("Decode without prefix: %v", err)
	}
	if decoded.Host != orig.Host {
		t.Errorf("Host: got %q, want %q", decoded.Host, orig.Host)
	}
}

func TestTokenDecodeInvalid(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"garbage", "not-valid-base64!!!"},
		{"empty json", "pw_e30"},              // {} — missing host
		{"no filename", "pw_eyJoIjoiMTAuMC4wLjE6ODA4MCJ9"}, // {"h":"10.0.0.1:8080"} — missing filename
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode(tc.input)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}
