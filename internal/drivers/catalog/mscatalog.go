package catalog

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/DustanBaker/uplink-composer/internal/buildinfo"
)

// The Microsoft Update Catalog has no API; this mirrors what the site's own
// page does: a search GET, then the DownloadDialog POST that reveals the
// file URL. Fragile by nature — every parse failure names the step.
const (
	msCatalogSearch   = "https://www.catalog.update.microsoft.com/Search.aspx"
	msCatalogDownload = "https://www.catalog.update.microsoft.com/DownloadDialog.aspx"
)

type msCatalogFeed struct{}

func (f *msCatalogFeed) Vendor() Vendor { return MSCatalog }

var (
	msRowRe  = regexp.MustCompile(`(?s)<tr id="([0-9a-f-]{36})_R\d+".*?</tr>`)
	msCellRe = regexp.MustCompile(`(?s)<td[^>]*id="[0-9a-f-]{36}_C(\d)_R\d+"[^>]*>(.*?)</td>`)
	msTagRe  = regexp.MustCompile(`(?s)<[^>]+>`)
	msSizeRe = regexp.MustCompile(`id="[0-9a-f-]{36}_originalSize">(\d+)<`)
	msFileRe = regexp.MustCompile(`downloadInformation\[0\]\.files\[(\d+)\]\.(url|digest|fileName)\s*=\s*'([^']*)'`)
)

func msUserAgent() string {
	return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) " + buildinfo.UserAgent()
}

// Search returns driver updates matching a hardware ID, newest first.
func (f *msCatalogFeed) Search(ctx context.Context, q Query) ([]Pack, error) {
	q.defaults()
	if q.HWID == "" {
		return nil, fmt.Errorf("the Microsoft Update Catalog is searched by hardware ID (e.g. PCI\\VEN_8086&DEV_15B8)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, msCatalogSearch+"?q="+url.QueryEscape(q.HWID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", msUserAgent())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Microsoft Update Catalog search: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Microsoft Update Catalog search: HTTP %d", resp.StatusCode)
	}
	return parseMSCatalog(string(body), q), nil
}

func parseMSCatalog(page string, q Query) []Pack {
	q.defaults()
	var out []Pack
	for _, row := range msRowRe.FindAllStringSubmatch(page, -1) {
		guid := row[1]
		cells := map[string]string{}
		for _, c := range msCellRe.FindAllStringSubmatch(row[0], -1) {
			cells[c[1]] = strings.TrimSpace(html.UnescapeString(msTagRe.ReplaceAllString(c[2], " ")))
		}
		classification := cells["3"]
		if !strings.Contains(strings.ToLower(classification), "driver") {
			continue
		}
		products := cells["2"]
		if q.OS == "win11" && !strings.Contains(products, "Windows 11") {
			continue
		}
		if q.OS == "win10" && !strings.Contains(products, "Windows 10") {
			continue
		}
		var size int64
		if m := msSizeRe.FindStringSubmatch(row[0]); m != nil {
			size, _ = strconv.ParseInt(m[1], 10, 64)
		}
		out = append(out, Pack{
			Vendor:   MSCatalog,
			Model:    strings.Join(strings.Fields(cells["1"]), " "),
			OS:       q.OS,
			Version:  cells["5"],
			Released: normalizeUSDate(cells["4"]),
			Size:     size,
			Products: products,
			UpdateID: guid,
			Format:   "cab",
		})
	}
	newestFirst(out)
	return out
}

// normalizeUSDate turns 11/15/2025 into 2025-11-15 so string sort works.
func normalizeUSDate(s string) string {
	parts := strings.Split(strings.TrimSpace(s), "/")
	if len(parts) != 3 {
		return s
	}
	return fmt.Sprintf("%s-%02s-%02s", parts[2], parts[0], parts[1])
}

// Resolve fills in the download URL and SHA-1 for a Catalog result.
func (f *msCatalogFeed) Resolve(ctx context.Context, p *Pack) error {
	if p.UpdateID == "" {
		return fmt.Errorf("pack has no Microsoft Update Catalog id")
	}
	payload := fmt.Sprintf(`[{"size":0,"updateID":"%s","uidInfo":"%s","languages":"","vendorName":"","version":""}]`, p.UpdateID, p.UpdateID)
	form := url.Values{}
	form.Set("updateIDs", payload)
	for _, k := range []string{"updateIDsBlockedForImport", "wsusApiPresent", "contentImport", "sku", "serverName", "ssl", "portNumber", "version"} {
		form.Set(k, "")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, msCatalogDownload, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", msUserAgent())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", "https://www.catalog.update.microsoft.com/")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("Microsoft Update Catalog download dialog: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	var fileURL, digest, name string
	for _, m := range msFileRe.FindAllStringSubmatch(string(body), -1) {
		if m[1] != "0" {
			continue // first file only; driver updates ship one cab
		}
		switch m[2] {
		case "url":
			fileURL = m[3]
		case "digest":
			digest = m[3]
		case "fileName":
			name = m[3]
		}
	}
	if fileURL == "" {
		return fmt.Errorf("Microsoft Update Catalog returned no download link for %s (page layout may have changed)", p.UpdateID)
	}
	p.URL = fileURL
	p.Format = formatOf(name)
	if raw, err := base64.StdEncoding.DecodeString(digest); err == nil && len(raw) == 20 {
		p.SHA1 = hex.EncodeToString(raw)
	}
	return nil
}
