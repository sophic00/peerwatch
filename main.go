package main

import (
	"fmt"
	"os"

	"github.com/sophic00/peerwatch.git/cmd"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "start":
		cmd.Start(os.Args[2:])
	case "join":
		cmd.Join(os.Args[2:])
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `peerwatch — P2P watch party

Usage:
  peerwatch start [flags] <file>    Host a watch party with a local video file
  peerwatch join  [flags] <token>   Join a watch party using a connection token

Examples:
  peerwatch start movie.mp4
  peerwatch join pw_eyJoIjoiMTkyLj...

Flags (start):
  -port int        Port to listen on (default 9876)
  -chunk-size int  Chunk size in bytes (default 524288)

Run 'peerwatch <command> -h' for command-specific help.
`)
}
