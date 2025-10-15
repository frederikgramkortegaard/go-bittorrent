package libnet

import (
	"gotorrent/internal/bencoding"
	"net"
	"time"
)

// PeerConnection tracks the connection state with a peer.
type PeerConnection struct {
	// Peer information
	Peer *bencoding.PeerStruct

	// TCP connection
	ConnectionAddress string
	Connection        net.Conn

	// Connection lifecycle
	Status PeerConnectionStatus
	Error  error // Error that caused failure (if Status == StatusFailed)

	// BitTorrent protocol state
	AmChoking      bool // This client is choking the peer
	AmInterested   bool // This client is interested in the peer
	PeerChoking    bool // Peer is choking this client
	PeerInterested bool // Peer is interested in this client

	// Piece availability
	Bitfield []byte // Bitfield of pieces this peer has

	// Data Tracking
	PendingBlockRequests []*BlockRequest

	// Statistics
	LastSeen        time.Time
	BytesUploaded   uint64
	BytesDownloaded uint64
}

// BlockRequest represents a pending request for a block of data.
type BlockRequest struct {
	PieceIndex  int
	BlockIndex  int
	Begin       int64 // Offset within piece in bytes
	Length      int64 // Block length in bytes
	RequestedAt time.Time
}

// HasPiece checks if the peer has a specific piece based on their bitfield.
func (pc *PeerConnection) HasPiece(pieceIndex int) bool {
	if pc.Bitfield == nil {
		return false
	}

	byteIndex := pieceIndex / 8
	bitOffset := pieceIndex % 8

	if byteIndex >= len(pc.Bitfield) {
		return false
	}

	return (pc.Bitfield[byteIndex]>>(7-bitOffset))&1 == 1
}
