package libnet


import (
    "errors"
    "testing"
)


func TestGetScrapeURL(t *testing.T) {

	scrapeURL, err := GetScrapeURLFromAnnounceURL("http://example.com/announce")
	if err != nil || scrapeURL != "http://example.com/scrape"{
		t.Errorf(`GetScrapeUrl("http://example.com/announce") = %s, %v, want match for %s, http://example.com/scrape`, scrapeURL, err, scrapeURL)
	}

	scrapeURL, err = GetScrapeURLFromAnnounceURL("http://example.com/x/announce")
	if err != nil || scrapeURL != "http://example.com/x/scrape"{
		t.Errorf(`GetScrapeUrl("http://example.com/x/announce") = %s, %v, want match for %s, http://example.com/x/scrape`, scrapeURL, err, scrapeURL)
	}

	scrapeURL, err = GetScrapeURLFromAnnounceURL("http://example.com/announce.php")
	if err != nil || scrapeURL != "http://example.com/scrape.php"{
		t.Errorf(`GetScrapeUrl("http://example.com/announce.php") = %s, %v, want match for %s, http://example.com/scrape.php`, scrapeURL, err, scrapeURL)
	}

	scrapeURL, err = GetScrapeURLFromAnnounceURL("http://example.com/a")
	if !errors.Is(err, ErrURLTooShort) || scrapeURL != ""{
		t.Errorf(`GetScrapeUrl("http://example.com/a") = %s, %v, want ErrURLTooShort`, scrapeURL, err)
	}

	scrapeURL, err = GetScrapeURLFromAnnounceURL("http://example.com/announce?x2%0644")
	if err != nil || scrapeURL != "http://example.com/scrape?x2%0644" {
		t.Errorf(`GetScrapeUrl("http://example.com/announce?x2%%0644") = %s, %v, want match for %s, http://example.com/scrape?x2%%0644`, scrapeURL, err, scrapeURL)
	}

	scrapeURL, err = GetScrapeURLFromAnnounceURL("http://example.com/announce?x=2/4")
	if !errors.Is(err, ErrURLTooShort) || scrapeURL != "" {
		t.Errorf(`GetScrapeUrl("http://example.com/announce?x=2/4") = %s, %v, want ErrURLTooShort`, scrapeURL, err)
	}
	scrapeURL, err = GetScrapeURLFromAnnounceURL("http://example.com/x%064announce")
	if !errors.Is(err, ErrNoAnnounceInURL) || scrapeURL != "" {
		t.Errorf(`GetScrapeUrl("http://example.com/x%%064announce") = %s, %v, want ErrNoAnnounceInURL`, scrapeURL, err)
	}
}
