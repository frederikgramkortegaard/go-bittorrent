package main

import (
	"fmt"
	"gotorrent/internal/bencoding"
	"gotorrent/internal/libnet"
	"log"
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

	// Setup a Client
	client := libnet.NewClient()
	response, err := libnet.SendTrackerRequest(client, torrent, libnet.SendTrackerRequestParams{
		TrackerAddress: *torrent.Data["announce"].StrVal,
		PeerID:         client.ID,
		Event:          "started",
		Port:           6881,
		Uploaded:       0,
		Downloaded:     0,
		Left:           100, // TODO: calculate from torrent
		Compact:        false,
	})
	if err != nil {
		log.Fatal(err)
	}

	_ = response
//bencoding.PrintDict(response, 0)

	fmt.Println("\n\n\n\n\n-----LLL--")
	dat, err := libnet.SendTrackerScrapeRequest("http://p4p.arenabg.com:1337/announce", []string{string(torrent.InfoHash[:])})
	bencoding.PrintDict(dat, 0)
}
