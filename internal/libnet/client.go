package libnet

import (
	"crypto/rand"
	"log"
	"net"
)

type Client struct {
	ID       string
	Listener net.Listener
}

func NewClient() *Client {

	// Create a peer ID (must be exactly 20 bytes)
	// Format: -XX0000-YYYYYYYYYYYY where XX=client ID, 0000=version, Y=random
	// Example: -GO0001-123456789012
	peerID := "-GO0001-"

	// Add 12 random alphanumeric characters
	randomBytes := make([]byte, 12)
	_, err := rand.Read(randomBytes)
	if err != nil {
		panic(err)
	}

	// Convert to alphanumeric
	chars := "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	for _, b := range randomBytes {
		peerID += string(chars[int(b)%len(chars)])
	}

	log.Println("Created client with ID:", peerID, "len:", len(peerID))

	// Setup TCP
	ln, err := net.Listen("tcp", ":6881") // standard BitTorrent port
	if err != nil {
		log.Fatal(err)
	}

	return &Client{
		ID: peerID,
		Listener: ln,
	}
}

func (c *Client) HandleConnection() {

}
