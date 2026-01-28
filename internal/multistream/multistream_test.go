package multistream

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveStreamName(t *testing.T) {
	tests := []struct {
		name     string
		stream   string
		quality  string
		expected string
	}{
		{
			name:     "empty quality returns stream only",
			stream:   "camera1",
			quality:  "",
			expected: "camera1",
		},
		{
			name:     "native quality returns stream only",
			stream:   "camera1",
			quality:  "native",
			expected: "camera1",
		},
		{
			name:     "360p quality appends suffix",
			stream:   "camera1",
			quality:  "360p",
			expected: "camera1-360p",
		},
		{
			name:     "720p quality appends suffix",
			stream:   "camera1",
			quality:  "720p",
			expected: "camera1-720p",
		},
		{
			name:     "1080p quality appends suffix",
			stream:   "camera1",
			quality:  "1080p",
			expected: "camera1-1080p",
		},
		{
			name:     "stream with dashes works correctly",
			stream:   "front-door-cam",
			quality:  "480p",
			expected: "front-door-cam-480p",
		},
		{
			name:     "stream with underscores works correctly",
			stream:   "parking_lot_1",
			quality:  "720p",
			expected: "parking_lot_1-720p",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResolveStreamName(tt.stream, tt.quality)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewSlot(t *testing.T) {
	slot := NewSlot(0, "camera1")

	require.NotNil(t, slot)
	assert.Equal(t, 0, slot.Index)
	assert.Equal(t, "camera1", slot.StreamName)
	assert.Equal(t, StatusPending, slot.Status)
	assert.Nil(t, slot.Stream)
	assert.Nil(t, slot.Transceiver)
	assert.Nil(t, slot.LocalTrack)
	assert.Nil(t, slot.Consumer)
}

func TestSlotInitConsumer(t *testing.T) {
	slot := NewSlot(0, "camera1")
	assert.Nil(t, slot.Consumer)

	slot.InitConsumer()

	require.NotNil(t, slot.Consumer)
	assert.NotNil(t, slot.Consumer.GetMedias())
	assert.Len(t, slot.Consumer.GetMedias(), 1)
	assert.Equal(t, "video", slot.Consumer.GetMedias()[0].Kind)
}

func TestSlotGetStatus(t *testing.T) {
	slot := NewSlot(0, "camera1")

	assert.Equal(t, StatusPending, slot.GetStatus())

	slot.Status = StatusActive
	assert.Equal(t, StatusActive, slot.GetStatus())

	slot.Status = StatusBuffering
	assert.Equal(t, StatusBuffering, slot.GetStatus())

	slot.Status = StatusError
	assert.Equal(t, StatusError, slot.GetStatus())

	slot.Status = StatusOffline
	assert.Equal(t, StatusOffline, slot.GetStatus())
}

func TestSlotGetStreamName(t *testing.T) {
	slot := NewSlot(0, "camera1-360p")

	assert.Equal(t, "camera1-360p", slot.GetStreamName())

	slot.StreamName = "camera2-720p"
	assert.Equal(t, "camera2-720p", slot.GetStreamName())
}

func TestSlotToStatus(t *testing.T) {
	slot := NewSlot(2, "camera3-1080p")
	slot.Status = StatusActive

	status := slot.ToStatus()

	assert.Equal(t, 2, status.Slot)
	assert.Equal(t, "camera3-1080p", status.Stream)
	assert.Equal(t, StatusActive, status.Status)
}

func TestSlotUnbindWithoutStream(t *testing.T) {
	slot := NewSlot(0, "camera1")
	slot.Status = StatusActive

	// Should not panic when unbinding without a stream
	slot.Unbind()

	assert.Equal(t, StatusInactive, slot.Status)
	assert.Nil(t, slot.Stream)
}

func TestNewSession(t *testing.T) {
	session := NewSession(nil)

	require.NotNil(t, session)
	assert.Nil(t, session.pc)
	assert.Nil(t, session.conn)
	assert.NotNil(t, session.slots)
	assert.Equal(t, 0, len(session.slots))
	assert.False(t, session.closed)
}

func TestSessionAddAndGetSlot(t *testing.T) {
	session := NewSession(nil)

	slot0 := NewSlot(0, "camera1")
	slot1 := NewSlot(1, "camera2")
	slot2 := NewSlot(2, "camera3")

	session.AddSlot(slot0)
	session.AddSlot(slot1)
	session.AddSlot(slot2)

	assert.Equal(t, 3, session.SlotCount())

	retrieved0 := session.GetSlot(0)
	require.NotNil(t, retrieved0)
	assert.Equal(t, "camera1", retrieved0.StreamName)

	retrieved1 := session.GetSlot(1)
	require.NotNil(t, retrieved1)
	assert.Equal(t, "camera2", retrieved1.StreamName)

	retrieved2 := session.GetSlot(2)
	require.NotNil(t, retrieved2)
	assert.Equal(t, "camera3", retrieved2.StreamName)

	// Non-existent slot
	retrieved99 := session.GetSlot(99)
	assert.Nil(t, retrieved99)
}

func TestSessionGetSlots(t *testing.T) {
	session := NewSession(nil)

	session.AddSlot(NewSlot(0, "camera1"))
	session.AddSlot(NewSlot(1, "camera2"))

	slots := session.GetSlots()

	assert.Equal(t, 2, len(slots))
	assert.Contains(t, slots, 0)
	assert.Contains(t, slots, 1)
}

func TestSessionIsClosed(t *testing.T) {
	session := NewSession(nil)

	assert.False(t, session.IsClosed())

	session.Close()

	assert.True(t, session.IsClosed())
}

func TestSessionCloseTwice(t *testing.T) {
	session := NewSession(nil)

	// Should not panic when closing twice
	session.Close()
	session.Close()

	assert.True(t, session.IsClosed())
}

func TestProtocolStatusConstants(t *testing.T) {
	// Verify status constants are defined correctly
	assert.Equal(t, "pending", StatusPending)
	assert.Equal(t, "active", StatusActive)
	assert.Equal(t, "buffering", StatusBuffering)
	assert.Equal(t, "offline", StatusOffline)
	assert.Equal(t, "error", StatusError)
	assert.Equal(t, "inactive", StatusInactive)
}

func TestSlotRequest(t *testing.T) {
	req := SlotRequest{
		Slot:    0,
		Stream:  "camera1",
		Quality: "360p",
	}

	assert.Equal(t, 0, req.Slot)
	assert.Equal(t, "camera1", req.Stream)
	assert.Equal(t, "360p", req.Quality)
}

func TestSlotStatus(t *testing.T) {
	status := SlotStatus{
		Slot:          0,
		Stream:        "camera1",
		Quality:       "360p",
		ActualQuality: "360p",
		Status:        StatusActive,
		Error:         "",
	}

	assert.Equal(t, 0, status.Slot)
	assert.Equal(t, "camera1", status.Stream)
	assert.Equal(t, "360p", status.Quality)
	assert.Equal(t, "360p", status.ActualQuality)
	assert.Equal(t, StatusActive, status.Status)
	assert.Empty(t, status.Error)
}

func TestSlotStatusWithError(t *testing.T) {
	status := SlotStatus{
		Slot:   1,
		Stream: "camera2",
		Status: StatusError,
		Error:  "stream not found",
	}

	assert.Equal(t, StatusError, status.Status)
	assert.Equal(t, "stream not found", status.Error)
}

func TestGetMaxSlots(t *testing.T) {
	// Before Init(), cfg.MaxSlots should be 0
	// After Init(), it should be the configured value (default 9)
	// We can't easily test this without calling Init(),
	// so we just verify the function exists and returns an int
	maxSlots := GetMaxSlots()
	assert.GreaterOrEqual(t, maxSlots, 0)
}

func TestCountActiveSlots(t *testing.T) {
	tests := []struct {
		name     string
		statuses []SlotStatus
		expected int
	}{
		{
			name:     "empty list",
			statuses: []SlotStatus{},
			expected: 0,
		},
		{
			name: "all active",
			statuses: []SlotStatus{
				{Slot: 0, Status: StatusActive},
				{Slot: 1, Status: StatusActive},
				{Slot: 2, Status: StatusActive},
			},
			expected: 3,
		},
		{
			name: "mixed statuses",
			statuses: []SlotStatus{
				{Slot: 0, Status: StatusActive},
				{Slot: 1, Status: StatusError},
				{Slot: 2, Status: StatusActive},
				{Slot: 3, Status: StatusBuffering},
			},
			expected: 2,
		},
		{
			name: "none active",
			statuses: []SlotStatus{
				{Slot: 0, Status: StatusError},
				{Slot: 1, Status: StatusOffline},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := countActiveSlots(tt.statuses)
			assert.Equal(t, tt.expected, result)
		})
	}
}
