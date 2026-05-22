package sync

import (
	"log"
	"math"
	"time"

	"github.com/sophic00/peerwatch.git/internal/peer"
	"github.com/sophic00/peerwatch.git/internal/player"
	"github.com/sophic00/peerwatch.git/internal/protocol"
)

// SyncManager coordinates playback synchronization across the swarm.
type SyncManager struct {
	swarm  *peer.Swarm
	player *player.Player
	isHost bool
	done   chan struct{}
}

// NewSyncManager creates a new SyncManager.
func NewSyncManager(swarm *peer.Swarm, p *player.Player, isHost bool) *SyncManager {
	return &SyncManager{
		swarm:  swarm,
		player: p,
		isHost: isHost,
		done:   make(chan struct{}),
	}
}

// Start activates the synchronization manager.
func (sm *SyncManager) Start() {
	if sm.isHost {
		go sm.hostLoop()
	} else {
		sm.swarm.OnSyncReceived = func(msg *protocol.SyncMsg) {
			sm.handleSync(msg)
		}
	}
}

// Stop deactivates the synchronization manager and releases resources.
func (sm *SyncManager) Stop() {
	select {
	case <-sm.done:
	default:
		close(sm.done)
	}
}

func (sm *SyncManager) hostLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			pos, err := sm.player.GetPlaybackTime()
			if err != nil {
				continue // Player not ready or loading, skip
			}

			paused, err := sm.player.IsPaused()
			if err != nil {
				continue
			}

			state := protocol.StatePlaying
			if paused {
				state = protocol.StatePaused
			}

			msg := &protocol.SyncMsg{
				PlaybackTime: pos,
				State:        state,
				UnixMs:       time.Now().UnixNano() / int64(time.Millisecond),
			}

			sm.swarm.Broadcast(msg)
		case <-sm.done:
			return
		}
	}
}

func (sm *SyncManager) handleSync(msg *protocol.SyncMsg) {
	paused, err := sm.player.IsPaused()
	if err != nil {
		return
	}

	hostPaused := (msg.State == protocol.StatePaused)

	// 1. Synchronize Pause/Play State
	if hostPaused && !paused {
		log.Printf("sync: host is paused | pausing local playback and seeking to %.2fs", msg.PlaybackTime)
		sm.player.SetPaused(true)
		sm.player.Seek(msg.PlaybackTime)
		sm.player.SetSpeed(1.0) // Reset speed to normal on pause
		return
	} else if !hostPaused && paused {
		log.Printf("sync: host has resumed | resuming local playback")
		sm.player.SetPaused(false)
	}

	// If host is paused, we don't perform continuous drift corrections
	if hostPaused {
		return
	}

	// 2. Continuous Playback Drift Correction
	pos, err := sm.player.GetPlaybackTime()
	if err != nil {
		return
	}

	// Estimate the network transit delay if system clocks are reasonably in-sync
	nowMs := time.Now().UnixNano() / int64(time.Millisecond)
	latency := float64(nowMs-msg.UnixMs) / 1000.0
	if latency < 0 || latency > 5.0 {
		latency = 0 // Clocks wildly out of sync, default to 0 latency offset
	}

	targetPos := msg.PlaybackTime + latency
	drift := pos - targetPos

	// 3. Three-tier synchronization correction
	if math.Abs(drift) > 2.0 {
		// Tier 3: Hard absolute seek
		log.Printf("sync: drift too high (%.2fs) | performing hard seek to %.2fs", drift, targetPos)
		sm.player.Seek(targetPos)
		sm.player.SetSpeed(1.0)
	} else if drift < -0.5 {
		// Tier 2a: Speed up to 1.05x to catch up
		log.Printf("sync: behind by %.2fs | speeding up to 1.05x", -drift)
		sm.player.SetSpeed(1.05)
	} else if drift > 0.5 {
		// Tier 2b: Slow down to 0.95x to let host catch up
		log.Printf("sync: ahead by %.2fs | slowing down to 0.95x", drift)
		sm.player.SetSpeed(0.95)
	} else {
		// Tier 1: Within tolerance window, reset speed to normal 1.0x
		sm.player.SetSpeed(1.0)
	}
}
