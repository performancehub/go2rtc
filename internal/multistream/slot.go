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
	// Validate inputs without holding lock during blocking operations
	log.Debug().Int("slot", slot.Index).Str("stream", slot.StreamName).Msg("[multistream] Bind: starting")

	if stream == nil {
		slot.mu.Lock()
		slot.Status = StatusError
		slot.mu.Unlock()
		log.Error().Int("slot", slot.Index).Msg("[multistream] Bind: stream is nil")
		return ErrStreamNotFound
	}

	slot.mu.Lock()
	consumer := slot.Consumer
	slot.mu.Unlock()

	if consumer == nil {
		slot.mu.Lock()
		slot.Status = StatusError
		slot.mu.Unlock()
		log.Error().Int("slot", slot.Index).Msg("[multistream] Bind: consumer not initialized")
		return errors.New("slot consumer not initialized")
	}

	// Add the consumer to the stream
	// NOTE: This can block if the stream needs to start/connect to the source!
	// IMPORTANT: Do NOT hold slot.mu during this call to avoid deadlock with Unbind()
	log.Debug().Int("slot", slot.Index).Str("stream", slot.StreamName).Msg("[multistream] Bind: calling stream.AddConsumer (may block)...")
	if err := stream.AddConsumer(consumer); err != nil {
		slot.mu.Lock()
		slot.Status = StatusError
		slot.mu.Unlock()
		log.Error().Err(err).Int("slot", slot.Index).Str("stream", slot.StreamName).Msg("[multistream] Bind: AddConsumer failed")
		return err
	}
	log.Debug().Int("slot", slot.Index).Str("stream", slot.StreamName).Msg("[multistream] Bind: AddConsumer completed")

	// Now acquire lock to update state
	slot.mu.Lock()
	slot.Stream = stream
	slot.Status = StatusActive
	slot.mu.Unlock()

	log.Info().Int("slot", slot.Index).Str("stream", slot.StreamName).Msg("[multistream] Bind: slot bound to stream successfully")
	return nil
}

// Unbind disconnects the slot from its current stream
func (slot *Slot) Unbind() {
	slot.mu.Lock()
	defer slot.mu.Unlock()

	if slot.Stream != nil && slot.Consumer != nil {
		// Remove consumer from stream first
		slot.Stream.RemoveConsumer(slot.Consumer)
		log.Debug().Int("slot", slot.Index).Str("stream", slot.StreamName).Msg("[multistream] slot unbound from stream")
	}

	// Reset the consumer to close all senders and prepare for reuse
	// Using Reset() instead of Stop() so the consumer can be reused for retries
	// Reset() closes senders AND sets stopped=false so AddTrack will work again
	if slot.Consumer != nil {
		slot.Consumer.Reset()
		log.Debug().Int("slot", slot.Index).Msg("[multistream] slot consumer reset")
	}

	slot.Stream = nil
	slot.Status = StatusInactive
}

// Switch changes the stream source without WebRTC renegotiation
func (slot *Slot) Switch(newStreamName string) error {
	log.Debug().Int("slot", slot.Index).Str("new_stream", newStreamName).Msg("[multistream] Switch: starting")

	// Get the new stream (no lock needed for stream lookup)
	newStream := streams.Get(newStreamName)
	if newStream == nil {
		slot.mu.Lock()
		slot.Status = StatusError
		slot.mu.Unlock()
		log.Error().Int("slot", slot.Index).Str("stream", newStreamName).Msg("[multistream] Switch: stream not found")
		return ErrStreamNotFound
	}

	// Check if same stream (need lock to read current state)
	slot.mu.Lock()
	if slot.StreamName == newStreamName && slot.Stream != nil {
		slot.mu.Unlock()
		log.Debug().Int("slot", slot.Index).Str("stream", newStreamName).Msg("[multistream] Switch: already on this stream")
		return nil
	}

	// Unbind from old stream first
	if slot.Stream != nil && slot.Consumer != nil {
		slot.Stream.RemoveConsumer(slot.Consumer)
		log.Debug().Int("slot", slot.Index).Str("stream", slot.StreamName).Msg("[multistream] Switch: unbound from old stream")
	}

	// Reset the consumer's senders for the new stream
	consumer := slot.Consumer
	if consumer != nil {
		consumer.Reset()
	}

	// Set status to buffering while switching
	slot.Status = StatusBuffering
	slot.mu.Unlock()

	// Bind to new stream - DO NOT hold lock during this blocking call!
	log.Debug().Int("slot", slot.Index).Str("stream", newStreamName).Msg("[multistream] Switch: binding to new stream (may block)...")
	if err := newStream.AddConsumer(consumer); err != nil {
		slot.mu.Lock()
		slot.Status = StatusError
		slot.mu.Unlock()
		log.Error().Err(err).Int("slot", slot.Index).Str("stream", newStreamName).Msg("[multistream] Switch: failed to bind to new stream")
		return err
	}

	// Update state after successful bind
	slot.mu.Lock()
	slot.Stream = newStream
	slot.StreamName = newStreamName
	slot.Status = StatusActive
	slot.mu.Unlock()

	log.Info().Int("slot", slot.Index).Str("stream", newStreamName).Msg("[multistream] Switch: successfully switched to new stream")
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

	transceiver := c.slot.Transceiver
	if transceiver == nil {
		return errors.New("slot has no transceiver")
	}

	// Get the negotiated payload type from the transceiver's codec parameters
	// This is what the browser expects to receive, not the source stream's payload type
	var payloadType uint8 = 96 // Default H264 dynamic payload type
	if params := transceiver.Sender().GetParameters(); len(params.Codecs) > 0 {
		payloadType = uint8(params.Codecs[0].PayloadType)
	}

	log.Debug().
		Int("slot", c.slot.Index).
		Uint8("payload_type", payloadType).
		Str("codec", codec.Name).
		Msg("[multistream] using negotiated payload type")

	// Create a sender for this track
	sender := core.NewSender(media, codec)

	// Set up the handler to write RTP packets to the local track
	// Add packet counting for debugging
	var packetCount uint64
	sender.Handler = func(packet *core.Packet) {
		packetCount++
		if packetCount == 1 {
			log.Debug().Int("slot", c.slot.Index).Msg("[multistream] first RTP packet sent to WebRTC")
		} else if packetCount%500 == 0 {
			log.Debug().Int("slot", c.slot.Index).Uint64("packets", packetCount).Msg("[multistream] RTP packet count")
		}
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

	// Start the sender - this is CRITICAL!
	// The standard webrtc.Conn calls sender.Start() when connection becomes "connected",
	// but our senders aren't in conn.Senders, so we must start them manually.
	// Start() launches the goroutine that reads from the buffer and outputs packets.
	sender.Start()

	c.senders = append(c.senders, sender)

	log.Debug().
		Int("slot", c.slot.Index).
		Str("codec", codec.Name).
		Msg("[multistream] track added to slot and sender started")

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
