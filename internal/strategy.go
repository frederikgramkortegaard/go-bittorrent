package internal

import "gotorrent/internal/libnet"

// SelectNextPiece picks the next piece to download from a peer (sequential strategy).
// Returns pieceIndex and true if found, or -1 and false if nothing available.
// This will return pieces that are in progress if they have blocks available.
func SelectNextPiece(pm *PieceManager, peer *libnet.PeerConnection) (int, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	// Sequential strategy: just go 0, 1, 2, 3...
	for pieceIndex := 0; pieceIndex < pm.TotalPieces; pieceIndex++ {
		// Already have this piece complete?
		piece := pm.Pieces[pieceIndex]
		if piece != nil && piece.Complete {
			continue
		}

		// Does peer have this piece?
		if !peer.HasPiece(pieceIndex) {
			continue
		}

		// Piece not started yet, or in progress - either way, check if it has blocks we can request
		// We'll check in GetNextBlockToRequest if there are actually available blocks
		return pieceIndex, true
	}

	return -1, false // No pieces available from this peer
}

// GetNextBlockToRequest finds the next block that needs to be requested for a piece.
// Returns blockIndex, begin offset, length, and true if found.
// Returns -1, 0, 0, false if no blocks available.
func GetNextBlockToRequest(pm *PieceManager, pieceIndex int) (blockIndex int, begin int32, length int32, ok bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	piece := pm.Pieces[pieceIndex]
	if piece == nil || piece.Complete {
		return -1, 0, 0, false
	}

	// Find first block that we don't have AND is not pending
	for blockIdx := 0; blockIdx < int(piece.TotalBlocks); blockIdx++ {
		// Already have this block?
		if _, exists := piece.Blocks[blockIdx]; exists {
			continue
		}

		// Is it pending (being requested by someone)?
		blockID := pieceIndex*pm.MaxBlocksPerPiece + blockIdx
		if _, pending := pm.PendingBlocks[blockID]; pending {
			continue
		}

		// Calculate offset and length
		begin = int32(blockIdx) * piece.BlockSize
		length = piece.BlockSize

		// Last block might be smaller
		if begin+length > piece.Length {
			length = piece.Length - begin
		}

		return blockIdx, begin, length, true
	}

	return -1, 0, 0, false // No blocks available
}
