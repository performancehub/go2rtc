package multistream

import (
	"encoding/json"
	"errors"
	"sync"

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
	var req InitRequest
	if err := parseMessage(msg, &req); err != nil {
		return err
	}

	log.Debug().
		Str("request_id", req.RequestID).
		Int("slots", len(req.Slots)).
		Msg("[multistream] init request")

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

		// Create a local track for this slot
		localTrack := webrtc.NewTrack("video")

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
	var req OfferMessage
	if err := parseMessage(msg, &req); err != nil {
		return err
	}

	log.Debug().
		Str("request_id", req.RequestID).
		Msg("[multistream] offer received")

	session := getSession(tr)
	if session == nil {
		return errors.New("no active session")
	}

	pc := session.GetPeerConnection()
	if pc == nil {
		return errors.New("session not initialized, call init first")
	}

	// Create webrtc.Conn wrapper
	conn := webrtc.NewConn(pc)
	conn.Mode = core.ModePassiveConsumer
	conn.Protocol = "ws"
	conn.UserAgent = tr.Request.UserAgent()
	session.SetConn(conn)

	// Set remote offer
	if err := conn.SetOffer(req.SDP); err != nil {
		log.Error().Err(err).Msg("[multistream] failed to set offer")
		return err
	}

	// Bind each slot to its stream
	slots := session.GetSlots()
	slotStatuses := make([]SlotStatus, 0, len(slots))

	for _, slot := range slots {
		stream := streams.Get(slot.StreamName)
		if stream == nil {
			slotStatuses = append(slotStatuses, SlotStatus{
				Slot:   slot.Index,
				Stream: slot.StreamName,
				Status: StatusError,
				Error:  "stream not found",
			})
			continue
		}

		// Bind slot to stream (the slot's consumer handles track setup)
		if err := slot.Bind(stream); err != nil {
			slotStatuses = append(slotStatuses, SlotStatus{
				Slot:   slot.Index,
				Stream: slot.StreamName,
				Status: StatusError,
				Error:  err.Error(),
			})
			continue
		}

		slotStatuses = append(slotStatuses, SlotStatus{
			Slot:   slot.Index,
			Stream: slot.StreamName,
			Status: StatusActive,
		})
	}

	// Generate and send answer
	answer, err := conn.GetAnswer()
	if err != nil {
		log.Error().Err(err).Msg("[multistream] failed to create answer")
		return err
	}

	tr.Write(&ws.Message{
		Type: "multistream/answer",
		Value: AnswerMessage{
			Type:      "multistream/answer",
			RequestID: req.RequestID,
			SDP:       answer,
			Slots:     slotStatuses,
		},
	})

	// Set up ICE candidate handling
	setupICEHandler(tr, conn)

	log.Debug().
		Str("request_id", req.RequestID).
		Int("active_slots", countActiveSlots(slotStatuses)).
		Msg("[multistream] answer sent")

	return nil
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
	log.Debug().Msg("[multistream] close request")

	sessionsMu.Lock()
	session, ok := sessions[tr]
	if ok {
		delete(sessions, tr)
	}
	sessionsMu.Unlock()

	if session != nil {
		session.Close()
	}

	return nil
}

// setupICEHandler sets up ICE candidate handling for the connection
func setupICEHandler(tr *ws.Transport, conn *webrtc.Conn) {
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
			log.Debug().Str("state", msg.String()).Msg("[multistream] peer connection state changed")
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
