package multistream

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/AlexxIT/go2rtc/internal/api/ws"
	"github.com/AlexxIT/go2rtc/internal/streams"
	intWebrtc "github.com/AlexxIT/go2rtc/internal/webrtc"
	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/webrtc"
	pion "github.com/pion/webrtc/v3"
)

var (
	// sessions maps WebSocket transports to their multistream sessions
	sessions   = make(map[*ws.Transport]*MultiStreamSession)
	sessionsMu sync.RWMutex
)

// getOrCreateSession retrieves an existing session or creates a new one
func getOrCreateSession(tr *ws.Transport) *MultiStreamSession {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()

	if s, ok := sessions[tr]; ok {
		return s
	}

	s := NewSession(tr)
	sessions[tr] = s

	// Clean up session when WebSocket closes
	tr.OnClose(func() {
		sessionsMu.Lock()
		delete(sessions, tr)
		sessionsMu.Unlock()
		s.Close()
		log.Debug().Msg("[multistream] session cleaned up on WebSocket close")
	})

	return s
}

// getSession retrieves an existing session (returns nil if not found)
func getSession(tr *ws.Transport) *MultiStreamSession {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	return sessions[tr]
}

// handleInit handles the multistream/init message
// This creates the PeerConnection and transceivers for each requested slot
func handleInit(tr *ws.Transport, msg *ws.Message) error {
	log.Debug().Msg("[multistream] handleInit: start")

	var req InitRequest
	if err := parseMessage(msg, &req); err != nil {
		log.Error().Err(err).Msg("[multistream] handleInit: failed to parse message")
		return err
	}

	log.Info().
		Str("request_id", req.RequestID).
		Int("slots", len(req.Slots)).
		Msg("[multistream] handleInit: request parsed")

	if len(req.Slots) > cfg.MaxSlots {
		return errors.New("too many slots requested")
	}

	if len(req.Slots) == 0 {
		return errors.New("no slots requested")
	}

	session := getOrCreateSession(tr)

	// Check if session already has a peer connection
	if session.GetPeerConnection() != nil {
		return errors.New("session already initialized")
	}

	// Create peer connection using the existing webrtc module
	pc, err := intWebrtc.PeerConnection(false)
	if err != nil {
		log.Error().Err(err).Msg("[multistream] failed to create peer connection")
		return err
	}
	session.SetPeerConnection(pc)

	// Track how many slots were successfully created
	slotsCreated := 0

	// Create transceivers for each requested slot
	for _, slotReq := range req.Slots {
		streamName := ResolveStreamName(slotReq.Stream, slotReq.Quality)

		// Verify stream exists
		stream := streams.Get(streamName)
		if stream == nil {
			log.Warn().
				Str("stream", streamName).
				Int("slot", slotReq.Slot).
				Msg("[multistream] stream not found, skipping slot")
			continue
		}

		// Create a local track for this slot with UNIQUE track ID
		// Each track must have a unique msid to avoid Chrome "Duplicate a=msid lines" error
		trackID := fmt.Sprintf("video-slot%d", slotReq.Slot)
		localTrack := webrtc.NewTrackWithID("video", trackID, "go2rtc")

		// Create sendonly transceiver with the local track
		transceiver, err := pc.AddTransceiverFromTrack(localTrack,
			pion.RTPTransceiverInit{Direction: pion.RTPTransceiverDirectionSendonly})
		if err != nil {
			log.Error().Err(err).Int("slot", slotReq.Slot).Msg("[multistream] failed to add transceiver")
			continue
		}

		// Create the slot
		slot := NewSlot(slotReq.Slot, streamName)
		slot.SetTransceiver(transceiver)
		slot.SetLocalTrack(localTrack)
		slot.InitConsumer() // Initialize the consumer for this slot

		session.AddSlot(slot)
		slotsCreated++

		log.Debug().
			Int("slot", slotReq.Slot).
			Str("stream", streamName).
			Msg("[multistream] slot created")
	}

	if slotsCreated == 0 {
		_ = pc.Close()
		return errors.New("no valid slots could be created")
	}

	// Send ready message - client should now send offer
	tr.Write(&ws.Message{
		Type: "multistream/ready",
		Value: map[string]any{
			"request_id": req.RequestID,
			"slots":      slotsCreated,
		},
	})

	log.Debug().
		Str("request_id", req.RequestID).
		Int("slots", slotsCreated).
		Msg("[multistream] session ready")

	return nil
}

// handleOffer handles the multistream/offer message
// This processes the client's SDP offer and binds slots to streams
func handleOffer(tr *ws.Transport, msg *ws.Message) error {
	log.Debug().Msg("[multistream] handleOffer: start")

	var req OfferMessage
	if err := parseMessage(msg, &req); err != nil {
		log.Error().Err(err).Msg("[multistream] handleOffer: failed to parse message")
		return err
	}

	log.Debug().
		Str("request_id", req.RequestID).
		Int("sdp_len", len(req.SDP)).
		Msg("[multistream] handleOffer: offer parsed")

	session := getSession(tr)
	if session == nil {
		log.Error().Msg("[multistream] handleOffer: no active session")
		return errors.New("no active session")
	}
	log.Debug().Msg("[multistream] handleOffer: session found")

	pc := session.GetPeerConnection()
	if pc == nil {
		log.Error().Msg("[multistream] handleOffer: session not initialized")
		return errors.New("session not initialized, call init first")
	}
	log.Debug().Msg("[multistream] handleOffer: peer connection found")

	// Create webrtc.Conn wrapper for ICE handling (but don't use SetOffer!)
	log.Debug().Msg("[multistream] handleOffer: creating webrtc.Conn wrapper")
	conn := webrtc.NewConn(pc)
	conn.Mode = core.ModePassiveConsumer
	conn.Protocol = "ws"
	conn.UserAgent = tr.Request.UserAgent()
	session.SetConn(conn)
	log.Debug().Msg("[multistream] handleOffer: webrtc.Conn wrapper created")

	// Set remote offer DIRECTLY on PeerConnection
	// DON'T use conn.SetOffer() - it creates new transceivers that overwrite our unique track IDs!
	log.Debug().Msg("[multistream] handleOffer: setting remote description directly on PC...")
	desc := pion.SessionDescription{Type: pion.SDPTypeOffer, SDP: req.SDP}
	if err := pc.SetRemoteDescription(desc); err != nil {
		log.Error().Err(err).Msg("[multistream] handleOffer: failed to set remote description")
		return err
	}
	log.Debug().Msg("[multistream] handleOffer: remote description set successfully")

	// Generate answer FIRST, before binding streams
	log.Debug().Msg("[multistream] handleOffer: creating answer...")
	answerDesc, err := pc.CreateAnswer(nil)
	if err != nil {
		log.Error().Err(err).Msg("[multistream] handleOffer: failed to create answer")
		return err
	}
	if err := pc.SetLocalDescription(answerDesc); err != nil {
		log.Error().Err(err).Msg("[multistream] handleOffer: failed to set local description")
		return err
	}

	// Get the sanitized SDP answer (removes duplicate msid attributes)
	answer := webrtc.SanitizeSDP(pc.LocalDescription().SDP)
	log.Debug().Int("answer_len", len(answer)).Msg("[multistream] handleOffer: answer generated")

	// Get slots for status reporting
	slots := session.GetSlots()

	// Build initial slot statuses (all pending/buffering)
	slotStatuses := make([]SlotStatus, 0, len(slots))
	for _, slot := range slots {
		slotStatuses = append(slotStatuses, SlotStatus{
			Slot:   slot.Index,
			Stream: slot.StreamName,
			Status: StatusBuffering, // Will update to active once bound
		})
	}

	// SEND ANSWER IMMEDIATELY - don't wait for stream binding!
	log.Debug().Msg("[multistream] handleOffer: sending answer to client IMMEDIATELY")
	tr.Write(&ws.Message{
		Type: "multistream/answer",
		Value: AnswerMessage{
			Type:      "multistream/answer",
			RequestID: req.RequestID,
			SDP:       answer,
			Slots:     slotStatuses,
		},
	})

	// Set up ICE candidate handling and connection state monitoring
	// This enables automatic cleanup when the WebRTC connection is lost
	setupICEHandler(tr, conn, session)

	log.Info().
		Str("request_id", req.RequestID).
		Int("total_slots", len(slots)).
		Msg("[multistream] handleOffer: answer sent, now binding streams asynchronously")

	// Bind streams ASYNCHRONOUSLY and IN PARALLEL
	// Each slot binds in its own goroutine for faster startup (5s vs 16s for 5 slots)
	go func() {
		var wg sync.WaitGroup
		const bindTimeout = 15 * time.Second // Timeout for each slot binding attempt
		const maxRetries = 3                  // Maximum binding attempts per slot
		const retryDelay = 2 * time.Second    // Delay between retries

		for _, slot := range slots {
			wg.Add(1)
			go func(s *Slot) {
				defer wg.Done()

				log.Debug().Int("slot", s.Index).Str("stream", s.StreamName).Msg("[multistream] parallel: looking up stream")

				stream := streams.Get(s.StreamName)
				if stream == nil {
					log.Warn().Int("slot", s.Index).Str("stream", s.StreamName).Msg("[multistream] parallel: stream not found")
					sendSlotStatus(tr, s.Index, s.StreamName, StatusError, "stream not found")
					return
				}

				// Try binding with retries
				for attempt := 1; attempt <= maxRetries; attempt++ {
					log.Debug().Int("slot", s.Index).Str("stream", s.StreamName).Int("attempt", attempt).Msg("[multistream] parallel: binding slot...")

					// Bind slot to stream with timeout (FFmpeg startup can hang)
					bindDone := make(chan error, 1)
					go func() {
						bindDone <- s.Bind(stream)
					}()

					select {
				case err := <-bindDone:
					if err != nil {
						log.Error().Err(err).Int("slot", s.Index).Str("stream", s.StreamName).Int("attempt", attempt).Str("error", err.Error()).Msg("[multistream] parallel: bind failed")
						if attempt < maxRetries {
							// Clean up before retry - Reset() allows consumer reuse
							log.Debug().Int("slot", s.Index).Int("attempt", attempt).Msg("[multistream] parallel: cleaning up before retry")
							s.Unbind()
							time.Sleep(retryDelay)
							continue
						}
						log.Error().Int("slot", s.Index).Str("stream", s.StreamName).Str("error", err.Error()).Msg("[multistream] parallel: all retry attempts failed")
						sendSlotStatus(tr, s.Index, s.StreamName, StatusError, err.Error())
						return
					}
					log.Info().Int("slot", s.Index).Str("stream", s.StreamName).Int("attempt", attempt).Msg("[multistream] parallel: slot bound successfully")
					sendSlotStatus(tr, s.Index, s.StreamName, StatusActive, "")
					return // Success, exit retry loop

					case <-time.After(bindTimeout):
						log.Warn().Int("slot", s.Index).Str("stream", s.StreamName).Int("attempt", attempt).Dur("timeout", bindTimeout).Msg("[multistream] parallel: binding timed out")

						// Clean up the stuck slot before retry or final error
						log.Debug().Int("slot", s.Index).Msg("[multistream] parallel: calling Unbind to cleanup")
						s.Unbind()
						log.Debug().Int("slot", s.Index).Msg("[multistream] parallel: Unbind completed")

						if attempt < maxRetries {
							log.Info().Int("slot", s.Index).Str("stream", s.StreamName).Int("next_attempt", attempt+1).Msg("[multistream] parallel: retrying after timeout")
							time.Sleep(retryDelay)
							log.Debug().Int("slot", s.Index).Int("attempt", attempt+1).Msg("[multistream] parallel: starting retry attempt")
							continue
						}
						log.Error().Int("slot", s.Index).Str("stream", s.StreamName).Msg("[multistream] parallel: all retry attempts exhausted")
						sendSlotStatus(tr, s.Index, s.StreamName, StatusError, "binding timeout after retries")
					}
				}
			}(slot)
		}

		// Wait for all slots to finish binding (success, failure, or timeout)
		wg.Wait()
		log.Info().Int("total_slots", len(slots)).Msg("[multistream] parallel: all slots binding complete")
	}()

	return nil
}

// sendSlotStatus sends a status update for a single slot
func sendSlotStatus(tr *ws.Transport, slotIndex int, streamName, status, errMsg string) {
	tr.Write(&ws.Message{
		Type: "multistream/status",
		Value: StatusUpdate{
			Type: "multistream/status",
			Slot: slotIndex,
			Status: SlotStatus{
				Slot:   slotIndex,
				Stream: streamName,
				Status: status,
				Error:  errMsg,
			},
		},
	})
}

// handleSwitch handles the multistream/switch message
// This switches a slot to a different stream without WebRTC renegotiation
func handleSwitch(tr *ws.Transport, msg *ws.Message) error {
	var req SwitchRequest
	if err := parseMessage(msg, &req); err != nil {
		return err
	}

	log.Debug().
		Str("request_id", req.RequestID).
		Int("slot", req.Slot).
		Str("stream", req.Stream).
		Str("quality", req.Quality).
		Msg("[multistream] switch request")

	session := getSession(tr)
	if session == nil {
		return errors.New("no active session")
	}

	slot := session.GetSlot(req.Slot)
	if slot == nil {
		sendSwitchError(tr, req.Slot, req.Stream, req.Quality, "invalid slot index")
		return nil
	}

	newStreamName := ResolveStreamName(req.Stream, req.Quality)

	// Switch the slot to new stream (no WebRTC renegotiation!)
	if err := slot.Switch(newStreamName); err != nil {
		sendSwitchError(tr, req.Slot, newStreamName, req.Quality, err.Error())
		return nil
	}

	// Send success status
	tr.Write(&ws.Message{
		Type: "multistream/status",
		Value: StatusUpdate{
			Type: "multistream/status",
			Slot: req.Slot,
			Status: SlotStatus{
				Slot:   req.Slot,
				Stream: newStreamName,
				Status: StatusActive,
			},
		},
	})

	log.Debug().
		Int("slot", req.Slot).
		Str("stream", newStreamName).
		Msg("[multistream] switch completed")

	return nil
}

// sendSwitchError sends an error status for a switch operation
func sendSwitchError(tr *ws.Transport, slot int, stream, quality, errMsg string) {
	tr.Write(&ws.Message{
		Type: "multistream/status",
		Value: StatusUpdate{
			Type: "multistream/status",
			Slot: slot,
			Status: SlotStatus{
				Slot:   slot,
				Stream: stream,
				Status: StatusError,
				Error:  errMsg,
			},
		},
	})
}

// handleICE handles the multistream/ice message
// This processes ICE candidates from the client
func handleICE(tr *ws.Transport, msg *ws.Message) error {
	var ice ICEMessage
	if err := parseMessage(msg, &ice); err != nil {
		return err
	}

	session := getSession(tr)
	if session == nil {
		return errors.New("no active session")
	}

	conn := session.GetConn()
	if conn == nil {
		return errors.New("session not connected")
	}

	log.Trace().Str("candidate", ice.Candidate).Msg("[multistream] received ICE candidate")

	return conn.AddCandidate(ice.Candidate)
}

// handleClose handles the multistream/close message
// This gracefully closes the session
func handleClose(tr *ws.Transport, msg *ws.Message) error {
	log.Info().Msg("[multistream] close request received")

	sessionsMu.Lock()
	session, ok := sessions[tr]
	if ok {
		delete(sessions, tr)
		log.Debug().Msg("[multistream] session removed from sessions map")
	}
	sessionsMu.Unlock()

	if session != nil {
		slotCount := session.SlotCount()
		log.Info().Int("slots", slotCount).Msg("[multistream] closing session with slots")
		session.Close()
		log.Info().Msg("[multistream] session closed successfully")
	} else {
		log.Warn().Msg("[multistream] close request but no session found")
	}

	return nil
}

// handlePause handles the multistream/pause message
// This pauses a slot (stops transcoding but keeps the slot allocated)
// Note: If other consumers are viewing the same stream, transcoding continues for them
func handlePause(tr *ws.Transport, msg *ws.Message) error {
	var req PauseRequest
	if err := parseMessage(msg, &req); err != nil {
		return err
	}

	log.Debug().
		Str("request_id", req.RequestID).
		Int("slot", req.Slot).
		Msg("[multistream] pause request")

	session := getSession(tr)
	if session == nil {
		return errors.New("no active session")
	}

	slot := session.GetSlot(req.Slot)
	if slot == nil {
		sendSlotStatus(tr, req.Slot, "", StatusError, "invalid slot index")
		return nil
	}

	// Unbind the slot from its stream
	// This calls stream.RemoveConsumer() which only stops the producer
	// if there are no other consumers viewing the same stream
	slot.Unbind()

	// Update status to paused
	slot.mu.Lock()
	slot.Status = StatusPaused
	streamName := slot.StreamName
	slot.mu.Unlock()

	// Send status update
	sendSlotStatus(tr, req.Slot, streamName, StatusPaused, "")

	log.Debug().
		Int("slot", req.Slot).
		Str("stream", streamName).
		Msg("[multistream] slot paused")

	return nil
}

// handleKeyframe handles the multistream/keyframe message
// This requests a keyframe (I-frame) for a slot
// Note: Actual keyframe request support depends on the producer type
func handleKeyframe(tr *ws.Transport, msg *ws.Message) error {
	var req KeyframeRequest
	if err := parseMessage(msg, &req); err != nil {
		return err
	}

	log.Debug().
		Str("request_id", req.RequestID).
		Int("slot", req.Slot).
		Msg("[multistream] keyframe request")

	session := getSession(tr)
	if session == nil {
		return errors.New("no active session")
	}

	// If slot is -1, request keyframe for all slots
	if req.Slot == -1 {
		slots := session.GetSlots()
		for _, slot := range slots {
			requestKeyframeForSlot(slot)
		}
		log.Debug().Int("slots", len(slots)).Msg("[multistream] keyframe requested for all slots")
	} else {
		slot := session.GetSlot(req.Slot)
		if slot == nil {
			return errors.New("invalid slot index")
		}
		requestKeyframeForSlot(slot)
	}

	// Send acknowledgment
	tr.Write(&ws.Message{
		Type: "multistream/keyframe",
		Value: map[string]any{
			"request_id": req.RequestID,
			"slot":       req.Slot,
			"status":     "requested",
		},
	})

	return nil
}

// requestKeyframeForSlot attempts to request a keyframe from the slot's producer
// This is a best-effort operation - not all producers support keyframe requests
func requestKeyframeForSlot(slot *Slot) {
	slot.mu.Lock()
	defer slot.mu.Unlock()

	if slot.Stream == nil {
		log.Debug().Int("slot", slot.Index).Msg("[multistream] cannot request keyframe - slot not bound")
		return
	}

	// TODO: Implement actual keyframe request to producer
	// For RTSP sources, this would send an RTCP PLI packet
	// For FFmpeg sources, this is not directly supported
	log.Debug().Int("slot", slot.Index).Str("stream", slot.StreamName).Msg("[multistream] keyframe request logged (producer support varies)")
}

// handleResume handles the multistream/resume message
// This resumes a paused slot by rebinding it to a stream
func handleResume(tr *ws.Transport, msg *ws.Message) error {
	var req ResumeRequest
	if err := parseMessage(msg, &req); err != nil {
		return err
	}

	log.Debug().
		Str("request_id", req.RequestID).
		Int("slot", req.Slot).
		Str("stream", req.Stream).
		Str("quality", req.Quality).
		Msg("[multistream] resume request")

	session := getSession(tr)
	if session == nil {
		return errors.New("no active session")
	}

	slot := session.GetSlot(req.Slot)
	if slot == nil {
		sendSlotStatus(tr, req.Slot, req.Stream, StatusError, "invalid slot index")
		return nil
	}

	// Resolve the stream name with quality suffix
	streamName := ResolveStreamName(req.Stream, req.Quality)

	// Get the stream
	stream := streams.Get(streamName)
	if stream == nil {
		sendSlotStatus(tr, req.Slot, streamName, StatusError, "stream not found")
		return nil
	}

	// Update the slot's stream name
	slot.mu.Lock()
	slot.StreamName = streamName
	slot.Status = StatusBuffering
	slot.mu.Unlock()

	// Send buffering status immediately
	sendSlotStatus(tr, req.Slot, streamName, StatusBuffering, "")

	// Bind asynchronously (can block while producer starts)
	go func() {
		// Reset the consumer for reuse
		if slot.Consumer != nil {
			slot.Consumer.Reset()
		}

		if err := slot.Bind(stream); err != nil {
			log.Error().Err(err).Int("slot", req.Slot).Str("stream", streamName).Msg("[multistream] failed to resume slot")
			sendSlotStatus(tr, req.Slot, streamName, StatusError, err.Error())
			return
		}

		sendSlotStatus(tr, req.Slot, streamName, StatusActive, "")

		log.Debug().
			Int("slot", req.Slot).
			Str("stream", streamName).
			Msg("[multistream] slot resumed")
	}()

	return nil
}

// setupICEHandler sets up ICE candidate handling and connection state monitoring
func setupICEHandler(tr *ws.Transport, conn *webrtc.Conn, session *MultiStreamSession) {
	conn.Listen(func(msg any) {
		switch msg := msg.(type) {
		case *pion.ICECandidate:
			if !intWebrtc.FilterCandidate(msg) {
				return
			}
			candidate := msg.ToJSON().Candidate
			log.Trace().Str("candidate", candidate).Msg("[multistream] sending ICE candidate")
			tr.Write(&ws.Message{
				Type:  "multistream/ice",
				Value: candidate,
			})

		case pion.PeerConnectionState:
			// Handle connection state changes for idle detection
			// This triggers cleanup when the WebRTC connection is lost
			session.HandleConnectionStateChange(msg)
		}
	})
}

// parseMessage parses a ws.Message value into the given struct
func parseMessage(msg *ws.Message, v any) error {
	data, err := json.Marshal(msg.Value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// countActiveSlots counts slots with active status
func countActiveSlots(statuses []SlotStatus) int {
	count := 0
	for _, s := range statuses {
		if s.Status == StatusActive {
			count++
		}
	}
	return count
}
