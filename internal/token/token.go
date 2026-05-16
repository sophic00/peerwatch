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

// Encode serializes the token to a prefixed base64url string.
func (t *Token) Encode() string {
	data, _ := json.Marshal(t)
	return prefix + base64.RawURLEncoding.EncodeToString(data)
}

// Decode parses a token string (with or without the "pw_" prefix).
func Decode(s string) (*Token, error) {
	s = strings.TrimPrefix(s, prefix)

	data, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid token encoding: %w", err)
	}

	var t Token
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("invalid token data: %w", err)
	}

	if t.Host == "" {
		return nil, fmt.Errorf("token missing host address")
	}
	if t.FileName == "" {
		return nil, fmt.Errorf("token missing file name")
	}

	return &t, nil
}
