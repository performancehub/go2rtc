package multistream

// Client -> Server messages

// InitRequest is sent by the client to initialize a multistream session
type InitRequest struct {
	Type      string        `json:"type"`       // "multistream/init"
	RequestID string        `json:"request_id"` // Unique request identifier
	Slots     []SlotRequest `json:"slots"`      // Requested stream slots
}

// SlotRequest defines a single slot configuration
type SlotRequest struct {
	Slot    int    `json:"slot"`              // Slot index (0-based)
	Stream  string `json:"stream"`            // Stream name/ID
	Quality string `json:"quality,omitempty"` // Optional quality suffix (e.g., "360p", "720p")
}

// SwitchRequest is sent to switch a slot to a different stream
type SwitchRequest struct {
	Type      string `json:"type"`              // "multistream/switch"
	RequestID string `json:"request_id"`        // Unique request identifier
	Slot      int    `json:"slot"`              // Slot index to switch
	Stream    string `json:"stream"`            // New stream name/ID
	Quality   string `json:"quality,omitempty"` // Optional quality suffix
}

// OfferMessage contains the WebRTC SDP offer from the client
type OfferMessage struct {
	Type      string `json:"type"`       // "multistream/offer"
	RequestID string `json:"request_id"` // Unique request identifier
	SDP       string `json:"sdp"`        // SDP offer string
}

// ICEMessage contains an ICE candidate
type ICEMessage struct {
	Type      string `json:"type"`      // "multistream/ice"
	Candidate string `json:"candidate"` // ICE candidate string
}

// Server -> Client messages

// ReadyMessage is sent when the session is initialized and ready for offer
type ReadyMessage struct {
	Type      string `json:"type"`       // "multistream/ready"
	RequestID string `json:"request_id"` // Request ID from init
	Slots     int    `json:"slots"`      // Number of slots created
}

// AnswerMessage contains the WebRTC SDP answer from the server
type AnswerMessage struct {
	Type      string       `json:"type"`       // "multistream/answer"
	RequestID string       `json:"request_id"` // Request ID from offer
	SDP       string       `json:"sdp"`        // SDP answer string
	Slots     []SlotStatus `json:"slots"`      // Status of each slot
}

// SlotStatus represents the current state of a slot
type SlotStatus struct {
	Slot          int    `json:"slot"`                     // Slot index
	Stream        string `json:"stream"`                   // Stream name
	Quality       string `json:"quality,omitempty"`        // Requested quality
	ActualQuality string `json:"actual_quality,omitempty"` // Actual quality (may differ if unavailable)
	Status        string `json:"status"`                   // "active", "buffering", "offline", "error"
	Error         string `json:"error,omitempty"`          // Error message if status is "error"
}

// StatusUpdate is sent when a slot's status changes
type StatusUpdate struct {
	Type   string     `json:"type"`   // "multistream/status"
	Slot   int        `json:"slot"`   // Slot index
	Status SlotStatus `json:"status"` // New status
}

// ErrorMessage is sent when an error occurs
type ErrorMessage struct {
	Type      string `json:"type"`                // "multistream/error"
	RequestID string `json:"request_id,omitempty"` // Request ID if applicable
	Error     string `json:"error"`               // Error message
}

// Status constants
const (
	StatusPending   = "pending"
	StatusActive    = "active"
	StatusBuffering = "buffering"
	StatusOffline   = "offline"
	StatusError     = "error"
	StatusInactive  = "inactive"
)
