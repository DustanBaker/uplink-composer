package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// newTrimmer returns a reader over b with a UTF-8 byte-order mark removed.
func newTrimmer(b []byte) *bytes.Reader {
	return bytes.NewReader(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")))
}

// Journal is the agent's record of what it did. It writes two files side by
// side: a human log at the name earlier DSKY versions used, and one JSON
// object per line for the checker and for anyone reading it by machine.
//
// Every step's result is recorded from the value the agent itself observed.
// The generated batch file it replaces logged a failed driver extract as
// "exited with 0", because cmd expands %errorlevel% inside a parenthesised
// block when the block is parsed rather than when it runs. A log that lies
// costs more than a step that fails.
type Journal struct {
	mu     sync.Mutex
	text   *os.File
	events *os.File
	dir    string
	failed []string
}

// OpenJournal creates or appends to the logs in dir.
func OpenJournal(dir string) (*Journal, error) {
	t, err := os.OpenFile(filepath.Join(dir, LogName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	e, err := os.OpenFile(filepath.Join(dir, EventsName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Close()
		return nil, err
	}
	return &Journal{text: t, events: e, dir: dir}, nil
}

// Close flushes and closes both logs.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	err := j.text.Close()
	if cerr := j.events.Close(); err == nil {
		err = cerr
	}
	return err
}

// Event is one line of the machine-readable log.
type Event struct {
	At     string `json:"at"`
	Step   string `json:"step,omitempty"`
	Level  string `json:"level"`
	Msg    string `json:"msg"`
	Detail string `json:"detail,omitempty"`
}

func (j *Journal) write(level, step, msg, detail string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	line := now.Format("[2006-01-02 15:04:05]") + " "
	if step != "" {
		line += step + ": "
	}
	if level == "FAILED" {
		line += "FAILED "
	}
	line += msg
	if detail != "" {
		line += " -- " + strings.ReplaceAll(strings.TrimSpace(detail), "\n", " | ")
	}
	fmt.Fprintln(j.text, line)
	b, _ := json.Marshal(Event{At: now.Format(time.RFC3339), Step: step, Level: level, Msg: msg, Detail: detail})
	fmt.Fprintln(j.events, string(b))
	if level == "FAILED" {
		j.failed = append(j.failed, strings.TrimSpace(step+" "+msg))
	}
}

// Info records something that went as intended.
func (j *Journal) Info(step, msg string, args ...any) {
	j.write("info", step, fmt.Sprintf(msg, args...), "")
}

// Detail records something that went as intended, with output attached.
func (j *Journal) Detail(step, msg, detail string) {
	j.write("info", step, msg, detail)
}

// Fail records something that did not work. It never stops the run: a
// machine that gets drivers and no Spotify is worth more than one that gets
// neither.
func (j *Journal) Fail(step, msg string, args ...any) {
	j.write("FAILED", step, fmt.Sprintf(msg, args...), "")
}

// FailDetail is Fail with the command output attached.
func (j *Journal) FailDetail(step, msg, detail string) {
	j.write("FAILED", step, msg, detail)
}

// Failures returns everything recorded as FAILED, for the closing summary.
func (j *Journal) Failures() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.failed...)
}

// Raw appends command output to the human log only, so a long winget
// transcript does not drown the event stream.
func (j *Journal) Raw(out string) {
	if strings.TrimSpace(out) == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, ln := range strings.Split(strings.TrimRight(out, "\r\n"), "\n") {
		fmt.Fprintln(j.text, "    "+strings.TrimRight(ln, "\r"))
	}
}
