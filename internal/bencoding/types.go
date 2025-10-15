package bencoding

import (
	"fmt"
)
// BenType represents the type of a bencoded object
type BenType string

const (
	BenString  BenType = "BenString"
	BenInteger BenType = "BenInteger"
	BenList    BenType = "BenList"
	BenDict    BenType = "BenDict"
)

// BencodedObject is a dynamic type container, which allows us to have e.g. lists or dictionaries
// with values of alternating types. @NOTE : We could instead of this, use the interface approach
// and reduce the allocations (from the heap-pointers) but since most .torrent files are relatively
// small the self-documentation, compile-time type-checking, and very clear definition of this
// approach is what we're going to prefer
type BencodedObject struct {
	Typ    BenType
	IntVal *int64
	StrVal *string
	List   []BencodedObject
	Dict   map[string]BencodedObject
}

// FileMode represents whether a torrent contains a single file or multiple files
type FileMode string

const (
	SingleFileMode FileMode = "SingleFileMode"
	MultiFileMode  FileMode = "MultiFileMode"
)

// TorrentFile represents a parsed .torrent file
type TorrentFile struct {
	Data      map[string]BencodedObject
	TFileMode FileMode
	InfoBytes []byte   // Raw bencoded bytes of the info dict
	InfoHash  [20]byte // SHA1 hash of InfoBytes
}

// PeerStruct represents a peer from a tracker response
type PeerStruct struct {
	PeerAddress string
	PeerPort    int64
	PeerID      string
}

func (p *PeerStruct) AddrPort() string {
	return fmt.Sprintf("%s:%d", p.PeerAddress, p.PeerPort)
}
