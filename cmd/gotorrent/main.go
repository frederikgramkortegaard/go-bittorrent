package main

import (
	"flag"
	"fmt"
	"gotorrent/internal/bencoding"
	"gotorrent/internal/config"
	"gotorrent/internal/daemon"
	"gotorrent/internal/libnet"
	"gotorrent/internal/logger"
	"os"
	"time"
)

func main() {
	// Load configuration with defaults
	cfg := config.DefaultConfig()

	// Define CLI flags to override config defaults
	outputDir := flag.String("o", cfg.OutputDir, "Output directory for downloaded files")
	port := flag.Int("port", cfg.ListenPort, "Port to listen on")
	maxPeers := flag.Int("max-peers", cfg.MaxPeersPerTorrent, "Maximum peers per torrent")
	blockSize := flag.Int("block-size", int(cfg.BlockSize), "Block size for piece requests (bytes)")
	pipelineSize := flag.Int("pipeline", cfg.RequestPipelineSize, "Request pipeline size per peer")
	logLevel := flag.String("log-level", cfg.LogLevel, "Log level (debug, info, warn, error)")

	flag.Parse()

	// Override config with CLI flags
	cfg.OutputDir = *outputDir
	cfg.ListenPort = *port
	cfg.MaxPeersPerTorrent = *maxPeers
	cfg.BlockSize = int32(*blockSize)
	cfg.RequestPipelineSize = *pipelineSize
	cfg.LogLevel = *logLevel

	// Create root logger for main
	log := logger.New().WithPrefix("Main")

	if flag.NArg() < 1 {
		fmt.Println("Usage: gotorrent [options] <file.torrent>")
		fmt.Println("\nOptions:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	filePath := flag.Arg(0)
	log.Info("Reading torrent file: %s", filePath)
	fileData, err := os.ReadFile(filePath)
	if err != nil {
		log.Error("Error reading file: %v", err)
		os.Exit(1)
	}

	torrent, err := bencoding.ParseTorrentFile(string(fileData))
	if err != nil {
		log.Error("Error parsing torrent file: %v", err)
		os.Exit(1)
	}

	log.Debug("File Mode: %s", torrent.TFileMode)
	// Print info dict, but skip the "pieces" field (binary data)
	infoDict := torrent.Data["info"].Dict
	infoDictCopy := make(map[string]bencoding.BencodedObject)
	for k, v := range infoDict {
		if k != "pieces" {
			infoDictCopy[k] = v
		}
	}
	bencoding.PrintDict(infoDictCopy, 0)

	// Create ONE client for the entire application
	client, err := libnet.NewClient(cfg, [20]byte{})
	if err != nil {
		log.Error("Error creating client: %v", err)
		os.Exit(1)
	}
	defer client.Listener.Close()

	// Create the torrent manager with the shared client
	torrentManager := daemon.NewTorrentManager(client)

	// Start downloading this torrent
	log.Info("Starting torrent download session")
	session, err := torrentManager.StartTorrentDownloadSession(torrent)
	if err != nil {
		log.Error("Error starting torrent session: %v", err)
		os.Exit(1)
	}

	log.Info("Started session - Active sessions: %d, Total pieces: %d, Total size: %d bytes (%.2f MB)",
		len(torrentManager.Sessions),
		session.PieceManager.TotalPieces,
		session.PieceManager.TotalSize(),
		float64(session.PieceManager.TotalSize())/(1024*1024))

	// Wait for download to complete (completion is handled automatically by the session)
	for !session.PieceManager.IsComplete() {
		time.Sleep(cfg.CompletionPollInterval)
	}

	log.Info("Download session completed!")
}
