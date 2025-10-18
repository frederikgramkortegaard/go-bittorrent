package bencoding

import (
	"errors"
	"fmt"
)

// PrintObject prints a BencodedObject with proper indentation
func PrintObject(obj BencodedObject, indent int) {
	prefix := ""
	for i := 0; i < indent; i++ {
		prefix += "  "
	}

	switch obj.Typ {
	case BenString:
		if obj.StrVal != nil {
			fmt.Printf("%s\n", *obj.StrVal)
		}
	case BenInteger:
		if obj.IntVal != nil {
			fmt.Printf("%d\n", *obj.IntVal)
		}
	case BenList:
		fmt.Println("[")
		for _, item := range obj.List {
			fmt.Print(prefix + "  ")
			PrintObject(item, indent+1)
		}
		fmt.Println(prefix + "]")
	case BenDict:
		fmt.Println("{")
		PrintDict(obj.Dict, indent+1)
		fmt.Println(prefix + "}")
	}
}

// PrintDict prints a bencoded dictionary with proper indentation
func PrintDict(dict map[string]BencodedObject, indent int) {
	prefix := ""
	for i := 0; i < indent; i++ {
		prefix += "  "
	}

	for key, val := range dict {
		fmt.Printf("%s%s: ", prefix, key)
		PrintObject(val, indent)
	}
}

// ExtractPeersFromTrackerResponse extracts peer information from a tracker response.
func ExtractPeersFromTrackerResponse(data map[string]BencodedObject) ([]PeerStruct, error) {

	peers := make([]PeerStruct, 0, 0)

	if _, ok := data["peers"]; !ok {
		return nil, errors.New("peers not found in data")
	}

	if data["peers"].List == nil {
		return nil, errors.New("list of peers has no bencoded list element")
	}

	for _, elem := range data["peers"].List {

		// Sanity check
		if elem.Dict == nil {
			continue
		}

		// IP Address
		if _, ok := elem.Dict["ip"]; !ok || elem.Dict["ip"].StrVal == nil {
			continue
		}

		peerIP := *elem.Dict["ip"].StrVal

		// Port
		if _, ok := elem.Dict["port"]; !ok  || elem.Dict["port"].IntVal == nil {
			continue
		}

		peerPort := *elem.Dict["port"].IntVal

		peers = append(peers, PeerStruct{
			PeerAddress: peerIP,
			PeerPort: peerPort,
			PeerID: "",
		})

	}

	return peers, nil
}

// PieceInfo contains information about pieces from the torrent file.
type PieceInfo struct {
	PieceLength     int32      // Length of each piece in bytes
	TotalPieces     int        // Total number of pieces
	Hashes          [][20]byte // SHA1 hash for each piece
	LastPieceLength int32      // Length of the last piece (may be smaller)
}

// ExtractPieceInfo extracts piece information from a torrent file.
// The "pieces" field in the info dict contains concatenated 20-byte SHA1 hashes.
func ExtractPieceInfo(torrent TorrentFile) (PieceInfo, error) {
	info := PieceInfo{}

	// Get piece length
	pieceLength, ok := torrent.Data["info"].Dict["piece length"]
	if !ok || pieceLength.IntVal == nil {
		return info, errors.New("piece length not found in torrent file")
	}
	info.PieceLength = int32(*pieceLength.IntVal)

	// Get pieces (concatenated SHA1 hashes)
	piecesObj, ok := torrent.Data["info"].Dict["pieces"]
	if !ok || piecesObj.StrVal == nil {
		return info, errors.New("pieces not found in torrent file")
	}
	piecesBytes := []byte(*piecesObj.StrVal)

	// Each hash is 20 bytes
	if len(piecesBytes)%20 != 0 {
		return info, fmt.Errorf("invalid pieces length: %d (not divisible by 20)", len(piecesBytes))
	}

	info.TotalPieces = len(piecesBytes) / 20
	info.Hashes = make([][20]byte, info.TotalPieces)

	// Split into 20-byte hashes
	for i := 0; i < info.TotalPieces; i++ {
		copy(info.Hashes[i][:], piecesBytes[i*20:(i+1)*20])
	}

	// Calculate last piece length
	var totalLength int64
	if torrent.TFileMode == SingleFileMode {
		lengthObj, ok := torrent.Data["info"].Dict["length"]
		if !ok || lengthObj.IntVal == nil {
			return info, errors.New("length not found in single-file torrent")
		}
		totalLength = int64(*lengthObj.IntVal)
	} else {
		// Multi-file mode: sum all file lengths
		filesObj, ok := torrent.Data["info"].Dict["files"]
		if !ok || filesObj.List == nil {
			return info, errors.New("files not found in multi-file torrent")
		}
		for _, fileObj := range filesObj.List {
			if fileObj.Dict == nil {
				continue
			}
			lengthObj, ok := fileObj.Dict["length"]
			if !ok || lengthObj.IntVal == nil {
				continue
			}
			totalLength += int64(*lengthObj.IntVal)
		}
	}

	// Last piece length = remaining bytes
	lastPieceLen := totalLength - int64(info.TotalPieces-1)*int64(info.PieceLength)
	if lastPieceLen <= 0 || lastPieceLen > int64(info.PieceLength) {
		info.LastPieceLength = info.PieceLength // Shouldn't happen, but safe default
	} else {
		info.LastPieceLength = int32(lastPieceLen)
	}

	return info, nil
}
