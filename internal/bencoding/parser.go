// Parses .torrent files
// https://wiki.theory.org/BitTorrentSpecification#Metainfo_File_Structure
package bencoding

import "errors"

type FileMode string

const (
	SingleFileMode FileMode = "SingleFileMode"
	MultiFileMode  FileMode = "MultiFileMode"
)

type TorrentFile struct {
	Data      map[string]BencodedObject
	TFileMode FileMode
}

// @TODO : Maybe make a validator, e.g. that each piece has a sha etc.
func ValidateTorrentFile(tf *TorrentFile) error {
	return nil
}
func ParseTorrentFile(data string) (TorrentFile, error) {

	metainfo, _, err := ParseDict(data)
	if err != nil {
		return TorrentFile{}, err
	}

	var fmode FileMode = SingleFileMode

	val, ok := metainfo["info"]
	if !ok {
		return TorrentFile{}, errors.New("Invalid metainfo section")
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
	}

	if err := ValidateTorrentFile(&tf); err != nil {
		return TorrentFile{}, err
	}
	return tf, nil
}
