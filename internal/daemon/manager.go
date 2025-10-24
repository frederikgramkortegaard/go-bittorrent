package daemon

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"go-bittorrent/internal"
	"go-bittorrent/internal/bencoding"
	"go-bittorrent/internal/config"
	"go-bittorrent/internal/libnet"
	"go-bittorrent/internal/logger"
	"math/bits"
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
		Connections: make(map[string]*libnet.PeerConnection),
		PieceManager: internal.NewPieceManager(
			pieceInfo.TotalPieces,
			pieceInfo.PieceLength,
			pieceInfo.LastPieceLength,
			pieceInfo.Hashes,
			cfg.BlockSize,
		),
		DiskManager:  NewDiskManager(torrentFile, cfg.OutputDir, cfg),
		IsSeedMature: false,
		Config:       cfg,
		doneChan:     nil, // Will be created when download sequence starts
	}

	// Create logger for this session
	session.Logger = logger.New().WithComponent(session)

	return session, nil
}

// StartTorrentSession creates and starts a new torrent session.
// This will acquire missing pieces, write them to disk, and can seed when complete.
func (t *TorrentManager) StartTorrentSession(torrentFile bencoding.TorrentFile) (*TorrentSession, error) {

	if torrentFile.Data == nil {
		return nil, ErrNoDataField
	}

	session, err := NewTorrentSession(torrentFile, t.Client.Config)
	if err != nil {
		return nil, err
	}

	// Add session to manager
	t.AddSession(session)

	// Create context for coordinating peer loops
	ctx, cancel := context.WithCancel(context.Background())
	session.ctx = ctx
	session.cancel = cancel

	// We have at least a single fully completed and verified piece of this torrent on-disk
	session.IsSeedMature = torrentFile.Bitfield != nil

	// Calculate piece availability
	// @NOTE : If the files on disk have been deleted, this could be incorrect
	// because of this, when we on program start go through our dotfolder and find every
	// torrentfile, we could/should possible do some validation of the data on-disk,
	pieceInfo, err := bencoding.ExtractPieceInfo(torrentFile)
	if err != nil {
		return nil, ErrNoPieceInfo
	}

	if len(torrentFile.Bitfield) != pieceInfo.TotalPieces {
		return nil, ErrBitfieldLengthMismatch
	}

	// Calculate how many pieces we already have and that are fully available
	setBits := 0
	for _, b := range torrentFile.Bitfield {
		setBits += bits.OnesCount8(b)
	}

	// Initiate donwload if required
	if torrentFile.Bitfield == nil || setBits < pieceInfo.TotalPieces {
		if _, ok := torrentFile.Data["announce"]; !ok {
			return session, ErrNoAnnounceField
		}

		err = session.InitiateDownloadSequence(t, ctx)
		if err != nil {
			return session, err
		}
	}

	// @NOTE : If we don't already have all of the torrent, we setup a download loop, it is not
	// required for us to setup a loop for seeding, as since this session now exists in-memory, the
	// libnet/client.go will on-message received, validate the message, and then use the infohash
	// to try and find this session in the TorrentManager's Session map, and from there it can access
	// this sessions DiskManager and use ReadPiece / ReadBlock.

	return session, nil
}

// TorrentSession represents an active download/upload session for a single torrent.
type TorrentSession struct {
	TorrentFile   bencoding.TorrentFile
	Connections   map[string]*libnet.PeerConnection // Map of peer address -> connection
	PieceManager  *internal.PieceManager
	DiskManager   *DiskManager
	Config        *config.Config
	connectionsMu sync.RWMutex
	Logger        *logger.Logger

	// Context for coordinating goroutine shutdown
	ctx    context.Context
	cancel context.CancelFunc

	// Channel closed when torrent completes (all pieces acquired) or is cancelled (broadcasts to all waiters)
	doneChan chan struct{}
	doneOnce sync.Once // Ensures doneChan is only closed once
	doneErr  error     // Error if cancelled/failed, nil if successful completion

	// Meta
	IsSeedMature bool
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
			ts.RemoveConnection(peer.ConnectionAddress)
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

					// Check if all pieces are complete
					if ts.PieceManager.IsComplete() {
						go ts.Complete()
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

func (ts *TorrentSession) InitiateDownloadSequence(torrentManager *TorrentManager, ctx context.Context) error {
	// Check if download sequence already in progress
	if ts.doneChan == nil {
		// First time - create the channel
		ts.Logger.Info("Initiating first download sequence")
		ts.doneChan = make(chan struct{})
	} else {
		// Not first time - check if previous download is still in progress
		select {
		case <-ts.doneChan:
			// Channel is closed (download completed/cancelled), reset for new sequence
			ts.Logger.Info("Resetting download sequence after previous completion")
			ts.doneChan = make(chan struct{})
			ts.doneOnce = sync.Once{}
			ts.doneErr = nil
		default:
			// Channel is still open, download already in progress
			ts.Logger.Info("Download sequence already in progress, rejecting duplicate initiation")
			return ErrDownloadAlreadyInProgress
		}
	}

	// Calculate total size for tracker request
	totalSize := uint64(ts.PieceManager.TotalSize())
	torrentFile := ts.TorrentFile

	// Request data from the tracker using the shared client
	response, err := torrentManager.Client.SendTrackerRequest(torrentFile, libnet.SendTrackerRequestParams{
		TrackerAddress: *torrentFile.Data["announce"].StrVal,
		PeerID:         string(torrentManager.Client.ID[:]),
		Event:          "started",
		Port:           torrentManager.Client.Config.ListenPort,
		Uploaded:       0,
		Downloaded:     0,
		Left:           totalSize,
		Compact:        torrentManager.Client.Config.CompactMode,
	})

	if err != nil {
		return err
	}

	bencoding.PrintDict(response, 0)

	// Extract peers from tracker response
	peers, err := bencoding.ExtractPeersFromTrackerResponse(response)
	if err != nil {
		return err
	}

	// Limit number of peers to connect to
	if len(peers) > torrentManager.Client.Config.MaxPeersPerTorrent {
		peers = peers[:torrentManager.Client.Config.MaxPeersPerTorrent]
	}

	// Connect to peers and perform handshakes
	connectDone := ts.ConnectToPeers(torrentManager.Client, peers)

	// Wait for all connection attempts to complete
	<-connectDone

	// Check if we have any active peers
	activePeers := ts.GetActivePeers()

	// Print summary
	ts.Logger.Info("Connection summary - Active: %d, Total attempts: %d",
		len(activePeers), len(ts.Connections))

	if len(activePeers) == 0 {
		return ErrNoActivePeers
	}
	// Start download loops for each active peer
	for _, peer := range activePeers {
		go ts.PeerReadLoop(ctx, peer)
		go ts.PeerDownloadLoop(ctx, peer)
	}

	// Start completion monitor goroutine
	go func() {
		// Wait for torrent to complete or be cancelled (blocks until doneChan is closed)
		<-ts.doneChan

		if ts.doneErr != nil {
			// ts was cancelled or failed
			ts.Logger.Info("Torrent ts ended: %v", ts.doneErr)
			return
		}

		// All pieces acquired successfully - handle completion
		err := ts.Complete()
		if err != nil {
			ts.Logger.Error("Error completing torrent: %v", err)
		}
	}()

	// Start peer health monitoring loop
	go ts.PeerHealthLoop(torrentManager, ctx)

	return nil
}

// PeerHealthLoop periodically requests new peers from the tracker and attempts to connect.
// Runs while the download is active (doneChan not closed).
func (ts *TorrentSession) PeerHealthLoop(torrentManager *TorrentManager, ctx context.Context) {
	ticker := time.NewTicker(ts.Config.PeerHealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			ts.Logger.Info("Peer health loop shutting down")
			return
		case <-ts.doneChan:
			ts.Logger.Info("Peer health loop stopping - torrent complete")
			return
		case <-ticker.C:
			// Check if we need more peers
			ts.connectionsMu.RLock()
			activePeers := len(ts.Connections)
			ts.connectionsMu.RUnlock()

			ts.Logger.Info("Peer health check - Active peers: %d", activePeers)

			// Request fresh peers from tracker
			totalSize := uint64(ts.PieceManager.TotalSize())
			response, err := torrentManager.Client.SendTrackerRequest(ts.TorrentFile, libnet.SendTrackerRequestParams{
				TrackerAddress: *ts.TorrentFile.Data["announce"].StrVal,
				PeerID:         string(torrentManager.Client.ID[:]),
				Event:          "",
				Port:           torrentManager.Client.Config.ListenPort,
				Uploaded:       0,
				Downloaded:     0,
				Left:           totalSize,
				Compact:        torrentManager.Client.Config.CompactMode,
			})

			if err != nil {
				ts.Logger.Error("Failed to request peers from tracker: %v", err)
				continue
			}

			// Extract and connect to new peers
			peers, err := bencoding.ExtractPeersFromTrackerResponse(response)
			if err != nil {
				ts.Logger.Error("Failed to extract peers from tracker response: %v", err)
				continue
			}

			ts.Logger.Info("Received %d peers from tracker", len(peers))

			// ConnectToPeers will skip peers already in our map (fire and forget)
			ts.ConnectToPeers(torrentManager.Client, peers)
		}
	}
}

// ConnectToPeers attempts to connect to a list of peers, perform handshakes, and receive bitfields.
// Returns a channel that closes when all connection attempts complete (successful or failed).
// Can be called multiple times to reconnect to failed peers or connect to new peers from tracker updates.
func (ts *TorrentSession) ConnectToPeers(client *libnet.Client, peers []bencoding.PeerStruct) <-chan struct{} {
	done := make(chan struct{})
	var wg sync.WaitGroup

	ts.Logger.Info("Attempting to connect to %d peers...", len(peers))

	for _, peer := range peers {
		wg.Add(1)
		go func(p bencoding.PeerStruct) {
			defer wg.Done()

			peerAddress := fmt.Sprintf("%s:%d", p.PeerAddress, p.PeerPort)

			// Check if we already have this peer
			ts.connectionsMu.RLock()
			_, exists := ts.Connections[peerAddress]
			ts.connectionsMu.RUnlock()

			if exists {
				// Peer already exists in map, skip
				return
			}

			// Create peer connection in discovered state
			pc := &libnet.PeerConnection{
				Peer:         &p,
				AmChoking:    true,
				AmInterested: false,
				RequestChan:  make(chan struct{}, ts.Config.RequestPipelineSize),
			}
			pc.SetStatus(libnet.StatusDiscovered)
			pc.PeerChoking.Store(true)     // Assume peer is choking us initially
			pc.PeerInterested.Store(false) // Assume peer is not interested initially

			// Set connection address for logger (before creating logger)
			pc.ConnectionAddress = peerAddress

			// Create logger for this peer connection
			pc.Logger = ts.Logger.WithComponent(pc)

			// Update status to connecting
			pc.SetStatus(libnet.StatusConnecting)
			pc.Logger.Info("Connecting to peer...")
			addr, conn, err := libnet.EstablishNewConnection(pc.ConnectionAddress)
			if err != nil {
				pc.SetStatus(libnet.StatusFailed)
				pc.Error = err
				pc.Logger.Error("Connection failed: %v", err)
				ts.AddConnection(pc)
				return
			}

			// TCP connected successfully
			pc.ConnectionAddress = addr
			pc.Connection = conn
			pc.SetStatus(libnet.StatusConnected)

			// Attempt handshake
			pc.SetStatus(libnet.StatusHandshaking)
			pc.Logger.Info("Sending handshake...")
			_, err = libnet.SendHandshakeToPeer(pc, client.ID, ts.TorrentFile.InfoHash)
			if err != nil {
				pc.SetStatus(libnet.StatusFailed)
				pc.Error = err
				pc.Logger.Error("Handshake failed: %v", err)
				ts.AddConnection(pc)
				return
			}

			pc.Logger.Info("Handshake successful")

			// Read first message (should be bitfield, but might be something else)
			msg, err := libnet.ReadMessage(pc.Connection)
			if err != nil {
				pc.SetStatus(libnet.StatusFailed)
				pc.Error = fmt.Errorf("failed to read first message: %w", err)
				pc.Logger.Error("Failed to read first message: %v", err)
				ts.AddConnection(pc)
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
			ts.AddConnection(pc)
		}(peer)
	}

	// Close the done channel when all connections complete
	go func() {
		wg.Wait()
		close(done)
	}()

	return done
}

// PeerDownloadLoop handles downloading pieces from a single peer.
func (ts *TorrentSession) PeerDownloadLoop(ctx context.Context, peer *libnet.PeerConnection) {
	peer.Logger.Info("Starting download loop")

	// Send INTERESTED message first
	interestedMsg := libnet.NewSimpleMessage(libnet.MsgInterested)
	if err := libnet.SendMessage(peer.Connection, interestedMsg); err != nil {
		peer.Logger.Error("Failed to send INTERESTED: %v", err)
		peer.SetStatus(libnet.StatusFailed)
		ts.RemoveConnection(peer.ConnectionAddress)
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
				ts.RemoveConnection(peer.ConnectionAddress)
				return
			}

			peer.Logger.Debug("Requested piece=%d block=%d", pieceIndex, blockIndex)
		}
	}
}

// StopPeerLoops stops all peer read/download loops by cancelling the context.
// This closes all peer connections gracefully.
func (ts *TorrentSession) StopPeerLoops() {
	if ts.cancel != nil {
		ts.cancel() // Cancel context -> stops PeerReadLoop and PeerDownloadLoop
	}
}

// Cancel stops the torrent session and signals cancellation.
// Use this when the user manually stops a torrent.
func (ts *TorrentSession) Cancel() {
	ts.StopPeerLoops()

	// Signal cancellation via doneChan
	ts.doneOnce.Do(func() {
		ts.doneErr = fmt.Errorf("cancelled")
		close(ts.doneChan)
	})
}

// Complete handles torrent completion - stops peer connections, marks ready for seeding,
// and signals completion via doneChan.
// This is called when all pieces have been acquired and verified.
func (ts *TorrentSession) Complete() error {
	ts.Logger.Info("Torrent complete, all %d pieces acquired", ts.PieceManager.CompletedPieces())

	// Stop all peer read/download loops
	ts.StopPeerLoops()

	ts.IsSeedMature = true // Mark as ready to seed

	// Signal successful completion via doneChan (doneErr stays nil)
	ts.doneOnce.Do(func() {
		ts.doneErr = nil
		close(ts.doneChan)
	})

	return nil
}

func (ts *TorrentSession) AddConnection(c *libnet.PeerConnection) {
	ts.connectionsMu.Lock()
	defer ts.connectionsMu.Unlock()
	ts.Connections[c.ConnectionAddress] = c
}

// RemoveConnection removes a peer connection from the map.
func (ts *TorrentSession) RemoveConnection(address string) {
	ts.connectionsMu.Lock()
	defer ts.connectionsMu.Unlock()
	delete(ts.Connections, address)
}

// GetActivePeers returns all peers in the connections map that have an active TCP connection.
func (ts *TorrentSession) GetActivePeers() []*libnet.PeerConnection {
	ts.connectionsMu.RLock()
	defer ts.connectionsMu.RUnlock()

	var active []*libnet.PeerConnection
	for _, pc := range ts.Connections {
		// Only include peers that have completed the connection setup
		if pc.Connection != nil {
			active = append(active, pc)
		}
	}
	return active
}
