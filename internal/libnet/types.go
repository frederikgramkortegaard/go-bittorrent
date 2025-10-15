// Manages state and progress of downloads / uploads of torrent files

package libnet

// PeerConnectionStatus represents the current state of a peer connection.
type PeerConnectionStatus string

const (
	StatusDiscovered   PeerConnectionStatus = "discovered"   // Peer received from tracker
	StatusConnecting   PeerConnectionStatus = "connecting"   // TCP connection in progress
	StatusConnected    PeerConnectionStatus = "connected"    // TCP connected, handshake not sent
	StatusHandshaking  PeerConnectionStatus = "handshaking"  // Handshake in progress
	StatusActive       PeerConnectionStatus = "active"       // Handshake complete, ready for data transfer
	StatusFailed       PeerConnectionStatus = "failed"       // Connection or handshake failed
	StatusDisconnected PeerConnectionStatus = "disconnected" // Was active, now closed
)
