package libnet

import (
	"crypto/rand"
	"fmt"
	"go-bittorrent/internal/config"
	"go-bittorrent/internal/logger"
	"net"
)

type Client struct {
	ID       [20]byte // Our peer ID (exactly 20 bytes)
	Listener net.Listener
	Logger   *logger.Logger
	Config   *config.Config
}

// NewClient creates a new BitTorrent client with a unique peer ID and listening port.
func NewClient(cfg *config.Config, peerID [20]byte) (*Client, error) {

	// Create a peer ID (must be exactly 20 bytes)
	// Format: -XX0000-YYYYYYYYYYYY where XX=client ID, 0000=version, Y=random
	// Example: -GO0001-123456789012
	if peerID == [20]byte{} {
		prefix := cfg.ClientID
		if len(prefix) != 8 {
			return nil, fmt.Errorf("client ID must be exactly 8 bytes, got %d", len(prefix))
		}
		copy(peerID[:], prefix)
		// Add 12 random alphanumeric characters
		randomBytes := make([]byte, 12)
		_, err := rand.Read(randomBytes)
		if err != nil {
			return nil, err
		}

		// Convert to alphanumeric
		chars := "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
		for i, b := range randomBytes {
			peerID[8+i] = chars[int(b)%len(chars)]
		}
	}

	// Setup TCP listener
	listenAddr := fmt.Sprintf(":%d", cfg.ListenPort)
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, err
	}

	client := &Client{
		ID:       peerID,
		Listener: ln,
		Config:   cfg,
	}

	// Create logger for this client
	client.Logger = logger.New().WithPrefix("Client")
	client.Logger.Info("Created client with ID: %s on port %d", string(peerID[:]), cfg.ListenPort)

	return client, nil
}
