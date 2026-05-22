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
	"github.com/sophic00/peerwatch.git/internal/player"
	"github.com/sophic00/peerwatch.git/internal/scheduler"
	"github.com/sophic00/peerwatch.git/internal/sync"
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

	// Wire up peer disconnection: release in-flight chunks so they can be rescheduled instantly
	swarm.OnPeerDisconnected = func(peerID [16]byte, inFlightChunks []uint32) {
		if len(inFlightChunks) > 0 {
			log.Printf("peer %s disconnected | releasing %d in-flight chunks to scheduler pool",
				peer.FormatPeerID(peerID), len(inFlightChunks))
			sched.ReleaseInFlight(inFlightChunks)
		}
	}

	// Start periodic bitfield broadcast (every 1s)
	go swarm.StartBitfieldBroadcast(1 * time.Second)

	// Start the scheduler (playback window + rarest-first)
	go sched.Run()

	// Start local HTTP server
	httpServer, err := player.NewServer(store, sched)
	if err != nil {
		log.Fatalf("failed to create HTTP server: %v", err)
	}
	defer httpServer.Close()
	httpServer.Start()
	log.Printf("local HTTP streaming server started at %s", httpServer.URL())

	// Launch mpv
	mpvPlayer, err := player.NewPlayer()
	if err != nil {
		log.Fatalf("failed to create mpv player: %v", err)
	}
	defer mpvPlayer.Close()

	log.Printf("launching mpv player playing from local HTTP server...")
	if err := mpvPlayer.Start(httpServer.URL()); err != nil {
		log.Printf("failed to start mpv player: %v (make sure mpv is installed)", err)
	}

	// Done channel to stop background goroutines on exit
	doneCh := make(chan struct{})
	defer close(doneCh)

	// Start cursor feedback loop: feeds current mpv playback position into the scheduler
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()

		var duration float64
		for {
			select {
			case <-ticker.C:
				if duration == 0 {
					d, err := mpvPlayer.GetDuration()
					if err == nil && d > 0 {
						duration = d
						log.Printf("video duration detected: %.2fs", duration)
					}
				}

				if duration > 0 {
					pos, err := mpvPlayer.GetPlaybackTime()
					if err == nil {
						// Map time to chunk index
						byteOffset := int64((pos / duration) * float64(manifest.FileSize))
						chunkIndex := int(byteOffset / manifest.ChunkSize)
						if chunkIndex >= manifest.ChunkCount {
							chunkIndex = manifest.ChunkCount - 1
						}
						if chunkIndex < 0 {
							chunkIndex = 0
						}
						sched.SetCursor(chunkIndex)
					}
				}
			case <-doneCh:
				return
			}
		}
	}()

	// Start sync manager
	syncMgr := sync.NewSyncManager(swarm, mpvPlayer, false)
	syncMgr.Start()
	defer syncMgr.Stop()

	// Start periodic TCP keepalive broadcast
	go swarm.StartKeepaliveLoop()

	// Start periodic host connection auto-reconnection loop
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if !swarm.HasHostConnection() {
					log.Printf("Host connection lost! Attempting to reconnect to %s...", tok.Host)
					_, err := swarm.ConnectToHost(tok.Host)
					if err != nil {
						log.Printf("Reconnection to host failed: %v", err)
					} else {
						log.Printf("Successfully reconnected to host %s!", tok.Host)
					}
				}
			case <-doneCh:
				return
			}
		}
	}()

	// Start periodic download progress status display
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				pct := float64(store.Count()) / float64(manifest.ChunkCount) * 100
				log.Printf("Status | Progress: %d/%d chunks (%.1f%%) | Connected Peers: %d | In-Flight: %d",
					store.Count(), manifest.ChunkCount, pct, swarm.PeerCount(), sched.InFlightCount())
			case <-doneCh:
				return
			}
		}
	}()

	log.Printf("peer %s | downloading from %d peers...",
		peer.FormatPeerID(selfID), swarm.PeerCount())

	// Block until interrupt or download complete
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("shutting down...")
}
