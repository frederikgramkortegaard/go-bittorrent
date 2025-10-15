package config

import "time"

const (
	// Block size for piece downloads (16KB is standard)
	BlockSize int64 = 16384

	// Maximum number of pending block requests per peer (pipelining)
	MaxPipelineDepth int = 5

	// Timeout for block requests
	BlockRequestTimeout time.Duration = 30 * time.Second

	// How often to check for new blocks to request
	WriteLoopInterval time.Duration = 100 * time.Millisecond

	// Connection timeout when establishing TCP connection
	ConnectionTimeout time.Duration = 5 * time.Second

	// Maximum number of timeouts before marking peer as failed
	MaxTimeouts int = 10
)
