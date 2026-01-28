package multistream

import (
	"github.com/AlexxIT/go2rtc/internal/api/ws"
	"github.com/AlexxIT/go2rtc/internal/app"
	"github.com/rs/zerolog"
)

var log zerolog.Logger

// Config holds multistream configuration options
type Config struct {
	MaxSlots int `yaml:"max_slots"` // Default: 9
}

var cfg Config

// Init initializes the multistream module
func Init() {
	cfg.MaxSlots = 9 // sensible default for typical grid layouts

	var c struct {
		Mod Config `yaml:"multistream"`
	}
	app.LoadConfig(&c)
	if c.Mod.MaxSlots > 0 {
		cfg.MaxSlots = c.Mod.MaxSlots
	}

	log = app.GetLogger("multistream")

	// Register WebSocket handlers
	ws.HandleFunc("multistream/init", handleInit)
	ws.HandleFunc("multistream/offer", handleOffer)
	ws.HandleFunc("multistream/switch", handleSwitch)
	ws.HandleFunc("multistream/ice", handleICE)
	ws.HandleFunc("multistream/close", handleClose)

	log.Info().Int("max_slots", cfg.MaxSlots).Msg("[multistream] initialized")
}

// GetMaxSlots returns the maximum number of slots allowed per session
func GetMaxSlots() int {
	return cfg.MaxSlots
}
