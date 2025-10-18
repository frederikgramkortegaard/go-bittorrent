package libnet

import (
	"crypto/rand"
	"log"
	"net"
)

type Client struct {
	ID       [20]byte // Our peer ID (exactly 20 bytes)
	Listener net.Listener
}

// NewClient creates a new BitTorrent client with a unique peer ID and listening port.
func NewClient(peerID [20]byte) (*Client, error) {

	// Create a peer ID (must be exactly 20 bytes)
	// Format: -XX0000-YYYYYYYYYYYY where XX=client ID, 0000=version, Y=random
	// Example: -GO0001-123456789012
	if peerID == [20]byte{} {
		prefix := "-GO0001-" // 8 bytes
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

	log.Println("Created client with ID:", string(peerID[:]), "len:", len(peerID))

	// Setup TCP listener
	ln, err := net.Listen("tcp", ":6881") // standard BitTorrent port
	if err != nil {
		return nil, err
	}

	return &Client{
		ID:       peerID,
		Listener: ln,
	}, nil
}
