package internal

import (
	"crypto/sha1"
	"sync"
)

type Piece struct {
	Index       int
	Length      int32          // Total piece length in bytes
	Hash        [20]byte       // Expected SHA1 from .torrent
	BlockSize   int32          // Usually 16KB (16384 bytes)
	TotalBlocks int32          // How many blocks in this piece
	Blocks      map[int][]byte // blockIndex -> block data
	Complete    bool           // Set to true once all blocks have been downloaded
}

type PieceManager struct {
	mu sync.RWMutex

	TotalPieces       int
	PieceLength       int32      // Default piece length
	LastPieceLength   int32      // Last piece may be smaller
	Hashes            [][20]byte // SHA1 hash for each piece
	BlockSize         int32      // Standard block size (16KB)
	MaxBlocksPerPiece int        // Maximum blocks in a piece (for blockID encoding)

	// Single source of truth: contains all pieces (in progress + complete)
	// Piece doesn't exist = not started
	// Piece exists with incomplete blocks = in progress
	// Piece exists with all blocks + verified = complete
	Pieces map[int]*Piece

	// Global pending tracking: blocks currently being requested from ANY peer
	// Key is encoded as: pieceIndex * MaxBlocksPerPiece + blockIndex
	PendingBlocks map[int]struct{}
}

// NewPieceManager creates a new PieceManager with piece information.
func NewPieceManager(totalPieces int, pieceLength, lastPieceLength int32, hashes [][20]byte, blockSize int32) *PieceManager {
	maxBlocksPerPiece := int((pieceLength + blockSize - 1) / blockSize)

	return &PieceManager{
		TotalPieces:       totalPieces,
		PieceLength:       pieceLength,
		LastPieceLength:   lastPieceLength,
		Hashes:            hashes,
		BlockSize:         blockSize,
		MaxBlocksPerPiece: maxBlocksPerPiece,
		Pieces:            make(map[int]*Piece),
		PendingBlocks:     make(map[int]struct{}),
	}
}

// AddBlock adds a block to a piece and removes it from pending.
// Returns true if the piece is now complete.
func (pm *PieceManager) AddBlock(pieceIndex, blockIndex int, data []byte) bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Initialize piece if it doesn't exist
	if pm.Pieces[pieceIndex] == nil {
		return false // Piece wasn't initialized, shouldn't happen
	}

	piece := pm.Pieces[pieceIndex]
	piece.Blocks[blockIndex] = data

	// Remove from pending blocks
	blockID := pieceIndex*pm.MaxBlocksPerPiece + blockIndex
	delete(pm.PendingBlocks, blockID)

	// Check if piece is now complete
	if len(piece.Blocks) == int(piece.TotalBlocks) {
		piece.Complete = true
		return true
	}

	return false
}

// InitializePiece creates a new piece ready for downloading.
// Uses the hash stored in the manager and calculates the piece length.
func (pm *PieceManager) InitializePiece(index int, blockSize int32) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Don't reinitialize if already exists
	if pm.Pieces[index] != nil {
		return
	}

	// Determine piece length (last piece may be shorter)
	var pieceLength int32
	if index == pm.TotalPieces-1 {
		pieceLength = pm.LastPieceLength
	} else {
		pieceLength = pm.PieceLength
	}

	totalBlocks := int32((pieceLength + blockSize - 1) / blockSize)

	pm.Pieces[index] = &Piece{
		Index:       index,
		Length:      pieceLength,
		Hash:        pm.Hashes[index],
		BlockSize:   blockSize,
		TotalBlocks: totalBlocks,
		Blocks:      make(map[int][]byte),
		Complete:    false,
	}
}

// VerifyPiece checks if the piece's SHA1 hash matches the expected hash.
func (pm *PieceManager) VerifyPiece(pieceIndex int) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	piece := pm.Pieces[pieceIndex]
	if piece == nil || !piece.Complete {
		return false
	}

	// Assemble all blocks in order
	data := make([]byte, 0, piece.Length)
	for i := 0; int32(i) < piece.TotalBlocks; i++ {
		data = append(data, piece.Blocks[i]...)
	}

	// Calculate SHA1 hash
	hash := sha1.Sum(data)
	return hash == piece.Hash
}

func (pm *PieceManager) RemovePiece(pieceIndex int) {
	// if the verification of the piece failed, our naive solution is just to remove the entire piece
	// and re-download it all
	pm.mu.Lock()
	defer pm.mu.Unlock()
	delete(pm.Pieces, pieceIndex)
}

// IsPieceComplete checks if all blocks have been downloaded for a piece.
func (pm *PieceManager) IsPieceComplete(pieceIndex int) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	piece := pm.Pieces[pieceIndex]
	if piece == nil {
		return false
	}

	return piece.Complete
}

// IsPieceInProgress checks if a piece has been started but not completed.
func (pm *PieceManager) IsPieceInProgress(pieceIndex int) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	piece := pm.Pieces[pieceIndex]
	return piece != nil && !piece.Complete
}

// GetPieceData assembles and returns the complete piece data.
// Only call this after verifying the piece!
func (pm *PieceManager) GetPieceData(pieceIndex int) []byte {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	piece := pm.Pieces[pieceIndex]
	if piece == nil || !piece.Complete {
		return nil
	}

	// Assemble all blocks in order
	data := make([]byte, 0, piece.Length)
	for i := 0; int32(i) < piece.TotalBlocks; i++ {
		data = append(data, piece.Blocks[i]...)
	}

	return data
}

// HasBlock checks if we have received a specific block.
func (pm *PieceManager) HasBlock(pieceIndex, blockIndex int) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	piece := pm.Pieces[pieceIndex]
	if piece == nil {
		return false
	}

	_, exists := piece.Blocks[blockIndex]
	return exists
}

// MarkBlockPending marks a block as pending (being requested).
func (pm *PieceManager) MarkBlockPending(pieceIndex, blockIndex int) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	blockID := pieceIndex*pm.MaxBlocksPerPiece + blockIndex
	pm.PendingBlocks[blockID] = struct{}{}
}

// UnmarkBlockPending removes a block from pending (e.g., on error).
func (pm *PieceManager) UnmarkBlockPending(pieceIndex, blockIndex int) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	blockID := pieceIndex*pm.MaxBlocksPerPiece + blockIndex
	delete(pm.PendingBlocks, blockID)
}

// TotalSize returns the total size of all pieces in bytes (int64 for large torrents).
func (pm *PieceManager) TotalSize() int64 {
	// All regular pieces + last piece
	return int64(pm.TotalPieces-1)*int64(pm.PieceLength) + int64(pm.LastPieceLength)
}

// BytesRemaining returns how many bytes are left to download (int64 for large torrents).
func (pm *PieceManager) BytesRemaining() int64 {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	var downloaded int64
	for _, piece := range pm.Pieces {
		if piece.Complete {
			downloaded += int64(piece.Length)
		}
	}

	return pm.TotalSize() - downloaded
}

// CompletedPieces returns the number of completed pieces.
func (pm *PieceManager) CompletedPieces() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	count := 0
	for _, piece := range pm.Pieces {
		if piece.Complete {
			count++
		}
	}
	return count
}

// IsComplete returns true if all pieces are downloaded and verified.
func (pm *PieceManager) IsComplete() bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	// Count completed pieces inline to avoid double-locking
	count := 0
	for _, piece := range pm.Pieces {
		if piece.Complete {
			count++
		}
	}

	return len(pm.Pieces) == pm.TotalPieces && count == pm.TotalPieces
}
