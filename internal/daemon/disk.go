package daemon

import (
	"fmt"
	"go-bittorrent/internal/bencoding"
	"go-bittorrent/internal/config"
	"os"
	"path/filepath"
	"sync"
)

// WriteRequest represents a request to write a piece to disk.
type WriteRequest struct {
	PieceIndex int
	Data       []byte
}

// FileEntry represents a file in the torrent with its metadata.
type FileEntry struct {
	Path        string // Full path including outputDir and base directory
	TotalLength int    // Total bytes in this file
}

// DiskManager handles persistent storage of torrent data.
// It's responsible for writing downloaded pieces to disk and reading pieces for seeding.
type DiskManager struct {
	torrentFile   bencoding.TorrentFile
	outputDir     string
	torrentDir    string // Actual directory where torrent files are stored (includes torrent name for multi-file)

	// File structure extracted from torrent metadata
	files []*FileEntry

	// Write queue for async I/O (only used during downloads)
	writeQueue      chan WriteRequest
	writeWorkerDone chan struct{} // Signal to stop write worker
	wg              sync.WaitGroup // Track pending writes
	workerRunning   bool           // Track if worker is running
	workerMu        sync.Mutex     // Protect worker state
}

// NewDiskManager creates a new DiskManager for a torrent.
// The write worker is NOT started by default - call StartWriteWorker() when needed.
func NewDiskManager(torrentFile bencoding.TorrentFile, outputDir string, cfg *config.Config) *DiskManager {
	dm := &DiskManager{
		torrentFile:     torrentFile,
		outputDir:       outputDir,
		writeQueue:      make(chan WriteRequest, cfg.DiskWriteQueueSize),
		writeWorkerDone: make(chan struct{}),
		workerRunning:   false,
	}

	// Extract file structure from torrent metadata
	dm.files = dm.extractFileStructure()

	// Print file structure for debugging
	fmt.Printf("DiskManager initialized with %d files:\n", len(dm.files))
	for i, file := range dm.files {
		fmt.Printf("  File %d: %s (%d bytes)\n", i, file.Path, file.TotalLength)
	}

	return dm
}

// extractFileStructure builds the file list from torrent metadata.
func (dm *DiskManager) extractFileStructure() []*FileEntry {
	infoDict := dm.torrentFile.Data["info"].Dict
	var files []*FileEntry

	// Check for "files" field (multi-file mode)
	filesObj, ok := infoDict["files"]
	if !ok || filesObj.List == nil || len(filesObj.List) == 0 {
		// Single-file mode - use "name" field
		nameObj, ok := infoDict["name"]
		if !ok || nameObj.StrVal == nil {
			return files // Empty list on error
		}

		// Get file length
		lengthObj, ok := infoDict["length"]
		if !ok || lengthObj.IntVal == nil {
			return files
		}
		length := int(*lengthObj.IntVal)

		filename := *nameObj.StrVal
		path := filepath.Join(dm.outputDir, filename)

		// For single-file, torrentDir is just outputDir (file is directly in output)
		dm.torrentDir = dm.outputDir

		files = append(files, &FileEntry{
			Path:        path,
			TotalLength: length,
		})

		return files
	}

	// Multi-file mode - check for base directory
	var baseDir string
	if nameObj, ok := infoDict["name"]; ok && nameObj.StrVal != nil {
		baseDir = filepath.Join(dm.outputDir, *nameObj.StrVal)
		dm.torrentDir = baseDir // For multi-file, torrentDir includes the torrent name
	} else {
		baseDir = dm.outputDir
		dm.torrentDir = dm.outputDir
	}

	// Extract all files
	for _, curFile := range filesObj.List {
		pathObj, ok := curFile.Dict["path"]
		if !ok || pathObj.List == nil || len(pathObj.List) == 0 {
			continue
		}
		if pathObj.List[0].StrVal == nil {
			continue
		}

		path := *pathObj.List[0].StrVal
		path = filepath.Join(baseDir, path)

		lengthObj, ok := curFile.Dict["length"]
		if !ok || lengthObj.IntVal == nil {
			continue
		}
		length := int(*lengthObj.IntVal)

		files = append(files, &FileEntry{
			Path:        path,
			TotalLength: length,
		})
	}

	return files
}

// writeWorker processes write requests from the queue asynchronously.
// Runs until writeWorkerDone is closed or writeQueue is closed.
func (dm *DiskManager) writeWorker() {
	defer func() {
		dm.workerMu.Lock()
		dm.workerRunning = false
		dm.workerMu.Unlock()
	}()

	for {
		select {
		case <-dm.writeWorkerDone:
			// Shutdown signal - drain remaining writes and exit
			for {
				select {
				case req := <-dm.writeQueue:
					err := dm.WritePiece(req.PieceIndex, req.Data)
					if err != nil {
						fmt.Printf("ERROR writing piece %d to disk: %v\n", req.PieceIndex, err)
					}
					dm.wg.Done()
				default:
					// Queue empty, exit
					return
				}
			}
		case req := <-dm.writeQueue:
			err := dm.WritePiece(req.PieceIndex, req.Data)
			if err != nil {
				fmt.Printf("ERROR writing piece %d to disk: %v\n", req.PieceIndex, err)
			}
			dm.wg.Done()
		}
	}
}

// QueueWrite queues a piece to be written to disk asynchronously.
func (dm *DiskManager) QueueWrite(pieceIndex int, data []byte) {
	dm.wg.Add(1)
	dm.writeQueue <- WriteRequest{
		PieceIndex: pieceIndex,
		Data:       data,
	}
}

// WritePiece writes a complete verified piece to disk at the correct offset.
// For multi-file torrents, this may span multiple files.
// This is called by writeWorker, so it runs on the async worker goroutine.
func (dm *DiskManager) WritePiece(index int, data []byte) error {
	infoDict := dm.torrentFile.Data["info"].Dict

	// Get piece length
	pieceLengthObj, ok := infoDict["piece length"]
	if !ok || pieceLengthObj.IntVal == nil {
		return fmt.Errorf("piece length not found in torrent metadata")
	}
	pieceLength := int64(*pieceLengthObj.IntVal)

	// Calculate the absolute byte offset for this piece in the torrent
	pieceOffset := int64(index) * pieceLength

	// Track position within the piece data
	dataOffset := 0
	dataRemaining := len(data)

	// Calculate which file(s) this piece belongs to
	var currentFileOffset int64 = 0 // Absolute offset in torrent

	for _, file := range dm.files {
		fileStart := currentFileOffset
		fileEnd := currentFileOffset + int64(file.TotalLength)

		// Check if this piece overlaps with this file
		pieceEnd := pieceOffset + int64(len(data))

		if pieceEnd <= fileStart || pieceOffset >= fileEnd {
			// Piece doesn't touch this file
			currentFileOffset = fileEnd
			continue
		}

		// Calculate the overlap between piece and file
		writeStart := pieceOffset
		if writeStart < fileStart {
			writeStart = fileStart
		}

		writeEnd := pieceEnd
		if writeEnd > fileEnd {
			writeEnd = fileEnd
		}

		bytesToWrite := int(writeEnd - writeStart)
		if bytesToWrite == 0 {
			currentFileOffset = fileEnd
			continue
		}

		// Calculate offset within the file
		fileWriteOffset := writeStart - fileStart

		// Calculate offset within the piece data
		pieceDataOffset := writeStart - pieceOffset

		// Extract the chunk to write
		chunk := data[pieceDataOffset : pieceDataOffset+int64(bytesToWrite)]

		// Ensure directory exists
		dir, _ := filepath.Split(file.Path)
		if dir != "" {
			if err := os.MkdirAll(dir, os.ModePerm); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", dir, err)
			}
		}

		// Open file for writing at specific offset
		f, err := os.OpenFile(file.Path, os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", file.Path, err)
		}

		// Write chunk at the correct offset
		n, err := f.WriteAt(chunk, fileWriteOffset)
		f.Close()
		if err != nil {
			return fmt.Errorf("failed to write to file %s: %w", file.Path, err)
		}

		fmt.Printf("Wrote piece %d (%d bytes) to %s at offset %d\n",
			index, n, filepath.Base(file.Path), fileWriteOffset)

		dataOffset += bytesToWrite
		dataRemaining -= bytesToWrite
		currentFileOffset = fileEnd

		if dataRemaining == 0 {
			break
		}
	}

	return nil
}

// ReadPiece reads a piece from disk (for seeding).
func (dm *DiskManager) ReadPiece(index int) ([]byte, error) {
	// @TODO: Implement disk read logic
	// - Calculate file offset(s) for this piece
	// - Read data from appropriate file(s)
	// - Handle partial reads across file boundaries
	return nil, nil
}

// WriteToDisk writes all pieces to disk at once using the writeQueue.
// This queues all pieces for writing by the async worker.
func (dm *DiskManager) WriteToDisk(pm *PieceManager) error {
	// Queue all pieces for writing
	for i := 0; i < pm.TotalPieces; i++ {
		data := pm.GetPieceData(i)
		if data == nil {
			return fmt.Errorf("missing piece %d", i)
		}
		dm.QueueWrite(i, data)
	}

	// Wait for all writes to complete
	dm.wg.Wait()

	return nil
}

// Flush ensures all pending writes are completed.
func (dm *DiskManager) Flush() error {
	// Wait for all pending writes to complete
	dm.wg.Wait()
	return nil
}

// StartWriteWorker starts the background write worker.
// Safe to call multiple times - only starts if not already running.
func (dm *DiskManager) StartWriteWorker() {
	dm.workerMu.Lock()
	defer dm.workerMu.Unlock()

	if dm.workerRunning {
		return // Already running
	}

	dm.workerRunning = true
	go dm.writeWorker()
}

// StopWriteWorker stops the background write worker and waits for pending writes.
// Call this when download completes but you still want to use DiskManager for reads.
func (dm *DiskManager) StopWriteWorker() error {
	dm.workerMu.Lock()
	if !dm.workerRunning {
		dm.workerMu.Unlock()
		return nil // Already stopped
	}
	dm.workerMu.Unlock()

	// Signal worker to stop
	close(dm.writeWorkerDone)
	// Wait for all pending writes to complete
	dm.wg.Wait()

	return nil
}

// Close closes the DiskManager and releases all resources.
// Call this when the entire TorrentSession is being removed.
func (dm *DiskManager) Close() error {
	// Stop write worker if running
	dm.StopWriteWorker()
	return nil
}

// VerifyOnDisk checks if a piece exists on disk and matches the expected hash.
// Useful for resuming downloads.
func (dm *DiskManager) VerifyOnDisk(index int, expectedHash [20]byte) (bool, error) {
	// @TODO: Read piece from disk and verify hash
	// - Read piece data
	// - Calculate SHA1
	// - Compare with expectedHash
	return false, nil
}

// GetOutputDir returns the actual directory where torrent files are stored.
// For multi-file torrents, this includes the torrent name subdirectory.
// For single-file torrents, this is just the output directory.
func (dm *DiskManager) GetOutputDir() string {
	return dm.torrentDir
}
