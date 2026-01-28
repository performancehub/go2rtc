package multistream

import (
	"errors"
	"sync"

	"github.com/AlexxIT/go2rtc/internal/streams"
	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/h264"
	"github.com/AlexxIT/go2rtc/pkg/h265"
	"github.com/AlexxIT/go2rtc/pkg/webrtc"
	pion "github.com/pion/webrtc/v3"
)

// ErrStreamNotFound is returned when a requested stream doesn't exist
var ErrStreamNotFound = errors.New("stream not found")

// Slot represents a single video slot in a multistream session
// Each slot can be bound to a different camera stream and switched dynamically
type Slot struct {
	Index       int                  // Slot index (0-based)
	StreamName  string               // Current stream name (e.g., "camera1-360p")
	Stream      *streams.Stream      // Current stream reference
	Transceiver *pion.RTPTransceiver // WebRTC transceiver for this slot
	LocalTrack  *webrtc.Track        // Local track for sending video
	Consumer    *SlotConsumer        // Consumer implementation for this slot
	Status      string               // Current status

	mu sync.Mutex
}

// SlotConsumer implements core.Consumer interface for a slot
type SlotConsumer struct {
	slot    *Slot
	medias  []*core.Media
	senders []*core.Sender
	stopped bool
	mu      sync.Mutex
}

// NewSlot creates a new slot with the given index and stream name
func NewSlot(index int, streamName string) *Slot {
	return &Slot{
		Index:      index,
		StreamName: streamName,
		Status:     StatusPending,
	}
}

// SetTransceiver sets the WebRTC transceiver for this slot
func (slot *Slot) SetTransceiver(tr *pion.RTPTransceiver) {
	slot.mu.Lock()
	slot.Transceiver = tr
	slot.mu.Unlock()
}

// SetLocalTrack sets the local track for this slot
func (slot *Slot) SetLocalTrack(track *webrtc.Track) {
	slot.mu.Lock()
	slot.LocalTrack = track
	slot.mu.Unlock()
}

// GetStatus returns the current status of the slot
func (slot *Slot) GetStatus() string {
	slot.mu.Lock()
	defer slot.mu.Unlock()
	return slot.Status
}

// GetStreamName returns the current stream name
func (slot *Slot) GetStreamName() string {
	slot.mu.Lock()
	defer slot.mu.Unlock()
	return slot.StreamName
}

// InitConsumer initializes the consumer for this slot
func (slot *Slot) InitConsumer() {
	slot.mu.Lock()
	defer slot.mu.Unlock()

	// Create medias that this consumer wants (video only for now)
	medias := []*core.Media{
		{
			Kind:      core.KindVideo,
			Direction: core.DirectionSendonly,
			Codecs: []*core.Codec{
				{Name: core.CodecH264},
				{Name: core.CodecH265},
			},
		},
	}

	slot.Consumer = &SlotConsumer{
		slot:   slot,
		medias: medias,
	}
}

// Bind connects this slot to a stream source
func (slot *Slot) Bind(stream *streams.Stream) error {
	log.Debug().Int("slot", slot.Index).Str("stream", slot.StreamName).Msg("[multistream] Bind: acquiring lock")
	slot.mu.Lock()
	defer slot.mu.Unlock()
	log.Debug().Int("slot", slot.Index).Str("stream", slot.StreamName).Msg("[multistream] Bind: lock acquired")

	if stream == nil {
		slot.Status = StatusError
		log.Error().Int("slot", slot.Index).Msg("[multistream] Bind: stream is nil")
		return ErrStreamNotFound
	}

	if slot.Consumer == nil {
		slot.Status = StatusError
		log.Error().Int("slot", slot.Index).Msg("[multistream] Bind: consumer not initialized")
		return errors.New("slot consumer not initialized")
	}

	// Add the consumer to the stream
	// NOTE: This can block if the stream needs to start/connect to the source!
	log.Debug().Int("slot", slot.Index).Str("stream", slot.StreamName).Msg("[multistream] Bind: calling stream.AddConsumer (may block)...")
	if err := stream.AddConsumer(slot.Consumer); err != nil {
		slot.Status = StatusError
		log.Error().Err(err).Int("slot", slot.Index).Str("stream", slot.StreamName).Msg("[multistream] Bind: AddConsumer failed")
		return err
	}
	log.Debug().Int("slot", slot.Index).Str("stream", slot.StreamName).Msg("[multistream] Bind: AddConsumer completed")

	slot.Stream = stream
	slot.Status = StatusActive

	log.Info().Int("slot", slot.Index).Str("stream", slot.StreamName).Msg("[multistream] Bind: slot bound to stream successfully")
	return nil
}

// Unbind disconnects the slot from its current stream
func (slot *Slot) Unbind() {
	slot.mu.Lock()
	defer slot.mu.Unlock()

	if slot.Stream != nil && slot.Consumer != nil {
		slot.Stream.RemoveConsumer(slot.Consumer)
		log.Debug().Int("slot", slot.Index).Str("stream", slot.StreamName).Msg("[multistream] slot unbound from stream")
	}

	slot.Stream = nil
	slot.Status = StatusInactive
}

// Switch changes the stream source without WebRTC renegotiation
func (slot *Slot) Switch(newStreamName string) error {
	slot.mu.Lock()
	defer slot.mu.Unlock()

	// Get the new stream
	newStream := streams.Get(newStreamName)
	if newStream == nil {
		slot.Status = StatusError
		return ErrStreamNotFound
	}

	// If same stream, nothing to do
	if slot.StreamName == newStreamName && slot.Stream != nil {
		return nil
	}

	// Unbind from old stream first
	if slot.Stream != nil && slot.Consumer != nil {
		slot.Stream.RemoveConsumer(slot.Consumer)
		log.Debug().Int("slot", slot.Index).Str("stream", slot.StreamName).Msg("[multistream] unbound from old stream")
	}

	// Reset the consumer's senders for the new stream
	slot.Consumer.Reset()

	// Set status to buffering while switching
	slot.Status = StatusBuffering

	// Bind to new stream
	if err := newStream.AddConsumer(slot.Consumer); err != nil {
		slot.Status = StatusError
		log.Error().Err(err).Int("slot", slot.Index).Str("stream", newStreamName).Msg("[multistream] failed to switch stream")
		return err
	}

	slot.Stream = newStream
	slot.StreamName = newStreamName
	slot.Status = StatusActive

	log.Debug().Int("slot", slot.Index).Str("stream", newStreamName).Msg("[multistream] switched to new stream")
	return nil
}

// ToStatus creates a SlotStatus from this slot's current state
func (slot *Slot) ToStatus() SlotStatus {
	slot.mu.Lock()
	defer slot.mu.Unlock()

	return SlotStatus{
		Slot:   slot.Index,
		Stream: slot.StreamName,
		Status: slot.Status,
	}
}

// ============================================================================
// SlotConsumer - implements core.Consumer interface
// ============================================================================

// GetMedias returns the media types this consumer wants
func (c *SlotConsumer) GetMedias() []*core.Media {
	return c.medias
}

// AddTrack is called by the stream to add a track to this consumer
func (c *SlotConsumer) AddTrack(media *core.Media, codec *core.Codec, track *core.Receiver) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stopped {
		return errors.New("consumer stopped")
	}

	localTrack := c.slot.LocalTrack
	if localTrack == nil {
		return errors.New("slot has no local track")
	}

	// Create a sender for this track
	sender := core.NewSender(media, codec)

	// Set up the handler to write RTP packets to the local track
	payloadType := codec.PayloadType
	sender.Handler = func(packet *core.Packet) {
		_ = localTrack.WriteRTP(payloadType, packet)
	}

	// Apply codec-specific transformations
	switch track.Codec.Name {
	case core.CodecH264:
		sender.Handler = h264.RTPPay(1200, sender.Handler)
		if track.Codec.IsRTP() {
			sender.Handler = h264.RTPDepay(track.Codec, sender.Handler)
		} else {
			sender.Handler = h264.RepairAVCC(track.Codec, sender.Handler)
		}

	case core.CodecH265:
		sender.Handler = h265.SafariPay(1200, sender.Handler)
		if track.Codec.IsRTP() {
			sender.Handler = h265.RTPDepay(track.Codec, sender.Handler)
		}
	}

	// Bind the sender to the track receiver
	sender.Bind(track)

	c.senders = append(c.senders, sender)

	log.Debug().
		Int("slot", c.slot.Index).
		Str("codec", codec.Name).
		Msg("[multistream] track added to slot")

	return nil
}

// Stop stops the consumer and closes all senders
func (c *SlotConsumer) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stopped = true

	for _, sender := range c.senders {
		sender.Close()
	}
	c.senders = nil

	return nil
}

// Reset resets the consumer for reuse with a new stream
func (c *SlotConsumer) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Close existing senders
	for _, sender := range c.senders {
		sender.Close()
	}
	c.senders = nil
	c.stopped = false
}

// ============================================================================
// Helper functions
// ============================================================================

// ResolveStreamName combines stream ID and quality suffix
// Examples:
//   - ResolveStreamName("camera1", "") -> "camera1"
//   - ResolveStreamName("camera1", "native") -> "camera1"
//   - ResolveStreamName("camera1", "360p") -> "camera1-360p"
func ResolveStreamName(stream, quality string) string {
	if quality == "" || quality == "native" {
		return stream
	}
	return stream + "-" + quality
}
