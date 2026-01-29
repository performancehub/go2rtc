package multistream

import (
	"sync"

	"github.com/AlexxIT/go2rtc/internal/api/ws"
	"github.com/AlexxIT/go2rtc/pkg/webrtc"
	pion "github.com/pion/webrtc/v3"
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

	if s.closed {
		log.Debug().Msg("[multistream] session already closed, skipping")
		return
	}
	s.closed = true

	// Unbind all slots from streams - this stops transcoding for each consumer
	log.Debug().Int("slots", len(s.slots)).Msg("[multistream] unbinding all slots")
	for idx, slot := range s.slots {
		log.Debug().Int("slot", idx).Str("stream", slot.StreamName).Msg("[multistream] unbinding slot")
		slot.Unbind()
	}
	log.Debug().Msg("[multistream] all slots unbound")

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
