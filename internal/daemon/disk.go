package daemon

import (
	"fmt"
	"os"
	"path/filepath"

	"gotorrent/internal"
	"gotorrent/internal/bencoding"
)

// WriteRequest represents a request to write a piece to disk.
type WriteRequest struct {
	PieceIndex int
	Data       []byte
}

// DiskManager handles persistent storage of torrent data.
// It's responsible for writing downloaded pieces to disk and reading pieces for seeding.
type DiskManager struct {
	torrentFile bencoding.TorrentFile
	outputDir   string

	// Write queue for async I/O
	writeQueue chan WriteRequest
	done       chan struct{} // Signal to stop write worker
}

// NewDiskManager creates a new DiskManager for a torrent and starts the write worker.
func NewDiskManager(torrentFile bencoding.TorrentFile, outputDir string) *DiskManager {
	dm := &DiskManager{
		torrentFile: torrentFile,
		outputDir:   outputDir,
		writeQueue:  make(chan WriteRequest, 100), // Buffer 100 write requests
		done:        make(chan struct{}),
	}

	// Start write worker goroutine
	go dm.writeWorker()

	return dm
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
			}
			return

		case req := <-dm.writeQueue:
			// @TODO: Actually write the piece to disk
			_ = dm.WritePiece(req.PieceIndex, req.Data)
		}
	}
}

// QueueWrite queues a piece to be written to disk asynchronously.
func (dm *DiskManager) QueueWrite(pieceIndex int, data []byte) {
	dm.writeQueue <- WriteRequest{
		PieceIndex: pieceIndex,
		Data:       data,
	}
}

// WritePiece writes a complete verified piece to disk.
// For multi-file torrents, this may span multiple files.
func (dm *DiskManager) WritePiece(index int, data []byte) error {
	// @TODO: Implement disk write logic
	// - Calculate file offset(s) for this piece
	// - Handle single-file vs multi-file mode
	// - Write data to appropriate file(s)
	// - Handle partial writes across file boundaries
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

// WriteToDisk writes all pieces to disk at once.
// This is a simple implementation for initial testing.
func (dm *DiskManager) WriteToDisk(pm *internal.PieceManager) error {
	// Assemble all pieces in order
	var allData []byte
	for i := 0; i < pm.TotalPieces; i++ {
		data := pm.GetPieceData(i)
		if data == nil {
			return fmt.Errorf("missing piece %d", i)
		}
		allData = append(allData, data...)
	}

	// Get filename from info dict
	nameObj, ok := dm.torrentFile.Data["info"].Dict["name"]
	if !ok || nameObj.StrVal == nil {
		return fmt.Errorf("name not found in torrent info")
	}
	filename := *nameObj.StrVal

	// Create output path
	outputPath := filepath.Join(dm.outputDir, filename)

	// Write file
	fmt.Printf("Writing file: %s (%d bytes)\n", filename, len(allData))
	err := os.WriteFile(outputPath, allData, 0644)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// Flush ensures all pending writes are completed.
func (dm *DiskManager) Flush() error {
	// @TODO: If using async write queue, wait for all writes to complete
	return nil
}

// Close closes the DiskManager and releases resources.
func (dm *DiskManager) Close() error {
	close(dm.done)
	// @TODO: Close file handles, etc.
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
