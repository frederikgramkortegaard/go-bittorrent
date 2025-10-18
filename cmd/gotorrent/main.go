package main

import (
	"flag"
	"fmt"
	"go-bittorrent/internal/bencoding"
	"go-bittorrent/internal/config"
	"go-bittorrent/internal/daemon"
	"go-bittorrent/internal/libnet"
	"go-bittorrent/internal/logger"
	"go-bittorrent/internal/tui"
	"os"
)

func main() {
	// Redirect stdout/stderr to log file IMMEDIATELY before any logging
	// This prevents logger output from interfering with the TUI
	logFile, err := os.OpenFile("go-bittorrent.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open log file: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()

	// Save original stdout/stderr for TUI
	origStdout := os.Stdout
	origStderr := os.Stderr

	// Redirect all output to log file
	os.Stdout = logFile
	os.Stderr = logFile

	// Restore on exit
	defer func() {
		os.Stdout = origStdout
		os.Stderr = origStderr
	}()

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

		log.Info("Starting torrent download session")
		_, err = torrentManager.StartTorrentDownloadSession(torrent)
		if err != nil {
			log.Error("Error starting torrent session: %v", err)
			os.Exit(1)
		}
	}

	// Start the TUI with original stderr for output
	// Logs are redirected to file, TUI gets clean terminal
	if err := tui.RunTUI(torrentManager, cfg, origStderr, "go-bittorrent.log"); err != nil {
		fmt.Fprintf(origStderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}
