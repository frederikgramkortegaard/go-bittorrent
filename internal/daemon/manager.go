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
	"sync"
	"time"
)

// TorrentManager manages all active torrent sessions for the application.
type TorrentManager struct {
	Sessions   map[[20]byte]*TorrentSession // Maps the InfoHash of a TorrentFile to it's current TorrentSession
	sessionsMu sync.RWMutex                 // Protects Sessions map
	Client     *libnet.Client               // Shared client for all torrents
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

// AllSessionsComplete checks if all torrents have completed downloading.
func (t *TorrentManager) AllSessionsComplete() bool {
	t.sessionsMu.RLock()
	defer t.sessionsMu.RUnlock()

	for _, session := range t.Sessions {
		if !session.PieceManager.IsComplete() {
			return false
		}
	}
	return true
}

// WaitForCompletion blocks until all torrent sessions have completed.
// Uses channels to wait efficiently (no busy-waiting).
func (t *TorrentManager) WaitForCompletion() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		// Check if all complete
		if t.AllSessionsComplete() {
			return
		}

		// Get a done channel to wait on
		t.sessionsMu.RLock()
		var waitChan <-chan struct{}
		for _, session := range t.Sessions {
			if session.doneChan != nil && !session.PieceManager.IsComplete() {
				waitChan = session.doneChan
				break
			}
		}
		t.sessionsMu.RUnlock()

		// If we found a channel, wait on it (or timeout)
		if waitChan != nil {
			select {
			case <-waitChan:
				// A session completed, loop again to check others
			case <-ticker.C:
				// Periodic check in case of race
			}
		} else {
			// No active downloads, just wait a bit
			<-ticker.C
		}
	}
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
		PieceManager: NewPieceManager(
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

	// Set DiskManager logger with session context
	session.DiskManager.Logger = session.Logger.WithPrefix("Disk")

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

	// Calculate piece availability
	// @NOTE : If the files on disk have been deleted, this could be incorrect
	// because of this, when we on program start go through our dotfolder and find every
	// torrentfile, we could/should possible do some validation of the data on-disk,
	pieceInfo, err := bencoding.ExtractPieceInfo(torrentFile)
	if err != nil {
		return nil, ErrNoPieceInfo
	}

	// Validate or initialize bitfield
	expectedBitfieldLen := (pieceInfo.TotalPieces + 7) / 8 // Round up to nearest byte
	setBits := 0

	if torrentFile.Bitfield != nil {
		// Bitfield exists - validate length
		if len(torrentFile.Bitfield) != expectedBitfieldLen {
			return nil, fmt.Errorf("bitfield has incorrect length: got %d bytes, expected %d bytes",
				len(torrentFile.Bitfield), expectedBitfieldLen)
		}


		// @TODO : Verify pieces exist on-disk and SHA checks

		// Initialize PieceManager with already-completed pieces from bitfield
		for i := 0; i < pieceInfo.TotalPieces; i++ {
			byteIndex := i / 8
			bitOffset := uint(i % 8)
			hasPiece := (torrentFile.Bitfield[byteIndex] >> (7 - bitOffset)) & 1 == 1

			if hasPiece {
				setBits++
				// Mark this piece as complete in PieceManager (it's already on disk)
				session.PieceManager.MarkPieceCompleteFromBitfield(i)
			}
		}

		session.Logger.Info("Resuming with %d/%d pieces already downloaded", setBits, pieceInfo.TotalPieces)
	} else {
		// No bitfield - initialize empty one for fresh download
		torrentFile.Bitfield = make([]byte, expectedBitfieldLen)
	}

	// We have at least a single fully completed and verified piece of this torrent on-disk
	session.IsSeedMature = setBits > 0

	// Initiate download if required
	if torrentFile.Bitfield == nil || setBits < pieceInfo.TotalPieces {
		if _, ok := torrentFile.Data["announce"]; !ok {
			return session, ErrNoAnnounceField
		}

		err = session.InitiateDownloadSequence(t, ctx)
		if err != nil {
			return session, err
		}
	}

	// Note: If already have all pieces, no download needed - session is ready for seeding

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
	PieceManager  *PieceManager
	DiskManager   *DiskManager
	Config        *config.Config
	connectionsMu sync.RWMutex
	Logger        *logger.Logger

	// Context for coordinating goroutine shutdown
	ctx    context.Context
	cancel context.CancelFunc

	// Channel-based completion signaling (for synchronization/waiting)
	doneChan     chan struct{}
	doneMu       sync.Mutex // Protects doneChan, doneSignaled, and doneErr
	doneSignaled bool       // True if doneChan has been closed
	doneErr      error      // Error if cancelled/failed, nil if successful completion

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
		msg, err := libnet.ReadMessage(peer.Connection)
		if err != nil {
			// Check if we're shutting down vs actual error
			select {
			case <-ctx.Done():
				peer.Logger.Debug("Read loop shutting down")
			default:
				peer.Logger.Error("Failed to read message: %v", err)
				peer.SetStatus(libnet.StatusFailed)
				ts.RemoveConnection(peer.ConnectionAddress)
			}
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

					// Mark piece complete in bitfield and persist to disk
					err := ts.MarkPieceCompleteAndPersist(receivedPieceIndex)
					if err != nil {
						ts.Logger.Error("Failed to persist piece completion: %v", err)
					}

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
	// Check if a download is already active
	ts.doneMu.Lock()
	if ts.doneChan != nil && !ts.doneSignaled {
		// Download already in progress
		ts.doneMu.Unlock()
		return ErrDownloadAlreadyInProgress
	}

	// Start new download sequence (create/reset doneChan)
	ts.doneChan = make(chan struct{})
	ts.doneSignaled = false
	ts.doneErr = nil
	ts.doneMu.Unlock()

	// Start write worker for this download sequence
	ts.DiskManager.StartWriteWorker()

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

			completedPieces := ts.PieceManager.CompletedPieces()
			totalPieces := ts.PieceManager.TotalPieces

			// Count peer states
			var unchoked, active, failed, disconnected int
			ts.connectionsMu.RLock()
			for _, conn := range ts.Connections {
				status := conn.GetStatus()
				if status == libnet.StatusActive && !conn.PeerChoking.Load() {
					unchoked++
				}
				if status == libnet.StatusActive {
					active++
				}
				if status == libnet.StatusFailed {
					failed++
				}
				if status == libnet.StatusDisconnected {
					disconnected++
				}
			}
			ts.connectionsMu.RUnlock()

			ts.Logger.Info("Health check - Total peers: %d (Active: %d, Unchoked: %d, Failed: %d, Disconnected: %d), Progress: %d/%d pieces (%.1f%%)",
				activePeers, active, unchoked, failed, disconnected, completedPieces, totalPieces, float64(completedPieces)*100.0/float64(totalPieces))

			if activePeers >= 10 {continue}

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
				Peer:        &p,
				RequestChan: make(chan struct{}, ts.Config.RequestPipelineSize),
			}
			pc.SetStatus(libnet.StatusDiscovered)
			pc.AmChoking.Store(true)       // This client is choking the peer initially
			pc.AmInterested.Store(false)   // This client is not interested initially
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

			// Set read deadline for first message (bitfield expected within 10 seconds)
			conn.SetReadDeadline(time.Now().Add(10 * time.Second))

			// Read first message (should be bitfield, but might be something else)
			msg, err := libnet.ReadMessage(pc.Connection)
			if err != nil {
				pc.SetStatus(libnet.StatusFailed)
				pc.Error = fmt.Errorf("failed to read first message: %w", err)
				pc.Logger.Error("Failed to read first message: %v", err)
				ts.AddConnection(pc)
				return
			}

			// Clear read deadline for subsequent messages (handled by PeerReadLoop)
			conn.SetReadDeadline(time.Time{})

			// Handle the message
			if msg.ID != nil && *msg.ID == libnet.MsgBitfield {
				pc.SetBitfield(msg.Payload)
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
	peer.AmInterested.Store(true)
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
			pieceIndex, ok := SelectNextPiece(ts.PieceManager, peer)
			if !ok {
				// No pieces available from this peer
				<-peer.RequestChan

				// Check if torrent is complete
				if ts.PieceManager.IsComplete() {
					peer.Logger.Info("All pieces complete! Torrent should be finishing...")
					return
				}

				peer.Logger.Debug("No pieces available from this peer, waiting...")
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
			blockIndex, begin, length, ok := GetNextBlockToRequest(ts.PieceManager, pieceIndex)
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
	ts.doneMu.Lock()
	if !ts.doneSignaled {
		ts.doneErr = fmt.Errorf("cancelled")
		close(ts.doneChan)
		ts.doneSignaled = true
	}
	ts.doneMu.Unlock()
}

// Complete handles torrent completion - stops peer connections, marks ready for seeding,
// and signals completion via doneChan.
// This is called when all pieces have been acquired and verified.
func (ts *TorrentSession) Complete() error {
	ts.Logger.Info("Torrent complete, all %d pieces acquired", ts.PieceManager.CompletedPieces())

	ts.StopPeerLoops()

	// Stop write worker (but keep DiskManager alive for seeding/reads)
	ts.DiskManager.StopWriteWorker()

	ts.IsSeedMature = true // Mark as ready to seed

	// Signal successful completion via doneChan (doneErr stays nil)
	ts.doneMu.Lock()
	if !ts.doneSignaled {
		close(ts.doneChan)
		ts.doneSignaled = true
	}
	ts.doneMu.Unlock()

	return nil
}

// MarkPieceCompleteAndPersist updates the bitfield to mark a piece as complete
// and persists the updated TorrentFile to disk for resume capability.
func (ts *TorrentSession) MarkPieceCompleteAndPersist(pieceIndex int) error {
	// Initialize bitfield if it doesn't exist
	if ts.TorrentFile.Bitfield == nil {
		// Calculate bitfield size: need one bit per piece, rounded up to bytes
		numPieces := ts.PieceManager.TotalPieces
		bitfieldSize := (numPieces + 7) / 8 // Round up to nearest byte
		ts.TorrentFile.Bitfield = make([]byte, bitfieldSize)
	}

	// Set the bit for this piece
	byteIndex := pieceIndex / 8
	bitOffset := uint(pieceIndex % 8)
	ts.TorrentFile.Bitfield[byteIndex] |= (1 << (7 - bitOffset))

	// Persist the updated torrent file to disk
	err := internal.StoreTorrentFileInDotfolder(ts.TorrentFile, ts.Config)
	if err != nil && err != internal.ErrFileAlreadyExists {
		ts.Logger.Error("Failed to persist bitfield update: %v", err)
		return err
	}

	// If file exists, we need to update it (overwrite)
	if err == internal.ErrFileAlreadyExists {
		err = internal.UpdateTorrentFileInDotfolder(ts.TorrentFile, ts.Config)
		if err != nil {
			ts.Logger.Error("Failed to update bitfield in dotfolder: %v", err)
			return err
		}
	}

	ts.Logger.Debug("Marked piece %d complete in bitfield and persisted", pieceIndex)
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
	if conn, exists := ts.Connections[address]; exists {
		ts.Logger.Info("Removing peer %s (Status: %s)", address, conn.GetStatus())
		delete(ts.Connections, address)
	}
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
