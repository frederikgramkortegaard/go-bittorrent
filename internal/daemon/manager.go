package daemon

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"go-bittorrent/internal"
	"go-bittorrent/internal/bencoding"
	"go-bittorrent/internal/config"
	"go-bittorrent/internal/libnet"
	"go-bittorrent/internal/logger"
	"sync"
	"time"
)

// TorrentManager manages all active torrent sessions for the application.
type TorrentManager struct {
	Sessions   map[[20]byte]*TorrentSession
	sessionsMu sync.RWMutex   // Protects Sessions map
	Client     *libnet.Client // Shared client for all torrents
}

// NewTorrentManager creates a new TorrentManager with a shared client.
func NewTorrentManager(client *libnet.Client) *TorrentManager {
	return &TorrentManager{
		Sessions: make(map[[20]byte]*TorrentSession, 0),
		Client:   client,
	}
}

// AddSession adds a session to the manager
func (t *TorrentManager) AddSession(session *TorrentSession) {
	t.sessionsMu.Lock()
	defer t.sessionsMu.Unlock()
	t.Sessions[session.TorrentFile.InfoHash] = session
}

func (t *TorrentManager) RemoveSession(infohash [20]byte) {
	t.sessionsMu.Lock()
	defer t.sessionsMu.Unlock()

	delete(t.Sessions, infohash)

}

// NewTorrentSession creates a new torrent session.
func NewTorrentSession(torrentFile bencoding.TorrentFile, cfg *config.Config) (*TorrentSession, error) {
	// Extract piece information from torrent file
	pieceInfo, err := bencoding.ExtractPieceInfo(torrentFile)
	if err != nil {
		return nil, fmt.Errorf("failed to extract piece info: %w", err)
	}

	session := &TorrentSession{
		TorrentFile: torrentFile,
		Connections: make([]*libnet.PeerConnection, 0),
		PieceManager: internal.NewPieceManager(
			pieceInfo.TotalPieces,
			pieceInfo.PieceLength,
			pieceInfo.LastPieceLength,
			pieceInfo.Hashes,
			cfg.BlockSize,
		),
		DiskManager: NewDiskManager(torrentFile, cfg.OutputDir, cfg),
		Config:      cfg,
	}

	// Create logger for this session
	session.Logger = logger.New().WithComponent(session)

	return session, nil
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

	session, err := NewTorrentSession(torrentFile, t.Client.Config)
	if err != nil {
		return nil, err
	}

	// Calculate total size for tracker request
	totalSize := uint64(session.PieceManager.TotalSize())

	// Request data from the tracker using the shared client
	response, err := t.Client.SendTrackerRequest(torrentFile, libnet.SendTrackerRequestParams{
		TrackerAddress: *torrentFile.Data["announce"].StrVal,
		PeerID:         string(t.Client.ID[:]),
		Event:          "started",
		Port:           t.Client.Config.ListenPort,
		Uploaded:       0,
		Downloaded:     0,
		Left:           totalSize,
		Compact:        t.Client.Config.CompactMode,
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

	// Limit number of peers to connect to
	if len(peers) > t.Client.Config.MaxPeersPerTorrent {
		peers = peers[:t.Client.Config.MaxPeersPerTorrent]
	}

	// Add session to manager (peers will be added as connections below)
	t.AddSession(session)

	// Attempt to connect to peers concurrently
	var wg sync.WaitGroup
	for _, peer := range peers {

		wg.Add(1)
		go func(p bencoding.PeerStruct) {
			defer wg.Done()

			// Create peer connection in discovered state
			pc := &libnet.PeerConnection{
				Peer:         &p,
				AmChoking:    true,
				AmInterested: false,
				RequestChan:  make(chan struct{}, session.Config.RequestPipelineSize),
			}
			pc.SetStatus(libnet.StatusDiscovered)
			pc.PeerChoking.Store(true)     // Assume peer is choking us initially
			pc.PeerInterested.Store(false) // Assume peer is not interested initially

			// Set connection address for logger (before creating logger)
			pc.ConnectionAddress = fmt.Sprintf("%s:%d", p.PeerAddress, p.PeerPort)

			// Create logger for this peer connection
			pc.Logger = session.Logger.WithComponent(pc)

			// Update status to connecting
			pc.SetStatus(libnet.StatusConnecting)
			pc.Logger.Info("Connecting to peer...")
			addr, conn, err := libnet.EstablishNewConnection(pc.ConnectionAddress)
			if err != nil {
				pc.SetStatus(libnet.StatusFailed)
				pc.Error = err
				pc.Logger.Error("Connection failed: %v", err)
				session.AddConnection(pc)
				return
			}

			// TCP connected successfully
			pc.ConnectionAddress = addr
			pc.Connection = conn
			pc.SetStatus(libnet.StatusConnected)

			// Attempt handshake
			pc.SetStatus(libnet.StatusHandshaking)
			pc.Logger.Info("Sending handshake...")
			_, err = libnet.SendHandshakeToPeer(pc, t.Client.ID, torrentFile.InfoHash)
			if err != nil {
				pc.SetStatus(libnet.StatusFailed)
				pc.Error = err
				pc.Logger.Error("Handshake failed: %v", err)
				session.AddConnection(pc)
				return
			}

			pc.Logger.Info("Handshake successful")

			// Read first message (should be bitfield, but might be something else)
			msg, err := libnet.ReadMessage(pc.Connection)
			if err != nil {
				pc.SetStatus(libnet.StatusFailed)
				pc.Error = fmt.Errorf("failed to read first message: %w", err)
				pc.Logger.Error("Failed to read first message: %v", err)
				session.AddConnection(pc)
				return
			}

			// Handle the message
			if msg.ID != nil && *msg.ID == libnet.MsgBitfield {
				pc.Bitfield = msg.Payload
				pc.Logger.Info("Received bitfield (%d bytes)", len(msg.Payload))
				pc.SetStatus(libnet.StatusActive)
			} else if msg.ID == nil {
				pc.Logger.Debug("Received keep-alive")
				pc.SetStatus(libnet.StatusActive)
			} else {
				pc.Logger.Warn("Received unexpected message (ID=%d)", *msg.ID)
				pc.SetStatus(libnet.StatusActive)
			}

			// Store connection
			session.AddConnection(pc)
		}(peer)
	}

	// Wait for all connection attempts to complete
	session.Logger.Info("Waiting for %d peer connection attempts...", len(peers))
	wg.Wait()

	// Print summary
	session.Logger.Info("Connection summary - Total: %d, Active: %d, Failed: %d",
		len(session.Connections),
		len(session.GetActivePeers()),
		len(session.GetFailedPeers()))

	// Check if we have any active peers
	activePeers := session.GetActivePeers()
	if len(activePeers) == 0 {
		return nil, fmt.Errorf("no active peers available - all connections failed")
	}

	// Create context for coordinating peer loops
	ctx, cancel := context.WithCancel(context.Background())
	session.ctx = ctx
	session.cancel = cancel

	// Start download loops for each active peer
	for _, peer := range activePeers {
		go session.PeerReadLoop(ctx, peer)
		go session.PeerDownloadLoop(ctx, peer)
	}

	// Start completion monitor goroutine
	go func() {
		// Wait for download to complete
		for !session.PieceManager.IsComplete() {
			time.Sleep(session.Config.CompletionPollInterval)
		}

		// Handle completion - write to disk and clean up
		err := session.Complete()
		if err != nil {
			session.Logger.Error("Error completing torrent: %v", err)
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
	Config        *config.Config
	connectionsMu sync.Mutex
	Logger        *logger.Logger

	// Context for coordinating goroutine shutdown
	ctx    context.Context
	cancel context.CancelFunc
}

// String implements fmt.Stringer for TorrentSession
func (ts *TorrentSession) String() string {
	// Try to get name from torrent file
	if info, ok := ts.TorrentFile.Data["info"]; ok && info.Dict != nil {
		if nameObj, ok := info.Dict["name"]; ok && nameObj.StrVal != nil {
			return fmt.Sprintf("Torrent:%s", *nameObj.StrVal)
		}
	}
	// Fallback to info hash
	return fmt.Sprintf("Torrent:%x", ts.TorrentFile.InfoHash[:8])
}

func (ts *TorrentSession) PeerReadLoop(ctx context.Context, peer *libnet.PeerConnection) {
	// Set initial read deadline
	peer.Connection.SetReadDeadline(time.Now().Add(ts.Config.PeerReadTimeout))

	for {
		select {
		case <-ctx.Done():
			peer.Logger.Debug("Read loop shutting down")
			return
		default:
		}

		msg, err := libnet.ReadMessage(peer.Connection)
		if err != nil {
			peer.Logger.Error("Failed to read message: %v", err)
			peer.SetStatus(libnet.StatusFailed)
			return
		}

		// Reset deadline after successful read
		peer.Connection.SetReadDeadline(time.Now().Add(ts.Config.PeerReadTimeout))
		peer.LastSeen = time.Now()

		if msg.ID == nil {
			// Keep-alive, ignore
			continue
		}

		switch *msg.ID {

		case libnet.MsgUnchoke:
			peer.Logger.Info("Unchoked by peer")
			peer.PeerChoking.Store(false)

		case libnet.MsgChoke:
			peer.Logger.Warn("Choked by peer")
			peer.PeerChoking.Store(true)

		case libnet.MsgHave:
			// @TODO : Not yet implemented - for now we're only downloading and we dont really
			// care about pareto efficiency

		case libnet.MsgPiece:
			// Parse PIECE message: <index><begin><block data>
			if len(msg.Payload) < 8 {
				peer.Logger.Error("Invalid PIECE message (payload too short)")
				continue
			}

			receivedPieceIndex := int(binary.BigEndian.Uint32(msg.Payload[0:4]))
			receivedBegin := int32(binary.BigEndian.Uint32(msg.Payload[4:8]))
			blockData := msg.Payload[8:]

			peer.Logger.Debug("Received piece=%d begin=%d len=%d", receivedPieceIndex, receivedBegin, len(blockData))

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
					ts.Logger.Info("Piece %d verified successfully", receivedPieceIndex)

					// Queue piece for writing to disk
					pieceData := ts.PieceManager.GetPieceData(receivedPieceIndex)
					if pieceData != nil {
						ts.DiskManager.QueueWrite(receivedPieceIndex, pieceData)
						// Free memory by clearing the piece data
						ts.PieceManager.ClearPieceData(receivedPieceIndex)
					}
				} else {
					ts.Logger.Warn("Piece %d FAILED verification, re-downloading", receivedPieceIndex)
					// Mark piece as failed and re-download
					ts.PieceManager.RemovePiece(receivedPieceIndex)
				}
			}

		default:
			peer.Logger.Warn("Received unhandled message ID=%d", *msg.ID)
		}
	}
}

// PeerDownloadLoop handles downloading pieces from a single peer.
func (ts *TorrentSession) PeerDownloadLoop(ctx context.Context, peer *libnet.PeerConnection) {
	peer.Logger.Info("Starting download loop")

	// Send INTERESTED message first
	interestedMsg := libnet.NewSimpleMessage(libnet.MsgInterested)
	if err := libnet.SendMessage(peer.Connection, interestedMsg); err != nil {
		peer.Logger.Error("Failed to send INTERESTED: %v", err)
		peer.SetStatus(libnet.StatusFailed)
		return
	}
	peer.AmInterested = true
	peer.Logger.Debug("Sent INTERESTED message")

	ticker := time.NewTicker(ts.Config.DownloadLoopInterval)
	defer ticker.Stop()

	for {
		// Check if we're choked first
		if peer.PeerChoking.Load() {
			select {
			case <-ctx.Done():
				peer.Logger.Debug("Download loop shutting down (choked)")
				return
			case <-ticker.C:
				// Wait and retry
				continue
			}
		}

		// Try to acquire a request slot
		select {
		case <-ctx.Done():
			peer.Logger.Debug("Download loop shutting down")
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
				peer.Logger.Error("Failed to send request: %v", err)
				peer.SetStatus(libnet.StatusFailed)
				ts.PieceManager.UnmarkBlockPending(pieceIndex, blockIndex)
				// Release the request slot
				<-peer.RequestChan
				return
			}

			peer.Logger.Debug("Requested piece=%d block=%d", pieceIndex, blockIndex)
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
	ts.Logger.Info("Torrent download complete! Downloaded %d pieces", ts.PieceManager.CompletedPieces())

	// Write to disk
	err := ts.DiskManager.WriteToDisk(ts.PieceManager)
	if err != nil {
		return fmt.Errorf("failed to write to disk: %w", err)
	}

	ts.Logger.Info("File written to disk successfully")

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
