package cmd

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sophic00/peerwatch.git/internal/chunk"
	"github.com/sophic00/peerwatch.git/internal/peer"
	"github.com/sophic00/peerwatch.git/internal/scheduler"
	"github.com/sophic00/peerwatch.git/internal/token"
)

// Join implements the "peerwatch join <token>" command.
// It decodes the connection token, connects to the host, receives the
// manifest, creates a peer store, and begins downloading chunks.
func Join(args []string) {
	fs := flag.NewFlagSet("join", flag.ExitOnError)
	outDir := fs.String("out", ".", "directory to store the downloaded video")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: peerwatch join [flags] <token>\n\nFlags:\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}

	tokenStr := fs.Arg(0)

	// Decode token
	tok, err := token.Decode(tokenStr)
	if err != nil {
		log.Fatalf("invalid token: %v", err)
	}

	log.Printf("joining room %s", tok.RoomID)
	log.Printf("  host:   %s", tok.Host)
	log.Printf("  file:   %s", tok.FileName)
	log.Printf("  size:   %d bytes (%d chunks)", tok.FileSize, tok.ChunkCount)

	// Create swarm (no store yet — we need the manifest first)
	selfID := peer.GeneratePeerID()
	swarm := peer.NewSwarm(selfID, nil, nil, false)
	defer swarm.Close()

	// Connect to host and get manifest
	log.Printf("connecting to host %s...", tok.Host)
	manifest, err := swarm.ConnectToHost(tok.Host)
	if err != nil {
		log.Fatalf("failed to connect to host: %v", err)
	}

	log.Printf("received manifest: %s (%d bytes, %d chunks)",
		manifest.FileName, manifest.FileSize, manifest.ChunkCount)

	// Create peer store (sparse file)
	store, err := chunk.NewPeerStore(manifest, *outDir)
	if err != nil {
		log.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	// Wire up the swarm to write received chunks to the store
	swarm.SetStore(store)

	// Create scheduler
	sched := scheduler.New(store, swarm)
	defer sched.Stop()

	// Wire up piece receipt: store the chunk, then notify the scheduler
	swarm.OnPieceReceived = func(index uint32, data []byte) {
		if err := store.WriteChunk(int(index), data); err != nil {
			log.Printf("write chunk %d: %v", index, err)
			return
		}
		sched.OnPieceReceived(index)

		pct := float64(store.Count()) / float64(manifest.ChunkCount) * 100
		log.Printf("chunk %d/%d (%.1f%%) | in-flight: %d",
			store.Count(), manifest.ChunkCount, pct, sched.InFlightCount())
	}

	// Start periodic bitfield broadcast (every 1s)
	go swarm.StartBitfieldBroadcast(1 * time.Second)

	// Start the scheduler (playback window + rarest-first)
	go sched.Run()

	// TODO(phase4): Start local HTTP server and launch mpv
	// TODO(phase5): Start sync loop

	log.Printf("peer %s | downloading from %d peers...",
		peer.FormatPeerID(selfID), swarm.PeerCount())

	// Block until interrupt or download complete
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("shutting down...")
}
