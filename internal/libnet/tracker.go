package libnet

import (
	"errors"
	"fmt"
	"gotorrent/internal/bencoding"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Custom error types for tracker operations
var (
	ErrNoSlashInURL    = errors.New("scrape request not supported by tracker: no slash in URL")
	ErrURLTooShort     = errors.New("scrape request not supported by tracker: URL too short")
	ErrNoAnnounceInURL = errors.New("scrape request not supported by tracker: 'announce' not found in URL")
)

// GetScrapeURLFromAnnounceURL converts a tracker announce URL to a scrape URL.
// See https://wiki.theory.org/BitTorrentSpecification#Tracker_.27scrape.27_Convention
func GetScrapeURLFromAnnounceURL(announceURL string) (string, error) {

	lastSlash := strings.LastIndexByte(announceURL, '/')
	if lastSlash == -1 {
		return "", ErrNoSlashInURL
	}

	// 8 is the length of the word 'announce' which we will check for
	if lastSlash+8 >= len(announceURL) {
		return "", ErrURLTooShort
	}

	// If scrape is supported, the slash is always followed by 'announce'
	if announceURL[lastSlash:lastSlash+9] != "/announce" {
		return "", ErrNoAnnounceInURL
	}

	scrapeURL := announceURL[:lastSlash] + strings.Replace(announceURL[lastSlash:], "announce", "scrape", -1)

	return scrapeURL, nil

}

// SendTrackerScrapeRequest sends a scrape request to the tracker to get statistics about torrents.
func (c *Client) SendTrackerScrapeRequest(trackerAddress string, infoHashes []string) (map[string]bencoding.BencodedObject, error) {
	c.Logger.Debug("Sending scrape request to %s", trackerAddress)

	// Get the scrape URL from the Tracker if it's possible
	scrapeURL, err := GetScrapeURLFromAnnounceURL(trackerAddress)
	if err != nil {
		return nil, err
	}

	// Build URL with query parameters
	u, err := url.Parse(scrapeURL)
	if err != nil {
		return nil, fmt.Errorf("URL parse error: %w", err)
	}

	// Add info_hash parameters
	q := u.Query()
	for _, hash := range infoHashes {
		q.Add("info_hash", hash) // Will be URL-encoded automatically
	}
	u.RawQuery = q.Encode()

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: c.Config.TrackerTimeout,
	}

	// Create and send request
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	req.Header.Set("User-Agent", c.Config.UserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	c.Logger.Debug("Scrape response: %s %s", resp.Proto, resp.Status)

	// Read body (automatically handles chunked encoding, compression, etc.)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	data, _, err := bencoding.ParseDict(string(body))
	if err != nil {
		return nil, err
	}
	return data, nil
}

// SendTrackerRequestParams contains the parameters for a regular tracker announce request.
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

// HexEncode encodes binary data for use in URLs according to RFC1738.
// All bytes not in the set 0-9, a-z, A-Z, '.', '-', '_' and '~' are encoded
// using the "%nn" format, where nn is the hexadecimal value of the byte.
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

// FormatQuery formats the tracker request parameters as a URL query string.
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

// SendTrackerRequest sends a regular announce request to the tracker.
func (c *Client) SendTrackerRequest(torrent bencoding.TorrentFile, params SendTrackerRequestParams) (map[string]bencoding.BencodedObject, error) {
	// Use info hash from torrent file as raw binary string
	params.InfoHash = string(torrent.InfoHash[:])

	c.Logger.Debug("Sending tracker request to %s", params.TrackerAddress)

	// Parse the tracker URL
	u, err := url.Parse(params.TrackerAddress)
	if err != nil {
		return nil, fmt.Errorf("URL parse error: %w", err)
	}

	// Build query parameters
	q := u.Query()
	q.Set("info_hash", params.InfoHash)
	q.Set("peer_id", params.PeerID)
	q.Set("port", fmt.Sprintf("%d", params.Port))
	q.Set("uploaded", fmt.Sprintf("%d", params.Uploaded))
	q.Set("downloaded", fmt.Sprintf("%d", params.Downloaded))
	q.Set("left", fmt.Sprintf("%d", params.Left))
	q.Set("compact", fmt.Sprintf("%d", map[bool]int{false: 0, true: 1}[params.Compact]))

	// Optional fields
	if params.Event != "" {
		q.Set("event", params.Event)
	}
	if params.IP != "" {
		q.Set("ip", params.IP)
	}
	if params.Numwant != 0 {
		q.Set("numwant", fmt.Sprintf("%d", params.Numwant))
	}
	if params.Key != "" {
		q.Set("key", params.Key)
	}
	if params.TrackerID != "" {
		q.Set("trackerid", params.TrackerID)
	}

	u.RawQuery = q.Encode()
	c.Logger.Debug("Query string: %s", u.RawQuery)

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: c.Config.TrackerTimeout,
	}

	// Create and send request
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	req.Header.Set("User-Agent", c.Config.UserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	c.Logger.Debug("Tracker response: %s %s", resp.Proto, resp.Status)

	// Read body (automatically handles chunked encoding, compression, etc.)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	data, _, err := bencoding.ParseDict(string(body))
	if err != nil {
		return nil, err
	}
	return data, nil

}
