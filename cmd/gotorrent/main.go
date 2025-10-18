package main

import (
	"fmt"
	"gotorrent/internal/bencoding"
	"gotorrent/internal/daemon"
	"gotorrent/internal/libnet"
	"os"
	"time"
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
	client, err := libnet.NewClient([20]byte{})
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

	fmt.Printf("\nStarted session for torrent. Active sessions: %d\n", len(torrentManager.Sessions))
	fmt.Printf("Total pieces: %d\n", session.PieceManager.TotalPieces)
	fmt.Printf("Total size: %d bytes (%.2f MB)\n", session.PieceManager.TotalSize(), float64(session.PieceManager.TotalSize())/(1024*1024))
	fmt.Println("\nDownloading...")

	for !session.PieceManager.IsComplete() {
		time.Sleep(1 * time.Second)
	}

	fmt.Println("\n\nDownload complete!")
	fmt.Printf("Downloaded %d pieces\n", session.PieceManager.CompletedPieces())

	// Cancel context to shut down all peer loops gracefully
	if session != nil {
		session.Cancel()
	}

	// Write to disk
	err = session.DiskManager.WriteToDisk(session.PieceManager)
	if err != nil {
		fmt.Println("Error writing to disk:", err)
		os.Exit(1)
	}

	fmt.Println("File written to disk successfully!")
	fmt.Println("Download session completed!")
}
