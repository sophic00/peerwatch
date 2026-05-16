package cmd

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/sophic00/peerwatch.git/internal/token"
)

// Join implements the "peerwatch join <token>" command.
// It decodes the connection token and connects to the host.
func Join(args []string) {
	fs := flag.NewFlagSet("join", flag.ExitOnError)
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

	// TODO(phase2): Connect to host via TCP
	// TODO(phase2): Receive manifest and peer list
	// TODO(phase3): Create peer store and start scheduler
	// TODO(phase4): Start local HTTP server and launch mpv
	// TODO(phase5): Start sync loop

	log.Println("connecting... (networking not yet implemented)")
}
