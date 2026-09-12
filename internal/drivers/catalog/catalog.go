// Package catalog finds driver packs for specific machines from official
// feeds: the Dell, Lenovo, and HP enterprise driver-pack catalogs (by
// model) and the Microsoft Update Catalog (by hardware ID, covering
// vendors without a feed — Intel/ASUS NUCs included). Results carry the
// download URL, size, hashes, and the silent-extract switches needed to
// install the pack at first boot.
package catalog

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Vendor identifies a feed.
type Vendor string

const (
	Dell      Vendor = "dell"
	Lenovo    Vendor = "lenovo"
	HP        Vendor = "hp"
	MSCatalog Vendor = "mscatalog"
)

// Pack is one downloadable driver package.
type Pack struct {
	Vendor    Vendor
	Model     string   // display model name (or MS Catalog title)
	SystemIDs []string // vendor machine/platform IDs
	OS        string   // "win11" | "win10"
	OSVersion string   // "24H2", "*" ...
	Version   string   // pack/driver version
	Released  string   // ISO-ish date as published
	URL       string
	Size      int64
	SHA256    string
	SHA1      string
	MD5       string
	Format    string   // "cab" | "exe"
	Extract   []string // silent-extract args for exe packs; {dir} = destination
	Products  string   // MS Catalog: products/OS column
	UpdateID  string   // MS Catalog GUID
}

// ID derives a stable manifest id for the pack.
func (p Pack) ID() string {
	base := slug(p.Model)
	if p.Vendor == MSCatalog {
		base = "hwid-" + slug(p.Model)
	}
	parts := []string{string(p.Vendor), base}
	if p.OS != "" {
		parts = append(parts, p.OS)
	}
	if p.OSVersion != "" && p.OSVersion != "*" {
		parts = append(parts, strings.ToLower(p.OSVersion))
	}
	id := strings.Join(parts, "-")
	if len(id) > 80 {
		id = id[:80]
	}
	return strings.Trim(id, "-.")
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// Query selects packs.
type Query struct {
	Model string // model substring or vendor machine ID (model feeds)
	HWID  string // hardware ID (MS Catalog)
	OS    string // "win11" (default) | "win10"
	Arch  string // "x64" (default) | "arm64"
}

func (q *Query) defaults() {
	if q.OS == "" {
		q.OS = "win11"
	}
	if q.Arch == "" {
		q.Arch = "x64"
	}
}

// Feed is one searchable catalog.
type Feed interface {
	Vendor() Vendor
	Search(ctx context.Context, q Query) ([]Pack, error)
}

// Feeds returns the feed for a vendor name.
func FeedFor(vendor string, c *Cache) (Feed, error) {
	switch Vendor(strings.ToLower(vendor)) {
	case Dell:
		return &dellFeed{cache: c}, nil
	case Lenovo:
		return &lenovoFeed{cache: c}, nil
	case HP:
		return &hpFeed{cache: c}, nil
	case MSCatalog, "catalog", "ms", "microsoft":
		return &msCatalogFeed{}, nil
	default:
		return nil, fmt.Errorf("unknown driver feed %q (dell, lenovo, hp, mscatalog)", vendor)
	}
}

// newestFirst orders packs by OS version then release date, descending.
func newestFirst(packs []Pack) {
	sort.SliceStable(packs, func(i, j int) bool {
		if packs[i].OSVersion != packs[j].OSVersion {
			return packs[i].OSVersion > packs[j].OSVersion
		}
		return packs[i].Released > packs[j].Released
	})
}

// modelMatches is a forgiving model comparison: case-insensitive, every
// query token must appear in the candidate.
func modelMatches(query, candidate string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	c := strings.ToLower(candidate)
	if q == "" {
		return false
	}
	for _, tok := range strings.Fields(q) {
		if !strings.Contains(c, tok) {
			return false
		}
	}
	return true
}
