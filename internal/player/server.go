package player

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/sophic00/peerwatch.git/internal/chunk"
	"github.com/sophic00/peerwatch.git/internal/scheduler"
)

// Server is a local HTTP server that streams the video file from the chunk store.
type Server struct {
	store     *chunk.Store
	scheduler *scheduler.Scheduler
	listener  net.Listener
	port      int
	server    *http.Server
}

// NewServer creates a new local HTTP server binding to a random port on 127.0.0.1.
func NewServer(store *chunk.Store, scheduler *scheduler.Scheduler) (*Server, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to listen on localhost: %w", err)
	}

	return &Server{
		store:     store,
		scheduler: scheduler,
		listener:  l,
		port:      l.Addr().(*net.TCPAddr).Port,
	}, nil
}

// Port returns the port the server is listening on.
func (s *Server) Port() int {
	return s.port
}

// URL returns the local streaming URL for the video.
func (s *Server) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/video", s.port)
}

// Start starts the HTTP server in a background goroutine.
func (s *Server) Start() {
	mux := http.NewServeMux()
	mux.HandleFunc("/video", func(w http.ResponseWriter, r *http.Request) {
		// Set headers for video streaming
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Accept-Ranges", "bytes")

		rs := &demandReadSeeker{
			store:     s.store,
			scheduler: s.scheduler,
			fileSize:  s.store.Manifest().FileSize,
			chunkSize: s.store.Manifest().ChunkSize,
		}

		// http.ServeContent automatically handles Range headers and partial requests
		http.ServeContent(w, r, s.store.Manifest().FileName, time.Time{}, rs)
	})

	s.server = &http.Server{
		Handler: mux,
	}

	go func() {
		if err := s.server.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()
}

// Close gracefully stops the HTTP server.
func (s *Server) Close() error {
	if s.server != nil {
		s.server.Close()
	}
	return s.listener.Close()
}

// demandReadSeeker wraps the chunk store and blocks reads on chunks
// after raising their demand priority in the scheduler.
type demandReadSeeker struct {
	store     *chunk.Store
	scheduler *scheduler.Scheduler
	offset    int64
	fileSize  int64
	chunkSize int64
}

// Read implements io.Reader.
func (d *demandReadSeeker) Read(p []byte) (n int, err error) {
	if d.offset >= d.fileSize {
		return 0, io.EOF
	}

	length := int64(len(p))
	if d.offset+length > d.fileSize {
		length = d.fileSize - d.offset
	}

	if length <= 0 {
		return 0, io.EOF
	}

	// Calculate which chunks cover the requested range
	startChunk := int(d.offset / d.chunkSize)
	endChunk := int((d.offset + length - 1) / d.chunkSize)

	// Notify the scheduler of urgent chunk demands
	// (only if we don't already have them locally and the scheduler is present)
	if d.scheduler != nil {
		for i := startChunk; i <= endChunk; i++ {
			if !d.store.HasChunk(i) {
				d.scheduler.Demand(i)
			}
		}
	}

	// Read from the store. This will block until the required chunks are written.
	data, err := d.store.ReadBytes(d.offset, length)
	if err != nil {
		return 0, err
	}

	n = copy(p, data)
	d.offset += int64(n)
	return n, nil
}

// Seek implements io.Seeker.
func (d *demandReadSeeker) Seek(offset int64, whence int) (int64, error) {
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = d.offset + offset
	case io.SeekEnd:
		next = d.fileSize + offset
	default:
		return 0, fmt.Errorf("invalid whence: %d", whence)
	}

	if next < 0 {
		return 0, fmt.Errorf("negative seek position: %d", next)
	}
	d.offset = next
	return d.offset, nil
}
