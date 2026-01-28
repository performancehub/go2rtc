package multistream

import (
	"testing"

	"github.com/AlexxIT/go2rtc/internal/api/ws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSessionLifecycle tests the full session lifecycle without actual WebRTC
func TestSessionLifecycle(t *testing.T) {
	// Create a session (without transport for testing)
	session := NewSession(nil)
	require.NotNil(t, session)

	// Add multiple slots
	for i := 0; i < 6; i++ {
		streamName := ResolveStreamName("camera", "360p")
		slot := NewSlot(i, streamName+string(rune('1'+i)))
		session.AddSlot(slot)
	}

	// Verify all slots were added
	assert.Equal(t, 6, session.SlotCount())

	// Verify each slot can be retrieved
	for i := 0; i < 6; i++ {
		slot := session.GetSlot(i)
		require.NotNil(t, slot)
		assert.Equal(t, i, slot.Index)
	}

	// Close session
	session.Close()
	assert.True(t, session.IsClosed())

	// Slots should still be retrievable after close
	assert.Equal(t, 6, session.SlotCount())
}

// TestSlotSwitchWithoutBind tests switching behavior when not bound
func TestSlotSwitchWithoutBind(t *testing.T) {
	slot := NewSlot(0, "camera1-360p")

	// Switch should fail because stream doesn't exist (not registered)
	err := slot.Switch("camera2-720p")
	assert.Error(t, err)
	assert.Equal(t, ErrStreamNotFound, err)
	assert.Equal(t, StatusError, slot.Status)
}

// TestMultipleSessionManagement tests managing multiple sessions
func TestMultipleSessionManagement(t *testing.T) {
	sessions := make([]*MultiStreamSession, 3)

	for i := 0; i < 3; i++ {
		sessions[i] = NewSession(nil)
		require.NotNil(t, sessions[i])

		// Add different number of slots to each session
		for j := 0; j <= i; j++ {
			sessions[i].AddSlot(NewSlot(j, "camera"+string(rune('1'+j))))
		}
	}

	// Verify each session has correct number of slots
	assert.Equal(t, 1, sessions[0].SlotCount())
	assert.Equal(t, 2, sessions[1].SlotCount())
	assert.Equal(t, 3, sessions[2].SlotCount())

	// Close all sessions
	for _, session := range sessions {
		session.Close()
		assert.True(t, session.IsClosed())
	}
}

// TestConcurrentSlotAccess tests thread-safe slot access
func TestConcurrentSlotAccess(t *testing.T) {
	session := NewSession(nil)

	// Add slots concurrently
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(index int) {
			slot := NewSlot(index, "camera"+string(rune('0'+index)))
			session.AddSlot(slot)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// All slots should be added
	assert.Equal(t, 10, session.SlotCount())
}

// TestConcurrentSlotStatusChange tests thread-safe status changes
func TestConcurrentSlotStatusChange(t *testing.T) {
	slot := NewSlot(0, "camera1")

	done := make(chan bool)

	// Change status concurrently from multiple goroutines
	for i := 0; i < 100; i++ {
		go func(index int) {
			if index%2 == 0 {
				slot.Status = StatusActive
			} else {
				slot.Status = StatusBuffering
			}
			_ = slot.GetStatus() // Read status
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 100; i++ {
		<-done
	}

	// Slot should still be in a valid state
	status := slot.GetStatus()
	assert.True(t, status == StatusActive || status == StatusBuffering)
}

// TestParseMessage tests the message parsing helper
func TestParseMessage(t *testing.T) {
	// Create a mock ws.Message
	msg := &ws.Message{
		Type: "multistream/init",
		Value: map[string]any{
			"type":       "multistream/init",
			"request_id": "test-123",
			"slots": []any{
				map[string]any{"slot": float64(0), "stream": "camera1", "quality": "360p"},
				map[string]any{"slot": float64(1), "stream": "camera2", "quality": "720p"},
			},
		},
	}

	var req InitRequest
	err := parseMessage(msg, &req)
	require.NoError(t, err)

	assert.Equal(t, "multistream/init", req.Type)
	assert.Equal(t, "test-123", req.RequestID)
	assert.Len(t, req.Slots, 2)
	assert.Equal(t, 0, req.Slots[0].Slot)
	assert.Equal(t, "camera1", req.Slots[0].Stream)
	assert.Equal(t, "360p", req.Slots[0].Quality)
}

// TestParseMessageSwitchRequest tests parsing switch requests
func TestParseMessageSwitchRequest(t *testing.T) {
	msg := &ws.Message{
		Type: "multistream/switch",
		Value: map[string]any{
			"type":       "multistream/switch",
			"request_id": "switch-456",
			"slot":       float64(2),
			"stream":     "camera3",
			"quality":    "1080p",
		},
	}

	var req SwitchRequest
	err := parseMessage(msg, &req)
	require.NoError(t, err)

	assert.Equal(t, "multistream/switch", req.Type)
	assert.Equal(t, "switch-456", req.RequestID)
	assert.Equal(t, 2, req.Slot)
	assert.Equal(t, "camera3", req.Stream)
	assert.Equal(t, "1080p", req.Quality)
}

// TestParseMessageOfferRequest tests parsing offer requests
func TestParseMessageOfferRequest(t *testing.T) {
	sdp := "v=0\r\no=- 123 456 IN IP4 0.0.0.0\r\ns=-\r\nt=0 0\r\n"
	msg := &ws.Message{
		Type: "multistream/offer",
		Value: map[string]any{
			"type":       "multistream/offer",
			"request_id": "offer-789",
			"sdp":        sdp,
		},
	}

	var req OfferMessage
	err := parseMessage(msg, &req)
	require.NoError(t, err)

	assert.Equal(t, "multistream/offer", req.Type)
	assert.Equal(t, "offer-789", req.RequestID)
	assert.Equal(t, sdp, req.SDP)
}

// TestParseMessageICE tests parsing ICE candidate messages
func TestParseMessageICE(t *testing.T) {
	candidate := "candidate:1 1 UDP 2013266431 192.168.1.1 50000 typ host"
	msg := &ws.Message{
		Type: "multistream/ice",
		Value: map[string]any{
			"type":      "multistream/ice",
			"candidate": candidate,
		},
	}

	var ice ICEMessage
	err := parseMessage(msg, &ice)
	require.NoError(t, err)

	assert.Equal(t, "multistream/ice", ice.Type)
	assert.Equal(t, candidate, ice.Candidate)
}

// BenchmarkResolveStreamName benchmarks stream name resolution
func BenchmarkResolveStreamName(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = ResolveStreamName("camera1", "360p")
	}
}

// BenchmarkSlotStatusChange benchmarks slot status changes
func BenchmarkSlotStatusChange(b *testing.B) {
	slot := NewSlot(0, "camera1")
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		slot.Status = StatusActive
		_ = slot.GetStatus()
	}
}

// BenchmarkSessionSlotAccess benchmarks slot access in session
func BenchmarkSessionSlotAccess(b *testing.B) {
	session := NewSession(nil)
	for i := 0; i < 9; i++ {
		session.AddSlot(NewSlot(i, "camera"+string(rune('1'+i))))
	}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = session.GetSlot(i % 9)
	}
}
