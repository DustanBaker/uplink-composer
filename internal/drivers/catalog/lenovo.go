package catalog

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"strings"
)

// Lenovo publishes catalogv2.xml: every model with its machine types and
// per-OS SCCM driver packs (self-extracting exe; crc is a SHA-256).
const lenovoCatalogURL = "https://download.lenovo.com/cdrt/td/catalogv2.xml"

// LenovoExtract are the silent-extract switches for Lenovo SCCM packs.
var LenovoExtract = []string{"/VERYSILENT", "/DIR={dir}", "/EXTRACT=YES"}

type lenovoFeed struct{ cache *Cache }

func (f *lenovoFeed) Vendor() Vendor { return Lenovo }

type lenovoModelList struct {
	Models []struct {
		Name  string   `xml:"name,attr"`
		Arch  string   `xml:"arch,attr"`
		Types []string `xml:"Types>Type"`
		SCCM  []struct {
			OS      string `xml:"os,attr"`
			Version string `xml:"version,attr"`
			Date    string `xml:"date,attr"`
			CRC     string `xml:"crc,attr"`
			MD5     string `xml:"md5,attr"`
			URL     string `xml:",chardata"`
		} `xml:"SCCM"`
	} `xml:"Model"`
}

func (f *lenovoFeed) Search(ctx context.Context, q Query) ([]Pack, error) {
	q.defaults()
	path, err := f.cache.file(ctx, "lenovo-catalogv2.xml", lenovoCatalogURL)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseLenovo(b, q)
}

func parseLenovo(b []byte, q Query) ([]Pack, error) {
	q.defaults()
	var list lenovoModelList
	if err := xml.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("parsing Lenovo catalog: %w", err)
	}
	var out []Pack
	for _, m := range list.Models {
		match := modelMatches(q.Model, m.Name)
		if !match {
			for _, t := range m.Types {
				if strings.EqualFold(t, strings.TrimSpace(q.Model)) {
					match = true
				}
			}
		}
		if !match {
			continue
		}
		for _, s := range m.SCCM {
			if !strings.EqualFold(s.OS, q.OS) {
				continue
			}
			url := strings.TrimSpace(s.URL)
			out = append(out, Pack{
				Vendor:    Lenovo,
				Model:     m.Name,
				SystemIDs: m.Types,
				OS:        strings.ToLower(s.OS),
				OSVersion: s.Version,
				Released:  s.Date,
				URL:       url,
				SHA256:    strings.ToLower(s.CRC),
				MD5:       strings.ToLower(s.MD5),
				Format:    formatOf(url),
				Extract:   LenovoExtract,
			})
		}
	}
	newestFirst(out)
	return out, nil
}

func formatOf(url string) string {
	l := strings.ToLower(url)
	switch {
	case strings.HasSuffix(l, ".cab"):
		return "cab"
	case strings.HasSuffix(l, ".zip"):
		return "zip"
	default:
		return "exe"
	}
}
