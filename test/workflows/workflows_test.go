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
	all := ""
	for _, j := range doc.Jobs {
		for _, s := range j.Steps {
			all += s.Run + "\n"
		}
	}
	for _, want := range []string{"gh release create", "dist/*", "SHA256SUMS.txt"} {
		if !strings.Contains(all, want) {
			t.Errorf("release.yml no longer contains %q — the release would publish nothing", want)
		}
	}
}
