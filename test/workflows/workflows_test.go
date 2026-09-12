// Package workflows tests the GitHub Actions workflow files themselves.
//
// A shell script lives inside a YAML block scalar, and YAML ends that block at
// the first line indented less than the block. A heredoc body and its
// terminator conventionally sit at column 0 — which silently cuts the script
// short, and a `---` there even starts a second YAML document. The truncated
// remainder can still exit 0, so the job goes green having skipped the work.
// That shipped once and produced a release with no binaries attached.
package workflows

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const workflowDir = "../../.github/workflows"

func workflowFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(workflowDir)
	if err != nil {
		t.Fatalf("reading %s: %v", workflowDir, err)
	}
	var out []string
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, ".yml") || strings.HasSuffix(n, ".yaml") {
			out = append(out, filepath.Join(workflowDir, n))
		}
	}
	if len(out) == 0 {
		t.Fatalf("no workflow files under %s", workflowDir)
	}
	return out
}

// TestWorkflowsAreOneDocument catches a block scalar that ended early: the
// stray `---` that follows starts a second YAML document.
func TestWorkflowsAreOneDocument(t *testing.T) {
	for _, path := range workflowFiles(t) {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		dec := yaml.NewDecoder(f)
		n := 0
		for {
			var doc yaml.Node
			if err := dec.Decode(&doc); err != nil {
				break
			}
			n++
		}
		f.Close()
		if n != 1 {
			t.Errorf("%s parses as %d YAML documents, want 1 — a run: block scalar "+
				"probably ends early (check for lines at column 0)", filepath.Base(path), n)
		}
	}
}

var heredocStart = regexp.MustCompile(`<<-?'?"?([A-Za-z_][A-Za-z0-9_]*)`)

// TestRunScriptsAreComplete checks every step's script survived YAML parsing
// intact: an opened heredoc must be closed, and the script must not stop
// mid-continuation.
func TestRunScriptsAreComplete(t *testing.T) {
	for _, path := range workflowFiles(t) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Jobs map[string]struct {
				Steps []struct {
					Name string `yaml:"name"`
					Run  string `yaml:"run"`
				} `yaml:"steps"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(b, &doc); err != nil {
			t.Errorf("%s: %v", filepath.Base(path), err)
			continue
		}
		for job, j := range doc.Jobs {
			for _, s := range j.Steps {
				if s.Run == "" {
					continue
				}
				where := filepath.Base(path) + " / " + job + " / " + s.Name
				for _, m := range heredocStart.FindAllStringSubmatch(s.Run, -1) {
					term := m[1]
					closed := false
					for _, line := range strings.Split(s.Run, "\n") {
						if strings.TrimSpace(line) == term {
							closed = true
							break
						}
					}
					if !closed {
						t.Errorf("%s: heredoc <<%s is never closed — the block scalar "+
							"ended before its terminator (heredoc bodies at column 0 do this; "+
							"use printf instead)", where, term)
					}
				}
				if last := strings.TrimSpace(s.Run); strings.HasSuffix(last, "\\") {
					t.Errorf("%s: script ends on a line continuation — it was truncated", where)
				}
			}
		}
	}
}

// TestCatalogHealthActuallyChecks guards the health job against the failure it
// exists to prevent: a check that runs and proves nothing. `-short` skips
// every network test, so a stray -short here would turn the whole workflow
// into a green tick that never touches a single URL — the precise silence it
// was built to break.
func TestCatalogHealthActuallyChecks(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(workflowDir, "catalog-health.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		On   map[string]any `yaml:"on"`
		Jobs map[string]struct {
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	all := ""
	for _, j := range doc.Jobs {
		for _, s := range j.Steps {
			for _, line := range strings.Split(s.Run, "\n") {
				if !strings.HasPrefix(strings.TrimSpace(line), "#") {
					all += line + "\n"
				}
			}
		}
	}
	if strings.Contains(all, "-short") {
		t.Error("catalog-health.yml passes -short, which skips every network test — " +
			"the job would pass without checking a single URL")
	}
	if !strings.Contains(all, "TestCatalogURLsLive") {
		t.Error("catalog-health.yml no longer runs TestCatalogURLsLive — nothing checks for link rot")
	}
	// A check nobody is told about is not a check.
	if !strings.Contains(all, "gh issue") {
		t.Error("catalog-health.yml does not raise an issue on failure — a failed " +
			"scheduled run notifies nobody who reads it")
	}
	if _, ok := doc.On["schedule"]; !ok {
		t.Error("catalog-health.yml has no schedule — it would only ever run by hand")
	}
}

// TestReleasePublishes is the specific regression: the release job must still
// actually create the release with its built assets.
func TestReleasePublishes(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(workflowDir, "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Jobs map[string]struct {
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	// Comment lines are stripped: these checks are about what the job runs, and
	// a comment explaining a past mistake should not read as committing it.
	all := ""
	for _, j := range doc.Jobs {
		for _, s := range j.Steps {
			for _, line := range strings.Split(s.Run, "\n") {
				if !strings.HasPrefix(strings.TrimSpace(line), "#") {
					all += line + "\n"
				}
			}
		}
	}
	for _, want := range []string{"gh release create", "dist/*", "SHA256SUMS.txt"} {
		if !strings.Contains(all, want) {
			t.Errorf("release.yml no longer contains %q — the release would publish nothing", want)
		}
	}
	// checkout can leave a lightweight tag ref, and git then falls back to the
	// tagged commit's message with no error — which once published a release
	// whose notes described a CI fix instead of the features. Read the tag
	// object through the API instead.
	if regexp.MustCompile(`git tag -l[^\n]*--format`).MatchString(all) {
		t.Error("release.yml reads the tag message with `git tag -l --format` — " +
			"that silently yields the COMMIT message when the local ref is lightweight; " +
			"use the git/tags API")
	}
	// Deleting a tag demotes its release to a draft, so the release a re-run
	// finds is usually one that needs publishing again, not just re-describing.
	if strings.Contains(all, "gh release edit") && !strings.Contains(all, "--draft=false") {
		t.Error("the `gh release edit` path does not pass --draft=false — a re-run " +
			"would leave the release an unpublished draft")
	}
}
