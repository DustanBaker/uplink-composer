package catalog

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"
)

// HP's HPClientDriverPackCatalog.cab joins two tables: ProductOSDriverPack
// (system name/IDs + OS → SoftPaq id) and SoftPaq (id → URL, size, hashes).
// Packs are SoftPaq self-extracting exes.
const hpCatalogURL = "https://hpia.hpcloud.hp.com/downloads/driverpackcatalog/HPClientDriverPackCatalog.cab"

// HPExtract are the silent-extract switches for HP SoftPaqs.
var HPExtract = []string{"-pdf", "-e", "-s", `-f"{dir}"`}

type hpFeed struct{ cache *Cache }

func (f *hpFeed) Vendor() Vendor { return HP }

type hpProduct struct {
	Architecture string `xml:"Architecture"`
	ProductType  string `xml:"ProductType"`
	SystemID     string `xml:"SystemId"`
	SystemName   string `xml:"SystemName"`
	OSName       string `xml:"OSName"`
	SoftPaqID    string `xml:"SoftPaqId"`
}

type hpSoftPaq struct {
	ID           string `xml:"Id"`
	Name         string `xml:"Name"`
	Version      string `xml:"Version"`
	Category     string `xml:"Category"`
	DateReleased string `xml:"DateReleased"`
	URL          string `xml:"Url"`
	Size         int64  `xml:"Size"`
	MD5          string `xml:"MD5"`
	SHA256       string `xml:"SHA256"`
}

func (f *hpFeed) Search(ctx context.Context, q Query) ([]Pack, error) {
	q.defaults()
	xmlPath, err := f.cache.xmlFromCab(ctx, "HPClientDriverPackCatalog.cab", hpCatalogURL, "HPClientDriverPackCatalog.xml")
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(xmlPath)
	if err != nil {
		return nil, err
	}
	return parseHP(b, q)
}

// parseHP walks the document collecting the two record types wherever
// they sit, so wrapper element names never matter.
func parseHP(b []byte, q Query) ([]Pack, error) {
	q.defaults()
	dec := xml.NewDecoder(bytes.NewReader(b))
	var products []hpProduct
	softpaqs := map[string]hpSoftPaq{}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parsing HP catalog: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "ProductOSDriverPack":
			var p hpProduct
			if err := dec.DecodeElement(&p, &se); err != nil {
				return nil, err
			}
			products = append(products, p)
		case "SoftPaq":
			var s hpSoftPaq
			if err := dec.DecodeElement(&s, &se); err != nil {
				return nil, err
			}
			softpaqs[s.ID] = s
		}
	}

	wantOS := "Windows " + strings.TrimPrefix(q.OS, "win") // "Windows 11"
	var out []Pack
	seen := map[string]bool{}
	for _, p := range products {
		if !strings.HasPrefix(p.OSName, wantOS) {
			continue
		}
		if q.Arch == "x64" && !strings.Contains(p.Architecture, "64") {
			continue
		}
		ids := strings.Split(p.SystemID, ",")
		match := modelMatches(q.Model, p.SystemName)
		for _, id := range ids {
			if strings.EqualFold(strings.TrimSpace(id), strings.TrimSpace(q.Model)) {
				match = true
			}
		}
		if !match {
			continue
		}
		sp, ok := softpaqs[p.SoftPaqID]
		if !ok || seen[sp.ID+"|"+p.SystemName] {
			continue
		}
		seen[sp.ID+"|"+p.SystemName] = true
		osVer := ""
		if i := strings.LastIndex(p.OSName, ","); i >= 0 {
			osVer = strings.TrimSpace(p.OSName[i+1:])
		}
		url := strings.Replace(strings.TrimSpace(sp.URL), "ftp://", "https://", 1)
		for i := range ids {
			ids[i] = strings.TrimSpace(ids[i])
		}
		out = append(out, Pack{
			Vendor:    HP,
			Model:     p.SystemName,
			SystemIDs: ids,
			OS:        q.OS,
			OSVersion: osVer,
			Version:   sp.Version,
			Released:  normalizeUSDate(strings.SplitN(sp.DateReleased, " ", 2)[0]),
			URL:       url,
			Size:      sp.Size,
			SHA256:    strings.ToLower(sp.SHA256),
			MD5:       strings.ToLower(sp.MD5),
			Format:    formatOf(url),
			Extract:   HPExtract,
		})
	}
	newestFirst(out)
	return out, nil
}
