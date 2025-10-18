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
	infoDict := dm.torrentFile.Data["info"].Dict

	// Check for "files" field (multi-file mode or single-file with path)
	filesObj, ok := infoDict["files"]
	if !ok || filesObj.List == nil || len(filesObj.List) == 0 {
		// Single-file mode fallback - use "name" field
		nameObj, ok := infoDict["name"]
		if !ok || nameObj.StrVal == nil {
			return fmt.Errorf("name not found in torrent info")
		}
		filename := *nameObj.StrVal
		outputPath := filepath.Join(dm.outputDir, filename)

		// Write file
		fmt.Printf("Writing file: %s (%d bytes)\n", filename, len(allData))
		err := os.WriteFile(outputPath, allData, 0644)
		if err != nil {
			return fmt.Errorf("failed to write file: %w", err)
		}
		return nil
	}

	// Multi-file mode - write files sequentially from allData
	// Check if there's a "name" field to use as a base directory
	var baseDir string
	if nameObj, ok := infoDict["name"]; ok && nameObj.StrVal != nil {
		baseDir = filepath.Join(dm.outputDir, *nameObj.StrVal)
	} else {
		baseDir = dm.outputDir
	}

	dataOffset := 0
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

		dir, filename := filepath.Split(path)

		// Create directory if it doesn't exist (dir could be empty for files in root)
		if dir != "" {
			if err := os.MkdirAll(dir, os.ModePerm); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", dir, err)
			}
		}

		bytesInFileObj, ok := curFile.Dict["length"]
		if !ok || bytesInFileObj.IntVal == nil {
			return fmt.Errorf("undefined how many bytes should be in file: %s", path)
		}
		bytesInFile := int(*bytesInFileObj.IntVal)

		// Check if we have enough data left
		if dataOffset+bytesInFile > len(allData) {
			return fmt.Errorf("not enough data for file %s: need %d bytes, have %d bytes remaining",
				filename, bytesInFile, len(allData)-dataOffset)
		}

		// Extract this file's data from allData
		fileData := allData[dataOffset : dataOffset+bytesInFile]

		// Write file
		fmt.Printf("Writing file: %s (%d bytes)\n", path, bytesInFile)
		err := os.WriteFile(path, fileData, 0644)
		if err != nil {
			return fmt.Errorf("failed to write file: %w", err)
		}

		// Advance offset for next file
		dataOffset += bytesInFile
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
