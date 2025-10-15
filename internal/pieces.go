package internal

import (
	"crypto/sha1"
	"sync"
)

type Piece struct {
	Index       int
	Length      int64          // Total piece length in bytes
	Hash        [20]byte       // Expected SHA1 from .torrent
	BlockSize   int64          // Usually 16KB (16384 bytes)
	TotalBlocks int            // How many blocks in this piece
	Blocks      map[int][]byte // blockIndex -> block data
	Complete    bool           // Set to true once all blocks have been downloaded
}

type PieceManager struct {
	mu sync.RWMutex

	TotalPieces     int
	PieceLength     int64 // Default piece length
	LastPieceLength int64 // Last piece may be smaller
	Hashes          [][20]byte // SHA1 hash for each piece
	BlockSize       int64 // Standard block size (16KB)
	MaxBlocksPerPiece int // Maximum blocks in a piece (for blockID encoding)

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
func NewPieceManager(totalPieces int, pieceLength, lastPieceLength int64, hashes [][20]byte, blockSize int64) *PieceManager {
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
	if len(piece.Blocks) == piece.TotalBlocks {
		piece.Complete = true
		return true
	}

	return false
}

// InitializePiece creates a new piece ready for downloading.
// Uses the hash stored in the manager and calculates the piece length.
func (pm *PieceManager) InitializePiece(index int, blockSize int64) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Don't reinitialize if already exists
	if pm.Pieces[index] != nil {
		return
	}

	// Determine piece length (last piece may be shorter)
	var pieceLength int64
	if index == pm.TotalPieces-1 {
		pieceLength = pm.LastPieceLength
	} else {
		pieceLength = pm.PieceLength
	}

	totalBlocks := int((pieceLength + blockSize - 1) / blockSize)

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
	for i := 0; i < piece.TotalBlocks; i++ {
		data = append(data, piece.Blocks[i]...)
	}

	// Calculate SHA1 hash
	hash := sha1.Sum(data)
	return hash == piece.Hash
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
	for i := 0; i < piece.TotalBlocks; i++ {
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
