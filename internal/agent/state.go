package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// State records which steps have finished, so a machine that reboots or
// crashes part way through carries on rather than starting over. A step is
// marked done only when it completed; a step that failed is left undone so a
// later run can try it again.
type State struct {
	path    string
	Started string          `json:"started"`
	Done    map[string]bool `json:"done"`
}

// LoadState reads the state beside the agent, or starts a fresh one.
func LoadState(dir string) *State {
	s := &State{path: filepath.Join(dir, StateName), Done: map[string]bool{}}
	if b, err := os.ReadFile(s.path); err == nil {
		var prev State
		if json.Unmarshal(b, &prev) == nil && prev.Done != nil {
			s.Done = prev.Done
			s.Started = prev.Started
		}
	}
	if s.Started == "" {
		s.Started = time.Now().Format(time.RFC3339)
	}
	return s
}

// Finished reports whether a step has already completed.
func (s *State) Finished(step string) bool { return s.Done[step] }

// Finish marks a step complete and saves immediately, because the next thing
// that happens may be a reboot.
func (s *State) Finish(step string) {
	s.Done[step] = true
	s.save()
}

func (s *State) save() {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(s.path, append(b, '\n'), 0o644)
}
