package multistream

import (
	"sync"
	"time"

	"github.com/AlexxIT/go2rtc/internal/api/ws"
	"github.com/AlexxIT/go2rtc/pkg/webrtc"
	pion "github.com/pion/webrtc/v3"
)

// Connection state monitoring constants
const (
	// DisconnectedGracePeriod is how long to wait before closing after disconnect
	// WebRTC can briefly disconnect due to network glitches - this prevents premature cleanup
	DisconnectedGracePeriod = 10 * time.Second
)

// MultiStreamSession represents a single multistream WebRTC session
// with multiple video slots that can be dynamically switched
type MultiStreamSession struct {
	tr   *ws.Transport
	pc   *pion.PeerConnection
	conn *webrtc.Conn

	slots map[int]*Slot
	mu    sync.RWMutex

	closed bool

	// Connection monitoring
	disconnectTimer *time.Timer
	lastState       pion.PeerConnectionState
}

// NewSession creates a new multistream session for the given WebSocket transport
func NewSession(tr *ws.Transport) *MultiStreamSession {
	return &MultiStreamSession{
		tr:    tr,
		slots: make(map[int]*Slot),
	}
}

// SetPeerConnection sets the pion PeerConnection for this session
func (s *MultiStreamSession) SetPeerConnection(pc *pion.PeerConnection) {
	s.mu.Lock()
	s.pc = pc
	s.mu.Unlock()
}

// GetPeerConnection returns the pion PeerConnection
func (s *MultiStreamSession) GetPeerConnection() *pion.PeerConnection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pc
}

// SetConn sets the webrtc.Conn wrapper for this session
func (s *MultiStreamSession) SetConn(conn *webrtc.Conn) {
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()
}

// GetConn returns the webrtc.Conn wrapper
func (s *MultiStreamSession) GetConn() *webrtc.Conn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.conn
}

// AddSlot adds a slot to the session
func (s *MultiStreamSession) AddSlot(slot *Slot) {
	s.mu.Lock()
	s.slots[slot.Index] = slot
	s.mu.Unlock()
}

// GetSlot returns a slot by index
func (s *MultiStreamSession) GetSlot(index int) *Slot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.slots[index]
}

// GetSlots returns all slots
func (s *MultiStreamSession) GetSlots() map[int]*Slot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	// Return a copy to avoid race conditions
	result := make(map[int]*Slot, len(s.slots))
	for k, v := range s.slots {
		result[k] = v
	}
	return result
}

// SlotCount returns the number of slots in this session
func (s *MultiStreamSession) SlotCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.slots)
}

// IsClosed returns whether the session is closed
func (s *MultiStreamSession) IsClosed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed
}

// Close closes the session and releases all resources
func (s *MultiStreamSession) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// ALWAYS unbind all slots first, even if session appears closed
	// This handles edge cases where closed flag is set but unbind didn't complete
	// Unbind is idempotent (checks if Stream is nil), so safe to call multiple times
	if len(s.slots) > 0 {
		log.Debug().Int("slots", len(s.slots)).Msg("[multistream] unbinding all slots in parallel")

		// Unbind ALL slots in parallel with a timeout
		// This prevents one stuck FFmpeg from blocking cleanup of other slots
		var wg sync.WaitGroup
		unbindTimeout := 5 * time.Second

		for idx, slot := range s.slots {
			wg.Add(1)
			go func(slotIdx int, sl *Slot) {
				defer wg.Done()

				log.Debug().Int("slot", slotIdx).Str("stream", sl.StreamName).Msg("[multistream] unbinding slot")

				// Run unbind with timeout to prevent blocking on stuck FFmpeg
				done := make(chan struct{})
				go func() {
					sl.Unbind()
					close(done)
				}()

				select {
				case <-done:
					log.Debug().Int("slot", slotIdx).Msg("[multistream] slot unbind completed")
				case <-time.After(unbindTimeout):
					log.Warn().Int("slot", slotIdx).Dur("timeout", unbindTimeout).Msg("[multistream] slot unbind timed out, forcing cleanup")
					// Force cleanup even if Unbind is stuck
					sl.mu.Lock()
					sl.bindingCancelled = true
					sl.Stream = nil
					sl.Status = StatusInactive
					if sl.Consumer != nil {
						sl.Consumer.Stop()
					}
					sl.mu.Unlock()
				}
			}(idx, slot)
		}

		// Wait for all unbinds to complete (with their individual timeouts)
		wg.Wait()
		log.Debug().Msg("[multistream] all slots unbound")

		// Clear slots map to prevent double-unbind on subsequent Close() calls
		s.slots = make(map[int]*Slot)
	}

	if s.closed {
		log.Debug().Msg("[multistream] session already closed, skipping remaining cleanup")
		return
	}
	s.closed = true

	// Cancel any pending disconnect timer
	if s.disconnectTimer != nil {
		s.disconnectTimer.Stop()
		s.disconnectTimer = nil
	}

	// Close peer connection
	if s.pc != nil {
		log.Debug().Msg("[multistream] closing peer connection")
		_ = s.pc.Close()
		s.pc = nil
	}

	s.conn = nil

	log.Info().Msg("[multistream] session closed and all resources released")
}

// Transport returns the WebSocket transport for this session
func (s *MultiStreamSession) Transport() *ws.Transport {
	return s.tr
}

// WriteMessage sends a message through the WebSocket transport
func (s *MultiStreamSession) WriteMessage(msg *ws.Message) {
	if s.tr != nil {
		s.tr.Write(msg)
	}
}

// HandleConnectionStateChange processes WebRTC peer connection state changes
// and triggers cleanup when the connection is lost.
//
// State flow:
// - "connected" -> cancel any pending disconnect timer
// - "disconnected" -> start grace period timer (network may recover)
// - "failed" -> immediate cleanup (connection unrecoverable)
// - "closed" -> immediate cleanup
func (s *MultiStreamSession) HandleConnectionStateChange(state pion.PeerConnectionState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Don't process if already closed
	if s.closed {
		return
	}

	// Track state for logging
	prevState := s.lastState
	s.lastState = state

	log.Debug().
		Str("prev_state", prevState.String()).
		Str("new_state", state.String()).
		Msg("[multistream] connection state changed")

	switch state {
	case pion.PeerConnectionStateConnected:
		// Connection healthy - cancel any pending disconnect timer
		if s.disconnectTimer != nil {
			s.disconnectTimer.Stop()
			s.disconnectTimer = nil
			log.Info().Msg("[multistream] connection recovered, cancelled disconnect timer")
		}

	case pion.PeerConnectionStateDisconnected:
		// Connection temporarily lost - start grace period
		// This happens during brief network glitches; connection may recover
		if s.disconnectTimer == nil {
			log.Warn().
				Dur("grace_period", DisconnectedGracePeriod).
				Msg("[multistream] connection disconnected, starting grace period timer")

			s.disconnectTimer = time.AfterFunc(DisconnectedGracePeriod, func() {
				log.Warn().Msg("[multistream] grace period expired, closing session due to disconnection")
				s.closeFromMonitor()
			})
		}

	case pion.PeerConnectionStateFailed:
		// Connection failed (ICE failed, unrecoverable) - immediate cleanup
		log.Warn().Msg("[multistream] connection failed, closing session immediately")
		if s.disconnectTimer != nil {
			s.disconnectTimer.Stop()
			s.disconnectTimer = nil
		}
		// Release lock before calling closeFromMonitor (it will acquire its own lock)
		s.mu.Unlock()
		s.closeFromMonitor()
		s.mu.Lock() // Re-acquire for defer

	case pion.PeerConnectionStateClosed:
		// Connection closed (by us or peer) - cleanup if not already done
		log.Debug().Msg("[multistream] peer connection closed")
		if s.disconnectTimer != nil {
			s.disconnectTimer.Stop()
			s.disconnectTimer = nil
		}
	}
}

// closeFromMonitor is called when the connection monitor detects the connection is lost
// It removes the session from the global sessions map and closes it
func (s *MultiStreamSession) closeFromMonitor() {
	// Remove from global sessions map
	sessionsMu.Lock()
	if _, ok := sessions[s.tr]; ok {
		delete(sessions, s.tr)
		log.Debug().Msg("[multistream] session removed from map by connection monitor")
	}
	sessionsMu.Unlock()

	// Close the session (stops all transcoding)
	s.Close()

	// Notify client that session was closed due to connection loss
	if s.tr != nil {
		s.tr.Write(&ws.Message{
			Type: "multistream/disconnected",
			Value: map[string]any{
				"reason": "connection_lost",
			},
		})
	}
}
