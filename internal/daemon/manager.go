package daemon

import (
	"fmt"
	"gotorrent/internal"
	"gotorrent/internal/bencoding"
	"gotorrent/internal/libnet"
	"sync"
)

// TorrentManager manages all active torrent sessions for the application.
type TorrentManager struct {
	Sessions []*TorrentSession
	Client   *libnet.Client // Shared client for all torrents
}

// NewTorrentManager creates a new TorrentManager with a shared client.
func NewTorrentManager(client *libnet.Client) *TorrentManager {
	return &TorrentManager{
		Sessions: make([]*TorrentSession, 0),
		Client:   client,
	}
}

// StartTorrentSession creates and starts a new torrent session.
func (t *TorrentManager) StartTorrentSession(torrentFile bencoding.TorrentFile) (*TorrentSession, error) {

	session, err := NewTorrentSession(torrentFile)
	if err != nil {
		return nil, err
	}

	// Request data from the tracker using the shared client
	response, err := libnet.SendTrackerRequest(t.Client, torrentFile, libnet.SendTrackerRequestParams{
		TrackerAddress: *torrentFile.Data["announce"].StrVal,
		PeerID:         string(t.Client.ID[:]),
		Event:          "started",
		Port:           6881,
		Uploaded:       0,
		Downloaded:     0,
		Left:           100, // We've downloaded nothing as this point
		Compact:        false,
	})

	if err != nil {
		return nil, err
	}

	bencoding.PrintDict(response, 0)

	// Extract peers from tracker response
	peers, err := bencoding.ExtractPeersFromTrackerResponse(response)
	if err != nil {
		return nil, err
	}

	// Add session to manager (peers will be added as connections below)
	t.Sessions = append(t.Sessions, session)

	// Attempt to connect to peers concurrently
	var wg sync.WaitGroup
	for _, peer := range peers {
		wg.Add(1)
		go func(p bencoding.PeerStruct) {
			defer wg.Done()

			// Create peer connection in discovered state
			pc := &libnet.PeerConnection{
				Peer:           &p,
				Status:         libnet.StatusDiscovered,
				AmChoking:      true, // Start choking by default
				AmInterested:   false,
				PeerChoking:    true, // Assume peer is choking us
				PeerInterested: false,
			}

			// Update status to connecting
			pc.Status = libnet.StatusConnecting
			addr, conn, err := libnet.EstablishNewConnection(fmt.Sprintf("%s:%d", p.PeerAddress, p.PeerPort))
			if err != nil {
				pc.Status = libnet.StatusFailed
				pc.Error = err
				fmt.Printf("Connection failed to %s:%d: %v\n", p.PeerAddress, p.PeerPort, err)
				session.Connections = append(session.Connections, pc)
				return
			}

			// TCP connected successfully
			pc.ConnectionAddress = addr
			pc.Connection = conn
			pc.Status = libnet.StatusConnected

			// Attempt handshake
			pc.Status = libnet.StatusHandshaking
			_, err = libnet.SendHandshakeToPeer(pc, t.Client.ID, torrentFile.InfoHash)
			if err != nil {
				pc.Status = libnet.StatusFailed
				pc.Error = err
				fmt.Printf("Handshake failed with %s:%d: %v\n", p.PeerAddress, p.PeerPort, err)
				session.Connections = append(session.Connections, pc)
				return
			}

			fmt.Printf("Handshake successful with %s:%d\n", p.PeerAddress, p.PeerPort)

			// Read first message (should be bitfield, but might be something else)
			msg, err := libnet.ReadMessage(pc.Connection)
			if err != nil {
				pc.Status = libnet.StatusFailed
				pc.Error = fmt.Errorf("failed to read first message: %w", err)
				fmt.Printf("Failed to read message from %s:%d: %v\n", p.PeerAddress, p.PeerPort, err)
				session.Connections = append(session.Connections, pc)
				return
			}

			// Handle the message
			if msg.ID != nil && *msg.ID == libnet.MsgBitfield {
				pc.Bitfield = msg.Payload
				fmt.Printf("Received bitfield from %s:%d (%d bytes)\n", p.PeerAddress, p.PeerPort, len(msg.Payload))
				pc.Status = libnet.StatusActive
			} else if msg.ID == nil {
				fmt.Printf("Received keep-alive from %s:%d\n", p.PeerAddress, p.PeerPort)
				pc.Status = libnet.StatusActive
			} else {
				fmt.Printf("Received unexpected message (ID=%d) from %s:%d\n", *msg.ID, p.PeerAddress, p.PeerPort)
				pc.Status = libnet.StatusActive
			}

			// Store connection
			session.Connections = append(session.Connections, pc)
		}(peer)
	}

	// Wait for all connection attempts to complete
	fmt.Printf("Waiting for %d peer connection attempts...\n", len(peers))
	wg.Wait()

	// Print summary
	fmt.Printf("Total peers: %d\n", len(session.Connections))
	fmt.Printf("Active: %d\n", len(session.GetActivePeers()))
	fmt.Printf("Failed: %d\n", len(session.GetFailedPeers()))



	return session, nil
}

// TorrentSession represents an active download/upload session for a single torrent.
type TorrentSession struct {
	TorrentFile bencoding.TorrentFile
	Connections []*libnet.PeerConnection // All peer connections (filter by .Status)
	PieceManager *internal.PieceManager
}

// NewTorrentSession creates a new torrent session.
func NewTorrentSession(torrentFile bencoding.TorrentFile) (*TorrentSession, error) {
	// Extract piece information from torrent file
	pieceInfo, err := bencoding.ExtractPieceInfo(torrentFile)
	if err != nil {
		return nil, fmt.Errorf("failed to extract piece info: %w", err)
	}

	return &TorrentSession{
		TorrentFile: torrentFile,
		Connections: make([]*libnet.PeerConnection, 0),
		PieceManager: internal.NewPieceManager(
			pieceInfo.TotalPieces,
			pieceInfo.PieceLength,
			pieceInfo.LastPieceLength,
			pieceInfo.Hashes,
			16384, // 16KB block size
		),
	}, nil
}

// GetActivePeers returns all peers with active connections.
func (ts *TorrentSession) GetActivePeers() []*libnet.PeerConnection {
	var active []*libnet.PeerConnection
	for _, pc := range ts.Connections {
		if pc.Status == libnet.StatusActive {
			active = append(active, pc)
		}
	}
	return active
}

// GetFailedPeers returns all peers with failed connections.
func (ts *TorrentSession) GetFailedPeers() []*libnet.PeerConnection {
	var failed []*libnet.PeerConnection
	for _, pc := range ts.Connections {
		if pc.Status == libnet.StatusFailed {
			failed = append(failed, pc)
		}
	}
	return failed
}
