package daemon

import "errors"

var (
	ErrNoDataField                = errors.New("torrentfile has no data field")
	ErrNoPieceInfo                = errors.New("could not extract pieceInfo from torrentFile")
	ErrBitfieldLengthMismatch     = errors.New("length of bitfield is not the same as the expected length of pieces in pieceInfo")
	ErrNoAnnounceField            = errors.New("no announce field in torrentfile")
	ErrNoActivePeers              = errors.New("no active peers available - all connections failed")
	ErrDownloadAlreadyInProgress  = errors.New("download sequence already in progress for this torrent")
)
