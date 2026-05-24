// Package token handles encoding and decoding of connection tokens.
//
// A token is a compact base64url-encoded JSON string that contains
// everything a peer needs to join a room: the host's address, room ID,
// and basic file metadata.
//
// Example token: pw_eyJoIjoiMTkyLjE2OC4xLjU6OTg3NiIs...
package token

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

const prefix = "pw_"

// Token contains the information needed to join a watch party room.
type Token struct {
	Host       string `json:"h"` // Host address as "ip:port"
	RoomID     string `json:"r"` // Random room identifier
	FileName   string `json:"f"` // Name of the video file
	FileSize   int64  `json:"s"` // File size in bytes
	ChunkCount int    `json:"c"` // Total number of chunks
}

// validate checks that all required Token fields are present and well-formed.
func (t *Token) validate() error {
	if t.Host == "" {
		return fmt.Errorf("token missing host address")
	}
	if _, _, err := net.SplitHostPort(t.Host); err != nil {
		return fmt.Errorf("token host %q is not a valid host:port: %w", t.Host, err)
	}
	if t.RoomID == "" {
		return fmt.Errorf("token missing room ID")
	}
	if t.FileName == "" {
		return fmt.Errorf("token missing file name")
	}
	if t.FileSize <= 0 {
		return fmt.Errorf("token has invalid file size %d", t.FileSize)
	}
	if t.ChunkCount <= 0 {
		return fmt.Errorf("token has invalid chunk count %d", t.ChunkCount)
	}
	return nil
}

// Encode validates the token and serializes it to a prefixed base64url string.
func (t *Token) Encode() (string, error) {
	if err := t.validate(); err != nil {
		return "", err
	}

	data, err := json.Marshal(t)
	if err != nil {
		return "", fmt.Errorf("failed to encode token: %w", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(data), nil
}

// Decode parses a token string (with or without the "pw_" prefix).
func Decode(s string) (*Token, error) {
	if s == "" {
		return nil, fmt.Errorf("token is empty")
	}
	s = strings.TrimPrefix(s, prefix)

	data, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid token encoding: %w", err)
	}

	var t Token
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("invalid token data: %w", err)
	}

	if err := t.validate(); err != nil {
		return nil, err
	}

	return &t, nil
}
