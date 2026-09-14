package catalog

import (
	"context"
	"fmt"
	"html"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Framework publishes no driver-pack catalog. Each model has a downloads page
// on resources.frame.work naming its Windows driver bundle, with a SHA-256,
// and the site's sitemap lists every such page. That is the catalog.
//
// The bundle is not a pack of INFs for pnputil. It is a self-extracting
// installer (7-Zip's SFXSetup) that runs Framework's own install_drivers.bat,
// which installs each vendor's installer silently and takes -u for
// unattended: no pause at the end and no forced restart. So a Framework pack
// is installed by running it with -u, and only on the model it is for — the
// script stops at a pause when it finds a different mainboard, which would
// hang an unattended first boot.
const frameworkSitemapURL = "https://resources.frame.work/sitemap-0.xml"

// FrameworkArgs run Framework's driver bundle unattended.
var FrameworkArgs = []string{"-u"}

type frameworkFeed struct{ cache *Cache }

func (f *frameworkFeed) Vendor() Vendor { return Framework }

var (
	fwPageURL  = regexp.MustCompile(`https://resources\.frame\.work/downloads/[a-z0-9-]+/[a-z0-9-]+/`)
	fwTitle    = regexp.MustCompile(`<title>([^<]*)</title>`)
	fwTag      = regexp.MustCompile(`<[^>]+>`)
	fwSpace    = regexp.MustCompile(`\s+`)
	fwBundle   = regexp.MustCompile(`Windows Driver Bundle (v[0-9.]+)\s+(\d{4}-\d{2}-\d{2})\s+(\S+\.exe)\s+SHA256 · ([0-9A-Fa-f]{64})`)
	fwMarks    = strings.NewReplacer("™", "", "®", "", "©", "")
	fwSplitNum = regexp.MustCompile(`([0-9])([a-z])|([a-z])([0-9])`)
)

// parseFrameworkPage reads one model's downloads page: the model's name and
// its Windows driver bundle for the OS, if it has one.
func parseFrameworkPage(page []byte, osName string) (Pack, bool) {
	s := string(page)
	t := fwTitle.FindStringSubmatch(s)
	if t == nil {
		return Pack{}, false
	}
	model := html.UnescapeString(t[1])
	if i := strings.Index(model, " — "); i >= 0 {
		model = model[:i]
	}
	model = strings.TrimSpace(fwSpace.ReplaceAllString(fwMarks.Replace(model), " "))
	text := fwSpace.ReplaceAllString(html.UnescapeString(fwTag.ReplaceAllString(s, " ")), " ")
	want := "_" + strings.ToUpper(strings.TrimPrefix(osName, "win")) // win11 -> _11, as in _W11_
	for _, m := range fwBundle.FindAllStringSubmatch(text, -1) {
		file := m[3]
		if !strings.Contains(strings.ToUpper(file), "_W"+strings.TrimPrefix(want, "_")+"_") {
			continue
		}
		url := ""
		if i := strings.Index(s, "https://downloads.frame.work/driver/"+file); i >= 0 {
			url = "https://downloads.frame.work/driver/" + file
		}
		if url == "" {
			continue
		}
		return Pack{
			Vendor:   Framework,
			Model:    model,
			OS:       osName,
			Version:  m[1],
			Released: m[2],
			URL:      url,
			SHA256:   strings.ToLower(m[4]),
			Format:   "exe",
			Install:  "exe",
			Args:     FrameworkArgs,
		}, true
	}
	return Pack{Vendor: Framework, Model: model}, false
}

// packs reads every model page and returns the bundles for this OS.
func (f *frameworkFeed) packs(ctx context.Context, osName string) ([]Pack, error) {
	q := Query{OS: osName}
	q.defaults()
	sitemap, err := f.cache.file(ctx, "framework-sitemap.xml", frameworkSitemapURL)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(sitemap)
	if err != nil {
		return nil, err
	}
	urls := fwPageURL.FindAllString(string(b), -1)
	sort.Strings(urls)
	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		out   []Pack
		errs  []string
		seen  = map[string]bool{}
		limit = make(chan struct{}, 4)
	)
	for _, u := range urls {
		if seen[u] || strings.Contains(u, "/knowledgebase/") {
			continue
		}
		seen[u] = true
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			name := "framework-" + strings.ReplaceAll(strings.Trim(strings.TrimPrefix(u, "https://resources.frame.work/downloads/"), "/"), "/", "-") + ".html"
			path, err := f.cache.file(ctx, name, u)
			if err == nil {
				var page []byte
				if page, err = os.ReadFile(path); err == nil {
					if p, ok := parseFrameworkPage(page, q.OS); ok {
						mu.Lock()
						out = append(out, p)
						mu.Unlock()
					}
					return
				}
			}
			mu.Lock()
			errs = append(errs, err.Error())
			mu.Unlock()
		}(u)
	}
	wg.Wait()
	if len(out) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("reading Framework's downloads pages: %s", errs[0])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out, nil
}

// Models lists every Framework model with a Windows driver bundle for the OS.
func (f *frameworkFeed) Models(ctx context.Context, osName, arch string) ([]string, error) {
	if arch != "" && arch != "x64" {
		return nil, nil
	}
	packs, err := f.packs(ctx, osName)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, p := range packs {
		names = append(names, p.Model)
	}
	return sortedUnique(names), nil
}

// Search finds the bundle for a model: the name as listed, or the name the
// machine reports about itself, such as "Laptop 13 (AMD Ryzen AI 300 Series)".
func (f *frameworkFeed) Search(ctx context.Context, q Query) ([]Pack, error) {
	q.defaults()
	if q.Arch != "x64" {
		return nil, nil
	}
	packs, err := f.packs(ctx, q.OS)
	if err != nil {
		return nil, err
	}
	var out []Pack
	for _, p := range packs {
		if FrameworkModelMatches(q.Model, p.Model) {
			out = append(out, p)
		}
	}
	// The closest listing first: a machine reporting "Laptop 13 (AMD Ryzen AI
	// 300 Series)" also fits the Laptop 13 Pro's page, which has a word more.
	sort.SliceStable(out, func(i, j int) bool {
		return len(frameworkTokens(out[i].Model)) < len(frameworkTokens(out[j].Model))
	})
	return out, nil
}

// frameworkTokens splits a model name into lowercase words, separating digits
// from letters ("7040Series" -> 7040 series, "12th" -> 12 th), without the
// maker's name or trademark marks.
func frameworkTokens(s string) []string {
	s = strings.ToLower(fwMarks.Replace(s))
	for prev := ""; prev != s; {
		prev = s
		s = fwSplitNum.ReplaceAllString(s, "$1$3 $2$4")
	}
	var out []string
	for _, w := range regexp.MustCompile(`[^a-z0-9]+`).Split(s, -1) {
		if w != "" && w != "framework" {
			out = append(out, w)
		}
	}
	return out
}

// FrameworkModelMatches reports whether a name — typed, picked, or the one a
// Framework machine reports about itself — is this listed model. Every word of
// the name must be in the listed name, and it must include a number: "Laptop"
// alone would match every Framework 13. The first-boot gate uses the same rule.
func FrameworkModelMatches(name, listed string) bool {
	want := frameworkTokens(name)
	have := map[string]bool{}
	for _, w := range frameworkTokens(listed) {
		have[w] = true
	}
	numeric := false
	for _, w := range want {
		if !have[w] {
			return false
		}
		if w[0] >= '0' && w[0] <= '9' {
			numeric = true
		}
	}
	return len(want) > 0 && numeric
}
