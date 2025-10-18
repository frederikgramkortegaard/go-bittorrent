package daemon

import (
	"fmt"
	"gotorrent/internal"
	"gotorrent/internal/bencoding"
	"gotorrent/internal/config"
	"os"
	"path/filepath"
	"sync"
)

// WriteRequest represents a request to write a piece to disk.
type WriteRequest struct {
	PieceIndex int
	Data       []byte
}

// FileEntry represents a file in the torrent with its metadata and write state.
type FileEntry struct {
	Path           string // Full path including outputDir and base directory
	TotalLength    int    // Total bytes in this file
	BytesWritten   int    // How many bytes written so far
	BytesRemaining int    // How many bytes left to write
}

// DiskManager handles persistent storage of torrent data.
// It's responsible for writing downloaded pieces to disk and reading pieces for seeding.
type DiskManager struct {
	torrentFile bencoding.TorrentFile
	outputDir   string

	// File structure extracted from torrent metadata
	files []*FileEntry

	// Write queue for async I/O
	writeQueue chan WriteRequest
	done       chan struct{} // Signal to stop write worker
	wg         sync.WaitGroup // Track pending writes
}

// NewDiskManager creates a new DiskManager for a torrent and starts the write worker.
func NewDiskManager(torrentFile bencoding.TorrentFile, outputDir string, cfg *config.Config) *DiskManager {
	dm := &DiskManager{
		torrentFile: torrentFile,
		outputDir:   outputDir,
		writeQueue:  make(chan WriteRequest, cfg.DiskWriteQueueSize),
		done:        make(chan struct{}),
	}

	// Extract file structure from torrent metadata
	dm.files = dm.extractFileStructure()

	// Start write worker goroutine
	go dm.writeWorker()

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

		files = append(files, &FileEntry{
			Path:           path,
			TotalLength:    length,
			BytesWritten:   0,
			BytesRemaining: length,
		})

		return files
	}

	// Multi-file mode - check for base directory
	var baseDir string
	if nameObj, ok := infoDict["name"]; ok && nameObj.StrVal != nil {
		baseDir = filepath.Join(dm.outputDir, *nameObj.StrVal)
	} else {
		baseDir = dm.outputDir
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
			Path:           path,
			TotalLength:    length,
			BytesWritten:   0,
			BytesRemaining: length,
		})
	}

	return files
}

// writeWorker processes write requests from the queue asynchronously.
func (dm *DiskManager) writeWorker() {
	for {
		select {
		case <-dm.done:
			// Drain any remaining writes before exiting
			for len(dm.writeQueue) > 0 {
				req := <-dm.writeQueue
				dm.WritePiece(req.PieceIndex, req.Data)
				dm.wg.Done()
			}
			return

		case req := <-dm.writeQueue:
			_ = dm.WritePiece(req.PieceIndex, req.Data)
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

// WritePiece writes a complete verified piece to disk.
// For multi-file torrents, this may span multiple files.
// This is called by writeWorker, so it runs on the async worker goroutine.
func (dm *DiskManager) WritePiece(index int, data []byte) error {
	dataOffset := 0
	dataRemaining := len(data)

	for _, file := range dm.files {
		// Skip files that are already complete
		if file.BytesRemaining == 0 {
			continue
		}

		// How much of this piece belongs to this file?
		bytesToWrite := dataRemaining
		if bytesToWrite > file.BytesRemaining {
			bytesToWrite = file.BytesRemaining
		}

		if bytesToWrite == 0 {
			break // No more data to write
		}

		// Extract the chunk for this file
		chunk := data[dataOffset : dataOffset+bytesToWrite]

		// Ensure directory exists
		dir, _ := filepath.Split(file.Path)
		if dir != "" {
			if err := os.MkdirAll(dir, os.ModePerm); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", dir, err)
			}
		}

		// Open file in append mode (or create if doesn't exist)
		f, err := os.OpenFile(file.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", file.Path, err)
		}

		// Write chunk
		n, err := f.Write(chunk)
		f.Close()
		if err != nil {
			return fmt.Errorf("failed to write to file %s: %w", file.Path, err)
		}

		// Update file state
		file.BytesWritten += n
		file.BytesRemaining -= n

		fmt.Printf("Wrote %d bytes to %s (total: %d/%d)\n",
			n, file.Path, file.BytesWritten, file.TotalLength)

		// Move to next chunk
		dataOffset += bytesToWrite
		dataRemaining -= bytesToWrite

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
func (dm *DiskManager) WriteToDisk(pm *internal.PieceManager) error {
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

// Close closes the DiskManager and releases resources.
func (dm *DiskManager) Close() error {
	// Signal write worker to stop
	close(dm.done)
	// Wait for any remaining writes to drain
	dm.wg.Wait()
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
