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
	swarm.OnPieceReceived = func(index uint32, data []byte) {
		if err := store.WriteChunk(int(index), data); err != nil {
			log.Printf("write chunk %d: %v", index, err)
			return
		}
		log.Printf("received chunk %d/%d (%.1f%%)",
			index+1, manifest.ChunkCount,
			float64(store.Count())/float64(manifest.ChunkCount)*100)
	}

	// Start periodic bitfield broadcast (every 1s)
	swarm.StartBitfieldBroadcast(1 * time.Second)

	// Start a simple sequential download loop
	// TODO(phase3): replace with proper scheduler
	go func() {
		for {
			if store.IsComplete() {
				log.Printf("download complete! file saved to %s", store.FilePath())
				return
			}

			// Find next missing chunks and request them
			missing := store.MissingChunks()
			if len(missing) == 0 {
				return
			}

			// Batch up to 4 chunks per request, send to peers that have them
			batch := make([]uint32, 0, 4)
			for _, idx := range missing {
				if len(batch) >= 4 {
					break
				}
				batch = append(batch, uint32(idx))
			}

			// Find a peer to request from
			peers := swarm.Peers()
			if len(peers) == 0 {
				time.Sleep(500 * time.Millisecond)
				continue
			}

			// Simple round-robin: request from first peer that has the chunks
			for _, p := range peers {
				peerBatch := make([]uint32, 0, len(batch))
				for _, idx := range batch {
					if p.HasChunk(int(idx)) {
						peerBatch = append(peerBatch, idx)
					}
				}
				if len(peerBatch) > 0 {
					swarm.RequestChunks(p.ID, peerBatch)
				}
			}

			// Wait a bit before next request cycle
			time.Sleep(100 * time.Millisecond)
		}
	}()

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
