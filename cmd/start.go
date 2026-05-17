package cmd

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sophic00/peerwatch.git/internal/chunk"
	"github.com/sophic00/peerwatch.git/internal/peer"
	"github.com/sophic00/peerwatch.git/internal/token"
)

// Supported video file extensions.
var supportedFormats = map[string]bool{
	".mp4":  true,
	".mkv":  true,
	".avi":  true,
	".webm": true,
}

// Start implements the "peerwatch start <file>" command.
// It validates the video file, builds a manifest, starts the swarm,
// and waits for peers.
func Start(args []string) {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	port := fs.Int("port", 9876, "port to listen on")
	chunkSize := fs.Int64("chunk-size", chunk.DefaultChunkSize, "chunk size in bytes")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: peerwatch start [flags] <file>\n\nFlags:\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}

	filePath := fs.Arg(0)

	// Validate file exists
	info, err := os.Stat(filePath)
	if err != nil {
		log.Fatalf("cannot access file: %v", err)
	}
	if info.IsDir() {
		log.Fatalf("%s is a directory, not a file", filePath)
	}

	// Validate format
	ext := strings.ToLower(filepath.Ext(filePath))
	if !supportedFormats[ext] {
		log.Fatalf("unsupported format %q (supported: .mp4, .mkv, .avi, .webm)", ext)
	}

	// Build manifest
	log.Printf("building manifest for %s (%d bytes)...", filepath.Base(filePath), info.Size())
	manifest, err := chunk.BuildManifest(filePath, *chunkSize)
	if err != nil {
		log.Fatalf("failed to build manifest: %v", err)
	}
	log.Printf("manifest ready: %d chunks × %d bytes", manifest.ChunkCount, *chunkSize)

	// Create host store
	store, err := chunk.NewHostStore(filePath, manifest)
	if err != nil {
		log.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	// Create swarm
	selfID := peer.GeneratePeerID()
	swarm := peer.NewSwarm(selfID, store, manifest, true)
	defer swarm.Close()

	// Start listening
	listenAddr := fmt.Sprintf(":%d", *port)
	if err := swarm.Listen(listenAddr); err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	// Start periodic bitfield broadcast (every 1s)
	swarm.StartBitfieldBroadcast(1 * time.Second)

	// Generate connection token
	localIP := getLocalIP()
	tok := &token.Token{
		Host:       fmt.Sprintf("%s:%d", localIP, *port),
		RoomID:     generateRoomID(),
		FileName:   manifest.FileName,
		FileSize:   manifest.FileSize,
		ChunkCount: manifest.ChunkCount,
	}

	encoded := tok.Encode()
	fmt.Println()
	fmt.Println("room created! share this token with your friends:")
	fmt.Printf("\n  %s\n\n", encoded)
	fmt.Printf("join command:\n  ./peerwatch join %s\n\n", encoded)

	log.Printf("room %s | host %s | peer %s | %d chunks | waiting for peers...",
		tok.RoomID, tok.Host, peer.FormatPeerID(selfID), manifest.ChunkCount)

	// TODO(phase4): Start local HTTP server and launch mpv

	// Block until interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("shutting down...")
}

// getLocalIP returns the machine's preferred outbound IP address.
// It uses a UDP dial trick — no data is actually sent.
func getLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// generateRoomID returns a random 8-character hex string.
func generateRoomID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}
