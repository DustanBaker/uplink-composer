package ciyaml

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// A workflow file that does not parse is not a failing check: GitHub reports
// a run with no jobs at all, named after the file, which is easy to read as
// "something went wrong in CI" rather than "CI never ran".
func TestWorkflowsParse(t *testing.T) {
	dir := filepath.Join("..", "..", ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(b, &doc); err != nil {
			t.Errorf("%s does not parse: %v", e.Name(), err)
			continue
		}
		if _, ok := doc["jobs"]; !ok {
			t.Errorf("%s has no jobs", e.Name())
		}
	}
}
