// Parses .torrent files
// https://wiki.theory.org/BitTorrentSpecification#Metainfo_File_Structure
package bencoding

import (
	"crypto/sha1"
	"errors"
)

type FileMode string

const (
	SingleFileMode FileMode = "SingleFileMode"
	MultiFileMode  FileMode = "MultiFileMode"
)

type TorrentFile struct {
	Data      map[string]BencodedObject
	TFileMode FileMode
	InfoBytes []byte   // Raw bencoded bytes of the info dict
	InfoHash  [20]byte // SHA1 hash of InfoBytes
}

// ValidateTorrentFile ensures that all require fields exists in a given TorrentFile @TODO : Maybe make a validator, e.g. that each piece has a sha etc.
func ValidateTorrentFile(tf *TorrentFile) error {
	return nil
}

func ParseTorrentFile(data string) (TorrentFile, error) {

	metainfo, _, err := ParseDict(data)
	if err != nil {
		return TorrentFile{}, err
	}

	// Extract raw info dict bytes for hash calculation
	infoBytes, err := ExtractInfoDictBytes(data)
	if err != nil {
		return TorrentFile{}, err
	}

	// Calculate info hash
	infoHash := sha1.Sum(infoBytes)

	var fmode = SingleFileMode

	val, ok := metainfo["info"]
	if !ok {
		return TorrentFile{}, errors.New("invalid metainfo section")
	}

	if val.Typ != BenDict {
		return TorrentFile{}, errors.New("'info' section in metainfo was not properly parsed as a dictionary")
	}

	if _, ok := val.Dict["files"]; ok {
		// MultiFileMode as 'files' key exists
		fmode = MultiFileMode
	}

	tf := TorrentFile{
		Data:      metainfo,
		TFileMode: fmode,
		InfoBytes: infoBytes,
		InfoHash:  infoHash,
	}

	if err := ValidateTorrentFile(&tf); err != nil {
		return TorrentFile{}, err
	}
	return tf, nil
}
