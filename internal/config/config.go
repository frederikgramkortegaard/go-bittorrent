package config

import "time"

// Config holds all configuration for the BitTorrent client
type Config struct {
	// Paths
	OutputDir string

	// Network
	ListenPort int
	ClientID   string
	UserAgent  string

	// Download behavior
	BlockSize           int32
	RequestPipelineSize int
	MaxPeersPerTorrent  int
	CompactMode         bool
	DiskWriteQueueSize  int

	// Timeouts
	PeerReadTimeout        time.Duration
	TrackerTimeout         time.Duration
	DownloadLoopInterval   time.Duration
	CompletionPollInterval time.Duration
	PeerHealthCheckInterval time.Duration

	// Logging
	LogLevel string
}

// DefaultConfig returns a Config with sensible default values
func DefaultConfig() *Config {
	return &Config{
		// Paths
		OutputDir: ".",

		// Network
		ListenPort: 6881,
		ClientID:   "-GO0001-",
		UserAgent:  "go-bittorrent/0.1",

		// Download behavior
		BlockSize:           16384,
		RequestPipelineSize: 5,
		MaxPeersPerTorrent:  50,
		CompactMode:         false,
		DiskWriteQueueSize:  100,

		// Timeouts
		PeerReadTimeout:         3 * time.Minute, // Must be > 2 minutes (BitTorrent keep-alive interval)
		TrackerTimeout:          30 * time.Second,
		DownloadLoopInterval:    500 * time.Millisecond,
		CompletionPollInterval:  1 * time.Second,
		PeerHealthCheckInterval: 5 * time.Minute,

		// Logging
		LogLevel: "info",
	}
}
