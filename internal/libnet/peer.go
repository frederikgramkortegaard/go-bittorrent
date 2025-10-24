package libnet

import (
	"fmt"
	"go-bittorrent/internal/bencoding"
	"go-bittorrent/internal/logger"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// PeerConnection tracks the connection state with a peer.
type PeerConnection struct {
	// Peer information
	Peer *bencoding.PeerStruct

	// TCP connection
	ConnectionAddress string
	Connection        net.Conn

	// Connection lifecycle (using atomic for lock-free access)
	Status atomic.Value // PeerConnectionStatus stored atomically
	Error  error        // Error that caused failure (if Status == StatusFailed)

	// BitTorrent protocol state (using atomics for lock-free access)
	AmChoking      atomic.Bool // This client is choking the peer (atomic for concurrent access)
	AmInterested   atomic.Bool // This client is interested in the peer (atomic for concurrent access)
	PeerChoking    atomic.Bool // Peer is choking this client (atomic for read-heavy access)
	PeerInterested atomic.Bool // Peer is interested in this client

	// Piece availability (protected by mutex for concurrent updates via HAVE messages)
	bitfieldMu sync.RWMutex
	Bitfield   []byte // Bitfield of pieces this peer has

	// Request pipelining (buffered channel for up to 5 concurrent requests)
	RequestChan chan struct{}

	// Statistics
	LastSeen        time.Time
	BytesUploaded   uint64
	BytesDownloaded uint64

	// Logger
	Logger *logger.Logger
}

// String implements fmt.Stringer for PeerConnection
func (pc *PeerConnection) String() string {
	return fmt.Sprintf("Peer:%s", pc.ConnectionAddress)
}

// BlockRequest represents a pending request for a block of data.
type BlockRequest struct {
	PieceIndex  int
	BlockIndex  int
	Begin       int32 // Offset within piece in bytes
	Length      int32 // Block length in bytes
	RequestedAt time.Time
}

// GetStatus returns the current peer connection status.
func (pc *PeerConnection) GetStatus() PeerConnectionStatus {
	v := pc.Status.Load()
	if v == nil {
		return StatusDiscovered // Default value
	}
	return v.(PeerConnectionStatus)
}

// SetStatus sets the peer connection status.
func (pc *PeerConnection) SetStatus(status PeerConnectionStatus) {
	pc.Status.Store(status)
}

// SetBitfield sets the peer's bitfield (thread-safe).
func (pc *PeerConnection) SetBitfield(bitfield []byte) {
	pc.bitfieldMu.Lock()
	defer pc.bitfieldMu.Unlock()
	pc.Bitfield = bitfield
}

// HasPiece checks if the peer has a specific piece based on their bitfield.
func (pc *PeerConnection) HasPiece(pieceIndex int) bool {
	pc.bitfieldMu.RLock()
	defer pc.bitfieldMu.RUnlock()

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
