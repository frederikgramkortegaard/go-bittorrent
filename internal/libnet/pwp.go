// Peer wire Protocol
// https://wiki.theory.org/BitTorrentSpecification#Peer_wire_protocol_.28TCP.29
package libnet

import (
	"fmt"
	"io"
	"net"
	"time"
)

// EstablishNewConnection creates a TCP connection to a peer.
// Returns the connection address and net.Conn to be used in PeerConnection.
func EstablishNewConnection(address string) (string, net.Conn, error) {

	d := net.Dialer{Timeout: 5 * time.Second}

	conn, err := d.Dial("tcp", address)
	if err != nil {
		return "", nil, err
	}

	return address, conn, nil
}

// SendHandshakeToPeer sends a BitTorrent handshake message to a peer and reads the response.
// ourID is OUR peer ID that we send to introduce ourselves (not the remote peer's ID)
func SendHandshakeToPeer(peer *PeerConnection, ourID [20]byte, infoHash [20]byte) ([]byte, error) {

	handshakeData := NewHandshakeMessage(ourID, infoHash)

	fmt.Printf("Sending handshake to %s (%d bytes)\n", peer.ConnectionAddress, len(handshakeData))
	fmt.Printf("  Our ID: %s\n", string(ourID[:]))
	fmt.Printf("  Info hash: %x\n", infoHash[:8]) // first 8 bytes for brevity

	// Send handshake message over raw TCP connection
	n, err := peer.Connection.Write(handshakeData)
	if err != nil {
		fmt.Println("Error sending handshake:", err)
		return nil, err
	}
	fmt.Printf("Sent %d bytes\n", n)

	// Read handshake response (68 bytes total)
	// 1 byte pstrlen + 19 bytes pstr + 8 bytes reserved + 20 bytes info_hash + 20 bytes peer_id = 68 bytes
	responseBuffer := make([]byte, 68)
	fmt.Printf("Waiting for handshake response from %s...\n", peer.ConnectionAddress)
	n, err = io.ReadFull(peer.Connection, responseBuffer)
	if err != nil {
		fmt.Printf("Error reading handshake response (read %d bytes): %v\n", n, err)
		return nil, err
	}

	fmt.Printf("Received handshake response from %s (%d bytes)\n", peer.ConnectionAddress, n)
	fmt.Printf("\tPeer protocol: %s\n", string(responseBuffer[1:20]))
	fmt.Printf("\tPeer info hash: %x\n", responseBuffer[28:36]) // first 8 bytes
	fmt.Printf("\tPeer ID: %s\n", string(responseBuffer[48:68]))

	return responseBuffer, nil
}
