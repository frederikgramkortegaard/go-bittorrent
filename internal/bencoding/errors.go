package bencoding

import "errors"

var (
	// String parsing errors
	ErrNoColonInString       = errors.New("no colon found in bencoded string")
	ErrNonDigitInLength      = errors.New("expected ASCII digit in length")
	ErrStringLengthExceeds   = errors.New("string length exceeds data bounds")

	// Integer parsing errors
	ErrIntegerTooShort       = errors.New("data is too short to represent a bencoded integer")
	ErrIntegerNoStartMarker  = errors.New("bencoded integer does not start with 'i'")
	ErrIntegerNoEndMarker    = errors.New("no 'e' terminator found")
	ErrIntegerLeadingZeros   = errors.New("leading zeros not allowed")
	ErrIntegerNegativeZero   = errors.New("negative zero not allowed")

	// List parsing errors
	ErrListTooShort          = errors.New("data is not long enough to contain a list")
	ErrListNoStartMarker     = errors.New("improperly formatted list, does not start with 'l'")
	ErrListNoEndMarker       = errors.New("list not terminated with 'e'")

	// Dictionary parsing errors
	ErrDictTooShort          = errors.New("data is not long enough to contain a dict")
	ErrDictNoStartMarker     = errors.New("improperly formatted dict, does not start with 'd'")
	ErrDictNoEndMarker       = errors.New("dict not terminated with 'e'")
	ErrNotADict              = errors.New("not a dict")
	ErrInfoKeyNotFound       = errors.New("info key not found")

	// General parsing errors
	ErrEmptyData             = errors.New("empty data")

	// Peer extraction errors
	ErrPeersNotFound         = errors.New("peers not found in data")
	ErrInvalidCompactPeerLen = errors.New("invalid compact peer data length (must be multiple of 6)")
	ErrPeersNotList          = errors.New("list of peers has no bencoded list element")

	// Torrent file errors
	ErrPieceLengthNotFound   = errors.New("piece length not found in torrent file")
)
