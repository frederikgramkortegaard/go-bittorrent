package daemon

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"gotorrent/internal"
	"gotorrent/internal/bencoding"
	"gotorrent/internal/libnet"
	"sync"
	"time"
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
		DiskManager: NewDiskManager(torrentFile, "."), // TODO: Make output dir configurable
	}, nil
}

// StartTorrentDownloadSession creates and starts a new torrent download session.
// This will download the torrent, write it to disk, and clean up automatically.
func (t *TorrentManager) StartTorrentDownloadSession(torrentFile bencoding.TorrentFile) (*TorrentSession, error) {

	if torrentFile.Data == nil {
		return nil, errors.New("torrentfile has no data field")
	}

	if _, ok := torrentFile.Data["announce"]; !ok {
		return nil, errors.New("no annouce field in torrentfile")
	}

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
		Left:           100,
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
				Peer:         &p,
				AmChoking:    true, // Start choking by default
				AmInterested: false,
				RequestChan:  make(chan struct{}, 5), // Buffer of 5 for pipelining @TODO : move this magic number to config
			}
			pc.SetStatus(libnet.StatusDiscovered)
			pc.PeerChoking.Store(true)     // Assume peer is choking us initially
			pc.PeerInterested.Store(false) // Assume peer is not interested initially

			// Update status to connecting
			pc.SetStatus(libnet.StatusConnecting)
			addr, conn, err := libnet.EstablishNewConnection(fmt.Sprintf("%s:%d", p.PeerAddress, p.PeerPort))
			if err != nil {
				pc.SetStatus(libnet.StatusFailed)
				pc.Error = err
				fmt.Printf("Connection failed to %s:%d: %v\n", p.PeerAddress, p.PeerPort, err)
				session.AddConnection(pc)
				return
			}

			// TCP connected successfully
			pc.ConnectionAddress = addr
			pc.Connection = conn
			pc.SetStatus(libnet.StatusConnected)

			// Attempt handshake
			pc.SetStatus(libnet.StatusHandshaking)
			_, err = libnet.SendHandshakeToPeer(pc, t.Client.ID, torrentFile.InfoHash)
			if err != nil {
				pc.SetStatus(libnet.StatusFailed)
				pc.Error = err
				fmt.Printf("Handshake failed with %s:%d: %v\n", p.PeerAddress, p.PeerPort, err)
				session.AddConnection(pc)
				return
			}

			fmt.Printf("Handshake successful with %s:%d\n", p.PeerAddress, p.PeerPort)

			// Read first message (should be bitfield, but might be something else)
			msg, err := libnet.ReadMessage(pc.Connection)
			if err != nil {
				pc.SetStatus(libnet.StatusFailed)
				pc.Error = fmt.Errorf("failed to read first message: %w", err)
				fmt.Printf("Failed to read message from %s:%d: %v\n", p.PeerAddress, p.PeerPort, err)
				session.AddConnection(pc)
				return
			}

			// Handle the message
			if msg.ID != nil && *msg.ID == libnet.MsgBitfield {
				pc.Bitfield = msg.Payload
				fmt.Printf("Received bitfield from %s:%d (%d bytes)\n", p.PeerAddress, p.PeerPort, len(msg.Payload))
				pc.SetStatus(libnet.StatusActive)
			} else if msg.ID == nil {
				fmt.Printf("Received keep-alive from %s:%d\n", p.PeerAddress, p.PeerPort)
				pc.SetStatus(libnet.StatusActive)
			} else {
				fmt.Printf("Received unexpected message (ID=%d) from %s:%d\n", *msg.ID, p.PeerAddress, p.PeerPort)
				pc.SetStatus(libnet.StatusActive)
			}

			// Store connection
			session.AddConnection(pc)
		}(peer)
	}

	// Wait for all connection attempts to complete
	fmt.Printf("Waiting for %d peer connection attempts...\n", len(peers))
	wg.Wait()

	// Print summary
	fmt.Printf("Total peers: %d\n", len(session.Connections))
	fmt.Printf("Active: %d\n", len(session.GetActivePeers()))
	fmt.Printf("Failed: %d\n", len(session.GetFailedPeers()))

	// Create context for coordinating peer loops
	ctx, cancel := context.WithCancel(context.Background())
	session.ctx = ctx
	session.cancel = cancel

	// Start download loops for each active peer
	for _, peer := range session.GetActivePeers() {
		go session.PeerReadLoop(ctx, peer)
		go session.PeerDownloadLoop(ctx, peer)
	}

	// Start completion monitor goroutine
	go func() {
		// Wait for download to complete
		for !session.PieceManager.IsComplete() {
			time.Sleep(1 * time.Second)
		}

		// Handle completion - write to disk and clean up
		err := session.Complete()
		if err != nil {
			fmt.Printf("Error completing torrent: %v\n", err)
		}
	}()

	// @TODO : Start a loop that continuously requests / updates from the tracker, and
	// attempts to re-connect to failed peers etc.

	return session, nil
}

// TorrentSession represents an active download/upload session for a single torrent.
type TorrentSession struct {
	TorrentFile   bencoding.TorrentFile
	Connections   []*libnet.PeerConnection // All peer connections (filter by .Status)
	PieceManager  *internal.PieceManager
	DiskManager   *DiskManager
	connectionsMu sync.Mutex

	// Context for coordinating goroutine shutdown
	ctx    context.Context
	cancel context.CancelFunc
}

func (ts *TorrentSession) PeerReadLoop(ctx context.Context, peer *libnet.PeerConnection) {
	// Set initial read deadline (60 seconds timeout)
	peer.Connection.SetReadDeadline(time.Now().Add(60 * time.Second))

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("PeerReadLoop for %s shutting down\n", peer.ConnectionAddress)
			return
		default:
		}

		msg, err := libnet.ReadMessage(peer.Connection)
		if err != nil {
			fmt.Printf("Failed to read from %s (timeout or connection error): %v\n", peer.ConnectionAddress, err)
			peer.SetStatus(libnet.StatusFailed)
			return
		}

		// Reset deadline after successful read
		peer.Connection.SetReadDeadline(time.Now().Add(60 * time.Second))
		peer.LastSeen = time.Now()

		if msg.ID == nil {
			// Keep-alive, ignore
			continue
		}

		switch *msg.ID {

		case libnet.MsgUnchoke:
			fmt.Printf("Peer %s unchoked us!\n", peer.ConnectionAddress)
			peer.PeerChoking.Store(false)

		case libnet.MsgChoke:
			fmt.Printf("Peer %s choked us (still waiting)...\n", peer.ConnectionAddress)
			peer.PeerChoking.Store(true)

		case libnet.MsgHave:
			// @TODO : Not yet implemented - for now we're only downloading and we dont really
			// care about pareto efficiency

		case libnet.MsgPiece:
			// Parse PIECE message: <index><begin><block data>
			if len(msg.Payload) < 8 {
				fmt.Printf("Invalid PIECE message from %s\n", peer.ConnectionAddress)
				continue
			}

			receivedPieceIndex := int(binary.BigEndian.Uint32(msg.Payload[0:4]))
			receivedBegin := int32(binary.BigEndian.Uint32(msg.Payload[4:8]))
			blockData := msg.Payload[8:]

			fmt.Printf("Received piece=%d begin=%d len=%d from %s\n",
				receivedPieceIndex, receivedBegin, len(blockData), peer.ConnectionAddress)

			// Calculate block index from begin offset
			receivedBlockIndex := int(receivedBegin / ts.PieceManager.BlockSize)

			// Signal that a request slot is now available (non-blocking)
			select {
			case <-peer.RequestChan:
				// Successfully drained one request slot
			default:
				// Channel was already empty, that's fine
			}

			// Store the block (this will also remove from pending)
			complete := ts.PieceManager.AddBlock(receivedPieceIndex, receivedBlockIndex, blockData)

			if complete {
				// Piece is complete, verify it
				if ts.PieceManager.VerifyPiece(receivedPieceIndex) {
					fmt.Printf("Piece %d verified successfully!\n", receivedPieceIndex)

					// Queue piece for writing to disk
					pieceData := ts.PieceManager.GetPieceData(receivedPieceIndex)
					if pieceData != nil {
						ts.DiskManager.QueueWrite(receivedPieceIndex, pieceData)
						// Free memory by clearing the piece data
						ts.PieceManager.ClearPieceData(receivedPieceIndex)
					}
				} else {
					fmt.Printf("Piece %d FAILED verification, re-downloading\n", receivedPieceIndex)
					// Mark piece as failed and re-download
					ts.PieceManager.RemovePiece(receivedPieceIndex)
				}
			}

		default:
			fmt.Printf("Received unhandled  message ID=%d from %s\n", *msg.ID, peer.ConnectionAddress)
		}
	}
}

// PeerDownloadLoop handles downloading pieces from a single peer.
func (ts *TorrentSession) PeerDownloadLoop(ctx context.Context, peer *libnet.PeerConnection) {
	fmt.Printf("Starting download loop for peer %s\n", peer.ConnectionAddress)

	// Send INTERESTED message first
	interestedMsg := libnet.NewSimpleMessage(libnet.MsgInterested)
	if err := libnet.SendMessage(peer.Connection, interestedMsg); err != nil {
		fmt.Printf("Failed to send INTERESTED to %s: %v\n", peer.ConnectionAddress, err)
		peer.SetStatus(libnet.StatusFailed)
		return
	}
	peer.AmInterested = true
	fmt.Printf("Sent INTERESTED to %s\n", peer.ConnectionAddress)

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		// Check if we're choked first
		if peer.PeerChoking.Load() {
			select {
			case <-ctx.Done():
				fmt.Printf("PeerDownloadLoop for %s shutting down\n", peer.ConnectionAddress)
				return
			case <-ticker.C:
				// Wait and retry
				continue
			}
		}

		// Try to acquire a request slot 
		select {
		case <-ctx.Done():
			fmt.Printf("PeerDownloadLoop for %s shutting down\n", peer.ConnectionAddress)
			return

		case peer.RequestChan <- struct{}{}:
			// Successfully acquired a request slot
			// Now select piece/block (after we have the slot)

			// Select next piece to download from this peer
			pieceIndex, ok := internal.SelectNextPiece(ts.PieceManager, peer)
			if !ok {
				// No pieces available, release the slot and wait
				<-peer.RequestChan
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					continue
				}
			}

			// Initialize piece if not already started
			ts.PieceManager.InitializePiece(pieceIndex, ts.PieceManager.BlockSize)

			// Get next block to request from this piece
			blockIndex, begin, length, ok := internal.GetNextBlockToRequest(ts.PieceManager, pieceIndex)
			if !ok {
				// No blocks available in this piece, release slot and try next piece
				<-peer.RequestChan
				continue
			}

			// Mark block as pending globally
			ts.PieceManager.MarkBlockPending(pieceIndex, blockIndex)

			// Create REQUEST message
			buf := new(bytes.Buffer)
			binary.Write(buf, binary.BigEndian, uint32(pieceIndex))
			binary.Write(buf, binary.BigEndian, uint32(begin))
			binary.Write(buf, binary.BigEndian, uint32(length))
			requestMsg := libnet.NewMessage(libnet.MsgRequest, buf.Bytes())

			// Send the REQUEST
			err := libnet.SendMessage(peer.Connection, requestMsg)
			if err != nil {
				fmt.Printf("Failed to send request to %s: %v\n", peer.ConnectionAddress, err)
				peer.SetStatus(libnet.StatusFailed)
				ts.PieceManager.UnmarkBlockPending(pieceIndex, blockIndex)
				// Release the request slot
				<-peer.RequestChan
				return
			}

			fmt.Printf("Requested piece=%d block=%d from %s\n", pieceIndex, blockIndex, peer.ConnectionAddress)
		}
	}
}

// Cancel cancels the context, shutting down all peer loops gracefully.
func (ts *TorrentSession) Cancel() {
	if ts.cancel != nil {
		ts.cancel()
	}
}

// Complete handles torrent completion - writes to disk and cleans up.
func (ts *TorrentSession) Complete() error {
	fmt.Println("\nTorrent download complete!")
	fmt.Printf("Downloaded %d pieces\n", ts.PieceManager.CompletedPieces())

	// Write to disk
	err := ts.DiskManager.WriteToDisk(ts.PieceManager)
	if err != nil {
		return fmt.Errorf("failed to write to disk: %w", err)
	}

	fmt.Println("File written to disk successfully!")

	// Shut down all peer loops
	ts.Cancel()

	return nil
}

func (ts *TorrentSession) AddConnection(c *libnet.PeerConnection) {
	ts.connectionsMu.Lock()
	defer ts.connectionsMu.Unlock()
	ts.Connections = append(ts.Connections, c)

}

// GetActivePeers returns all peers with active connections.
func (ts *TorrentSession) GetActivePeers() []*libnet.PeerConnection {
	var active []*libnet.PeerConnection
	for _, pc := range ts.Connections {
		if pc.GetStatus() == libnet.StatusActive {
			active = append(active, pc)
		}
	}
	return active
}

// GetFailedPeers returns all peers with failed connections.
func (ts *TorrentSession) GetFailedPeers() []*libnet.PeerConnection {
	var failed []*libnet.PeerConnection
	for _, pc := range ts.Connections {
		if pc.GetStatus() == libnet.StatusFailed {
			failed = append(failed, pc)
		}
	}
	return failed
}
