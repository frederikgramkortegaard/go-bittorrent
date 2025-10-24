package main

import (
	"flag"
	"fmt"
	"go-bittorrent/internal"
	"go-bittorrent/internal/bencoding"
	"go-bittorrent/internal/config"
	"go-bittorrent/internal/daemon"
	"go-bittorrent/internal/libnet"
	"go-bittorrent/internal/logger"
	//"go-bittorrent/internal/tui"
	"os"
	"path/filepath"
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

	// Create root logger for main (will now write to log file)
	log := logger.New().WithPrefix("Main")

	// Create ONE client for the entire application
	client, err := libnet.NewClient(cfg, [20]byte{})
	if err != nil {
		log.Error("Error creating client: %v", err)
		os.Exit(1)
	}
	defer client.Listener.Close()

	// Create the torrent manager with the shared client
	torrentManager := daemon.NewTorrentManager(client)

	// If a torrent file was provided as argument, start it automatically
	if flag.NArg() >= 1 {
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

		err = internal.StoreTorrentFileInDotfolder(torrent, cfg)
		if err != nil {
			log.Error("Error storing torrent file on disk: %v", err)
			os.Exit(1)
		}

		log.Info("Starting torrent session")
		_, err = torrentManager.StartTorrentSession(torrent)
		if err != nil {
			log.Error("Error starting torrent session: %v", err)
			os.Exit(1)
		}
	} else {

		// Scan Dotfolder for all torrent files and start their sessions
		files, err := os.ReadDir(cfg.DotfolderPath)
		if err != nil {
			log.Error("%v", err)
			os.Exit(1)
		}

		for _, f := range files {
			fmt.Println(f.Name())
			torrentFile, err := internal.LoadTorrentFileFromPath(
				filepath.Join(cfg.DotfolderPath, f.Name()),
				cfg,
			)

			if err != nil || torrentFile == nil {
				log.Error("Failed to load torrent file from disk: %s, %v", f.Name(), err)
				continue
			}

			log.Info("loaded torrent file %s from dotfolder, starting session", f.Name())
			_, err = torrentManager.StartTorrentSession(*torrentFile)
			if err != nil {
				log.Error("Error starting torrent session: %v", err)
				os.Exit(1)
			}

		}

	}

	// Wait for all torrents to complete (blocks efficiently using channels)
	torrentManager.WaitForCompletion()

}
