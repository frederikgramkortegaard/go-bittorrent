package main

import (
	"fmt"
	"gotorrent/internal/bencoding"
	"gotorrent/internal/daemon"
	"gotorrent/internal/libnet"
	"os"
)

func main() {

	if len(os.Args) < 2 {
		fmt.Println("Usage: gotorrent <file.torrent>")
		os.Exit(1)
	}

	filePath := os.Args[1]
	fileData, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Println("Error reading file:", err)
		os.Exit(1)
	}

	torrent, err := bencoding.ParseTorrentFile(string(fileData))
	if err != nil {
		fmt.Println("Error parsing torrent file:", err)
		os.Exit(1)
	}

	fmt.Printf("File Mode: %s\n\n", torrent.TFileMode)
	bencoding.PrintDict(torrent.Data, 0)

	// == Start of what will really happen

	// Create ONE client for the entire application
	client, err := libnet.NewClient()
	if err != nil {
		fmt.Println("Error creating client:", err)
		os.Exit(1)
	}
	defer client.Listener.Close()

	// Create the torrent manager with the shared client
	torrentManager := daemon.NewTorrentManager(client)

	// Start downloading this torrent
	session, err := torrentManager.StartTorrentSession(torrent)
	if err != nil {
		fmt.Println("Error starting torrent session:", err)
		os.Exit(1)
	}
	_ = session

	fmt.Printf("\nStarted session for torrent. Active sessions: %d\n", len(torrentManager.Sessions))

}
