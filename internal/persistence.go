package internal

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"go-bittorrent/internal/bencoding"
	"go-bittorrent/internal/config"
	"os"
	"path/filepath"
)

func TorrentFileExistsInDotfolder(torrentFile bencoding.TorrentFile, cfg *config.Config) bool {
	byteArray := []byte(torrentFile.InfoHash[:])
	encoded := base64.RawURLEncoding.EncodeToString(byteArray) // Use RawURLEncoding to avoid padding
	savepath := filepath.Join(cfg.DotfolderPath, encoded+".json")

	_, err := os.Stat(savepath)
	return err == nil

}
func StoreTorrentFileInDotfolder(torrentFile bencoding.TorrentFile, cfg *config.Config) error {
	// We use the infoHash as the filename, but since stringified raw bytes are not safe
	// as a path/filename, we convert it to base64 first.
	byteArray := []byte(torrentFile.InfoHash[:])
	encoded := base64.RawURLEncoding.EncodeToString(byteArray) // Use RawURLEncoding to avoid padding
	savepath := filepath.Join(cfg.DotfolderPath, encoded+".json")

	if _, err := os.Stat(savepath); !errors.Is(err, os.ErrNotExist) {
		// File already exists
		return ErrFileAlreadyExists
	}

	data, err := json.MarshalIndent(torrentFile, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(savepath, data, 0644)
}

func LoadTorrentFileFromDotfolder(infoHash [20]byte, cfg *config.Config) (*bencoding.TorrentFile, error) {
	// Convert infoHash to base64 filename
	byteArray := []byte(infoHash[:])
	encoded := base64.RawURLEncoding.EncodeToString(byteArray)
	savepath := filepath.Join(cfg.DotfolderPath, encoded+".json")

	data, err := os.ReadFile(savepath)
	if err != nil {
		return nil, err
	}

	var tf bencoding.TorrentFile
	err = json.Unmarshal(data, &tf)
	if err != nil {
		return nil, err
	}

	return &tf, nil
}
