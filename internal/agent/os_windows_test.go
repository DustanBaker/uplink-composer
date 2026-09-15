//go:build windows

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// The task description must ask for a standard-user token in the signed-in
// user's session: that is the whole reason this path exists, and an elevated
// task would fail exactly as the direct install did.
func TestTaskXMLAsksForAStandardUserToken(t *testing.T) {
	x := taskXML(`CORP\user`, `C:\Windows\Setup\Scripts\dsky-agent.exe`, `C:\job.json`, `C:\res.json`)
	for _, want := range []string{
		"<RunLevel>LeastPrivilege</RunLevel>",
		"<LogonType>InteractiveToken</LogonType>",
		"<UserId>CORP\\user</UserId>",
		`user-install "C:\job.json" "C:\res.json"`,
	} {
		if !strings.Contains(x, want) {
			t.Errorf("the task description is missing %q", want)
		}
	}
	// schtasks refuses a file it cannot read as Unicode.
	b := utf16LE(x)
	if len(b) < 2 || b[0] != 0xff || b[1] != 0xfe {
		t.Fatal("no byte-order mark")
	}
	dec := string(utf16.Decode(func() []uint16 {
		var u []uint16
		for i := 2; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
		}
		return u
	}()))
	if dec != x {
		t.Error("the encoded task description does not read back as it was written")
	}
}

// The unelevated half runs the job it was handed and reports its own result,
// so nothing has to be parsed out of Task Scheduler's localised output.
func TestUserJobReportsItsOwnResult(t *testing.T) {
	dir := t.TempDir()
	job := filepath.Join(dir, "job.json")
	res := filepath.Join(dir, "res.json")
	os.WriteFile(job, []byte(`{"exe":"cmd.exe","args":["/c","exit 7"]}`), 0o644)
	if err := RunUserJob(job, res); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"code":7`) {
		t.Errorf("the result was %s, want the job's own exit code", b)
	}
}

// A policy the machine allows is written; the debloat step reports the ones
// Windows refuses rather than spraying an error into the log.
func TestSetPolicyWritesTheRegistry(t *testing.T) {
	a := &Agent{}
	p := policy{Path: `HKCU\Software\DSKY-test`, Name: "Probe", DWord: 1}
	if err := a.setPolicy(p); err != nil {
		t.Fatalf("writing a value under HKCU: %v", err)
	}
	if err := a.setPolicy(policy{Path: `HKNOPE\Software\x`, Name: "n", DWord: 1}); err == nil {
		t.Error("an unknown hive was accepted")
	}
}

// The files the two copies pass between them must live where a standard user
// can write. C:\Windows\Setup\Scripts, where the agent itself lives, is
// read-only for one, which would fail the install for the one reason this
// path exists to avoid.
func TestUserJobFilesAreWritableByAStandardUser(t *testing.T) {
	a := &Agent{Dir: `C:\Windows\Setup\Scripts`}
	// runAsSignedInUser fails early here (no task scheduler in a test), but
	// it writes the job file first, and where it writes is the point.
	a.J, _ = OpenJournal(t.TempDir())
	_, _ = a.runAsSignedInUser("cmd.exe", []string{"/c", "exit 0"})
	job := filepath.Join(os.TempDir(), "dsky-user-job.json")
	if _, err := os.Stat(job); err != nil {
		t.Errorf("the job was not written to the user's temp directory: %v", err)
	}
	os.Remove(job)
	if _, err := os.Stat(filepath.Join(`C:\Windows\Setup\Scripts`, "dsky-user-job.json")); err == nil {
		t.Error("the job was written beside the agent, where a standard user cannot write")
	}
}
