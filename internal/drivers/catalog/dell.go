package catalog

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path"
	"strings"
)

// Dell's DriverPackCatalog.cab holds one DriverPackage per enterprise pack
// (cab or self-extracting exe) with SHA-256, supported systems, and OS.
const dellCatalogURL = "https://downloads.dell.com/catalog/DriverPackCatalog.cab"

// DellExtract are the silent-extract switches for Dell .exe packs.
var DellExtract = []string{"/s", "/e={dir}"}

type dellFeed struct{ cache *Cache }

func (f *dellFeed) Vendor() Vendor { return Dell }

type dellManifest struct {
	BaseLocation string `xml:"baseLocation,attr"`
	Packages     []struct {
		Format        string `xml:"format,attr"`
		Size          int64  `xml:"size,attr"`
		DateTime      string `xml:"dateTime,attr"`
		VendorVersion string `xml:"vendorVersion,attr"`
		DellVersion   string `xml:"dellVersion,attr"`
		Path          string `xml:"path,attr"`
		ReleaseID     string `xml:"releaseID,attr"`
		Type          string `xml:"type,attr"`
		Name          struct {
			Display string `xml:"Display"`
		} `xml:"Name"`
		OS []struct {
			Code string `xml:"osCode,attr"`
			Arch string `xml:"osArch,attr"`
		} `xml:"SupportedOperatingSystems>OperatingSystem"`
		Models []struct {
			SystemID string `xml:"systemID,attr"`
			Name     string `xml:"name,attr"`
			Display  string `xml:"Display"`
		} `xml:"SupportedSystems>Brand>Model"`
		Hashes []struct {
			Algorithm string `xml:"algorithm,attr"`
			Value     string `xml:",chardata"`
		} `xml:"Cryptography>Hash"`
	} `xml:"DriverPackage"`
}

func (f *dellFeed) Search(ctx context.Context, q Query) ([]Pack, error) {
	q.defaults()
	xmlPath, err := f.cache.xmlFromCab(ctx, "DriverPackCatalog.cab", dellCatalogURL, "DriverPackCatalog.xml")
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(xmlPath)
	if err != nil {
		return nil, err
	}
	return parseDell(b, q)
}

// Models lists every Dell model with a pack for this OS and architecture.
func (f *dellFeed) Models(ctx context.Context, osName, arch string) ([]string, error) {
	xmlPath, err := f.cache.xmlFromCab(ctx, "DriverPackCatalog.cab", dellCatalogURL, "DriverPackCatalog.xml")
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(xmlPath)
	if err != nil {
		return nil, err
	}
	return listDell(b, osName, arch)
}

func listDell(b []byte, osName, arch string) ([]string, error) {
	q := Query{OS: osName, Arch: arch}
	q.defaults()
	var m dellManifest
	if err := xml.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parsing Dell catalog: %w", err)
	}
	wantOS := "windows" + strings.TrimPrefix(q.OS, "win")
	var names []string
	for _, p := range m.Packages {
		if !strings.EqualFold(p.Type, "win") {
			continue
		}
		for _, o := range p.OS {
			if strings.EqualFold(o.Code, wantOS) && (o.Arch == "" || strings.EqualFold(o.Arch, q.Arch)) {
				for _, md := range p.Models {
					names = append(names, md.Name)
				}
				break
			}
		}
	}
	return sortedUnique(names), nil
}

func parseDell(b []byte, q Query) ([]Pack, error) {
	q.defaults()
	var m dellManifest
	if err := xml.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parsing Dell catalog: %w", err)
	}
	base := strings.TrimSuffix(m.BaseLocation, "/")
	if base == "" {
		base = "downloads.dell.com"
	}
	wantOS := "windows" + strings.TrimPrefix(q.OS, "win") // win11 -> windows11
	var out []Pack
	for _, p := range m.Packages {
		if !strings.EqualFold(p.Type, "win") {
			continue
		}
		osOK := false
		for _, o := range p.OS {
			if strings.EqualFold(o.Code, wantOS) && (o.Arch == "" || strings.EqualFold(o.Arch, q.Arch)) {
				osOK = true
			}
		}
		if !osOK {
			continue
		}
		var model string
		var ids []string
		for _, md := range p.Models {
			ids = append(ids, md.SystemID)
			for _, cand := range []string{md.Name, strings.TrimSpace(md.Display)} {
				// An exact name wins over a looser match already found in the
				// same pack, so Exact can find it among the results.
				if strings.EqualFold(strings.TrimSpace(md.Name), strings.TrimSpace(q.Model)) {
					model = md.Name
				} else if model == "" && (modelMatches(q.Model, cand) || strings.EqualFold(cand, strings.TrimSpace(q.Model)) || strings.EqualFold(md.SystemID, strings.TrimSpace(q.Model))) {
					model = md.Name
				}
			}
		}
		if model == "" {
			continue
		}
		pk := Pack{
			Vendor:    Dell,
			Model:     model,
			SystemIDs: ids,
			OS:        q.OS,
			Version:   strings.TrimSpace(p.VendorVersion + " " + p.DellVersion),
			Released:  strings.SplitN(p.DateTime, "T", 2)[0],
			URL:       "https://" + base + "/" + strings.TrimPrefix(p.Path, "/"),
			Size:      p.Size,
			Format:    strings.ToLower(p.Format),
		}
		for _, h := range p.Hashes {
			switch strings.ToUpper(h.Algorithm) {
			case "SHA256":
				pk.SHA256 = strings.ToLower(strings.TrimSpace(h.Value))
			case "SHA1":
				pk.SHA1 = strings.ToLower(strings.TrimSpace(h.Value))
			case "MD5":
				pk.MD5 = strings.ToLower(strings.TrimSpace(h.Value))
			}
		}
		if pk.Format == "" {
			pk.Format = formatOf(path.Base(p.Path))
		}
		if pk.Format == "exe" {
			pk.Extract = DellExtract
		}
		out = append(out, pk)
	}
	newestFirst(out)
	// Prefer cab packs (no extraction step) when dates tie.
	for i := 1; i < len(out); i++ {
		if out[i].Released == out[i-1].Released && out[i].Format == "cab" && out[i-1].Format != "cab" {
			out[i], out[i-1] = out[i-1], out[i]
		}
	}
	return out, nil
}
