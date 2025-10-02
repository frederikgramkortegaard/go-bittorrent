package main

import (
	"fmt"
	"gotorrent/internal/bencoding"
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
	printDict(torrent.Data, 0)
}

func printObject(obj bencoding.BencodedObject, indent int) {
	prefix := ""
	for i := 0; i < indent; i++ {
		prefix += "  "
	}

	switch obj.Typ {
	case bencoding.BenString:
		if obj.StrVal != nil {
			fmt.Printf("%s\n", *obj.StrVal)
		}
	case bencoding.BenInteger:
		if obj.IntVal != nil {
			fmt.Printf("%d\n", *obj.IntVal)
		}
	case bencoding.BenList:
		fmt.Println("[")
		for _, item := range obj.List {
			fmt.Print(prefix + "  ")
			printObject(item, indent+1)
		}
		fmt.Println(prefix + "]")
	case bencoding.BenDict:
		fmt.Println("{")
		printDict(obj.Dict, indent+1)
		fmt.Println(prefix + "}")
	}
}

func printDict(dict map[string]bencoding.BencodedObject, indent int) {
	prefix := ""
	for i := 0; i < indent; i++ {
		prefix += "  "
	}

	for key, val := range dict {
		fmt.Printf("%s%s: ", prefix, key)
		printObject(val, indent)
	}
}
