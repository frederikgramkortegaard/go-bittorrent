package libnet

import (
	"bufio"
	"time"
	"fmt"
	"gotorrent/internal/bencoding"
	"io"
	"net"
	"net/url"
	"strings"
)

type SendTrackerRequestParams struct {

	// Target
	TrackerAddress string

	// BitTorrent Specification Requirements
	// InfoHash is derived from TorrentFile parameter
	InfoHash   string // Sent as 'info_hash' (internal, set by SendTrackerRequest)
	PeerID     string // Sent as 'peer_id'
	Port       int    // Sent as 'port'
	Uploaded   int64  // Sent as 'uploaded'
	Downloaded uint64 // Sent as 'downloaded'
	Left       uint64 // Sent as 'left'
	Compact    bool   // Sent as 'compact' as a 1 or 0

	// Optional fields (only sent if non-empty/non-zero)
	Event     string // OPTIONAL: Sent as 'event' if non-empty
	IP        string // OPTIONAL: Sent as 'ip' if non-empty
	Numwant   int64  // OPTIONAL: Sent as 'numwant' if non-zero
	Key       string // OPTIONAL: Sent as 'key' if non-empty
	TrackerID string // OPTIONAL: Sent as 'trackerid' if non-empty
}

// Note that all binary data in the URL (particularly info_hash and peer_id) must be properly escaped. This means any byte not in the set 0-9, a-z, A-Z, '.', '-', '_' and '~', must be encoded using the "%nn" format, where nn is the hexadecimal value of the byte. (See RFC1738 for details.)
func HexEncode(data string) string {
	result := ""
	// Iterate over bytes, not runes (important for binary data)
	for i := 0; i < len(data); i++ {
		b := data[i]
		// Unreserved characters: 0-9, a-z, A-Z, '.', '-', '_', '~'
		if (b >= '0' && b <= '9') ||
			(b >= 'a' && b <= 'z') ||
			(b >= 'A' && b <= 'Z') ||
			b == '.' || b == '-' || b == '_' || b == '~' {
			result += string(b)
		} else {
			result += fmt.Sprintf("%%%02X", b) // Uppercase %NN
		}
	}
	return result
}

func (s *SendTrackerRequestParams) FormatQuery() string {

	out := fmt.Sprintf("info_hash=%s&peer_id=%s&port=%d&uploaded=%d&downloaded=%d&left=%d&compact=%d",
		HexEncode(s.InfoHash),
		HexEncode(s.PeerID),
		s.Port,
		s.Uploaded,
		s.Downloaded,
		s.Left,
		map[bool]int{false: 0, true: 1}[s.Compact],
	)

	// Optional fields
	if s.Event != "" {
		out += fmt.Sprintf("&event=%s", s.Event)
	}
	if s.IP != "" {
		out += fmt.Sprintf("&ip=%s", s.IP)
	}
	if s.Numwant != 0 {
		out += fmt.Sprintf("&numwant=%d", s.Numwant)
	}
	if s.Key != "" {
		out += fmt.Sprintf("&key=%s", s.Key)
	}
	if s.TrackerID != "" {
		out += fmt.Sprintf("&trackerid=%s", s.TrackerID)
	}

	return out

}

func SendTrackerRequest(c *Client, torrent bencoding.TorrentFile, params SendTrackerRequestParams) (map[string]bencoding.BencodedObject, error) {
	// Use info hash from torrent file as raw binary string
	params.InfoHash = string(torrent.InfoHash[:])

	// Parse the tracker URL
	fmt.Println(params.FormatQuery())
	u, err := url.Parse(params.TrackerAddress)
	if err != nil {
		fmt.Println("URL parse error:", err)
		return nil, err
	}

	// Extract host:port for TCP connection
	host := u.Host
	if u.Port() == "" {
		// Default HTTP port if not specified
		host = u.Hostname() + ":80"
	}

	// Construct HTTP GET request with path from URL
	path := u.Path
	if path == "" {
		path = "/"
	}
	request := fmt.Sprintf("GET %s?%s HTTP/1.1\r\n", path, params.FormatQuery()) +
		fmt.Sprintf("Host: %s\r\n", u.Hostname()) +
		"User-Agent: MyTorrentClient/0.1\r\n" +
		"Connection: close\r\n" +
		"\r\n"

	// Open TCP connection to tracker
	d := net.Dialer{Timeout: 30 * time.Second}
	conn, err := d.Dial("tcp", host)
	if err != nil {
		fmt.Println("Connection error:", err)
		return nil, err
	}
	defer conn.Close()

	// Send the raw HTTP GET request
	_, err = conn.Write([]byte(request))
	if err != nil {
		fmt.Println("Write error:", err)
		return nil, err
	}

	fmt.Println("----")
	// Read and print the response
	reader := bufio.NewReader(conn)

	// Read headers
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		fmt.Print(line)
		// Empty line marks end of headers
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	// Read body
	fmt.Println("Body:")
	buf := new(strings.Builder)
	_, err = io.Copy(buf, reader)
	if err != nil {
		return nil, err
	}

	data, _, err := bencoding.ParseDict(buf.String())
	if err != nil {
		return nil, err
	}
	return data, nil

}
