package bencoding

import (
	"encoding/base64"
	"encoding/json"
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
	Typ    BenType                   `json:"type"`
	IntVal *int64                    `json:"int_val,omitempty"`
	StrVal *string                   `json:"-"` // Custom marshaling to handle binary data
	List   []BencodedObject          `json:"list,omitempty"`
	Dict   map[string]BencodedObject `json:"dict,omitempty"`
}

// bencodedObjectJSON is used for custom JSON marshaling to handle binary strings
type bencodedObjectJSON struct {
	Typ       BenType                   `json:"type"`
	IntVal    *int64                    `json:"int_val,omitempty"`
	StrValB64 *string                   `json:"str_val_b64,omitempty"` // Base64-encoded binary data
	List      []BencodedObject          `json:"list,omitempty"`
	Dict      map[string]BencodedObject `json:"dict,omitempty"`
}

// MarshalJSON custom marshals BencodedObject, encoding binary strings as base64
func (b BencodedObject) MarshalJSON() ([]byte, error) {
	obj := bencodedObjectJSON{
		Typ:    b.Typ,
		IntVal: b.IntVal,
		List:   b.List,
		Dict:   b.Dict,
	}

	if b.StrVal != nil {
		// Base64 encode the string to preserve binary data
		encoded := base64.StdEncoding.EncodeToString([]byte(*b.StrVal))
		obj.StrValB64 = &encoded
	}

	return json.Marshal(obj)
}

// UnmarshalJSON custom unmarshals BencodedObject, decoding base64 strings
func (b *BencodedObject) UnmarshalJSON(data []byte) error {
	var obj bencodedObjectJSON
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}

	b.Typ = obj.Typ
	b.IntVal = obj.IntVal
	b.List = obj.List
	b.Dict = obj.Dict

	if obj.StrValB64 != nil {
		// Base64 decode the string to restore binary data
		decoded, err := base64.StdEncoding.DecodeString(*obj.StrValB64)
		if err != nil {
			return err
		}
		str := string(decoded)
		b.StrVal = &str
	}

	return nil
}

// FileMode represents whether a torrent contains a single file or multiple files
type FileMode string

const (
	SingleFileMode FileMode = "SingleFileMode"
	MultiFileMode  FileMode = "MultiFileMode"
)

// TorrentFile represents a parsed .torrent file
type TorrentFile struct {
	Data      map[string]BencodedObject `json:"data"`
	TFileMode FileMode                  `json:"file_mode"`
	InfoBytes []byte                    `json:"info_bytes"` // Raw bencoded bytes of the info dict
	InfoHash  [20]byte                  `json:"info_hash"`  // SHA1 hash of InfoBytes

	// Meta
	Bitfield []byte 										`json:"bitfield"` // Used to quickly determine seed mature and missing pieces for existing torrents on-disk
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
