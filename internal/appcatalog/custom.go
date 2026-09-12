package appcatalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Custom programs: installers the operator supplies.
//
// The built-in list names packages in winget, which covers public software and
// nothing else. The installer that actually matters to an MSP is the one no
// package manager has — the RMM agent built for this customer, a licensed app
// with a site-specific MSI, an in-house tool. Those are the programs worth
// putting on every machine, and before this there was no way to add one
// without hand-authoring a workspace recipe.
//
// The file itself goes into the library, content-addressed like everything
// else, so it is pinned by its own hash and shared between builds. This file
// only records what it is and how to run it silently.
//
// Machine-local on purpose: Quick Install has no workspace, and an
// organisation's agent installer is usually the thing you least want to commit
// to a git repository. Sharing a set between technicians is a library
// export/import job, not a catalog one.

// CustomStoreVersion is the on-disk format of the custom-app store.
const CustomStoreVersion = 1

// CustomCategory is where operator-added programs appear in the pickers,
// which is deliberately one group rather than mixed into the built-in ones:
// "did my agent get added?" should be answerable at a glance.
const CustomCategory = "Your installers"

// Custom is one operator-supplied installer.
type Custom struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Category string    `json:"category,omitempty"`
	Format   string    `json:"format"` // msi | exe
	SHA256   string    `json:"sha256"` // the library blob holding the installer
	Filename string    `json:"filename"`
	Args     []string  `json:"args,omitempty"` // silent-install switches
	Size     int64     `json:"size"`
	AddedAt  time.Time `json:"added_at"`
}

// SourceID is the library/manifest id the installer is filed under. Prefixed
// so an app called "git" cannot collide with a driver pack or an OS image.
func (c Custom) SourceID() string { return "app-" + c.ID }

// RunArgs are the switches to run the installer with. An MSI with no switches
// would open a wizard on a machine nobody is sitting at, so quiet is the
// default; msiexec's own /qn is applied downstream when this is empty.
func (c Custom) RunArgs() []string { return c.Args }

type customStore struct {
	Version int      `json:"version"`
	Apps    []Custom `json:"apps"`
}

var (
	customMu sync.RWMutex
	customs  []Custom
)

// idRe matches what a manifest id may contain, since SourceID becomes one.
var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// StorePath is where the custom-app records live, beside the library they
// reference.
func StorePath(root string) string { return filepath.Join(root, "apps.json") }

// LoadCustom reads the operator's programs and makes them part of Catalog().
// Called once at startup. A missing store is the normal first-run state, not
// an error; a corrupt one is reported so it can be fixed rather than silently
// emptying somebody's list.
func LoadCustom(root string) error {
	b, err := os.ReadFile(StorePath(root))
	if err != nil {
		if os.IsNotExist(err) {
			// The list is whatever this root says, and this root says none.
			// Clearing rather than returning early matters when the root
			// changes (--library): the previous root's programs must not
			// linger as if they were still available.
			customMu.Lock()
			customs = nil
			customMu.Unlock()
			return nil
		}
		return err
	}
	var s customStore
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("%s is not readable: %w", StorePath(root), err)
	}
	if s.Version > CustomStoreVersion {
		return fmt.Errorf("%s was written by a newer version of this tool (format %d, this build reads %d)",
			StorePath(root), s.Version, CustomStoreVersion)
	}
	customMu.Lock()
	customs = s.Apps
	customMu.Unlock()
	return nil
}

// CustomApps returns the operator's programs, newest listing order by name.
func CustomApps() []Custom {
	customMu.RLock()
	defer customMu.RUnlock()
	out := make([]Custom, len(customs))
	copy(out, customs)
	return out
}

// GetCustom returns the operator program with this id.
func GetCustom(id string) (Custom, bool) {
	for _, c := range CustomApps() {
		if strings.EqualFold(c.ID, id) {
			return c, true
		}
	}
	return Custom{}, false
}

func saveCustom(root string, list []Custom) error {
	sort.Slice(list, func(i, j int) bool {
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
	b, err := json.MarshalIndent(customStore{Version: CustomStoreVersion, Apps: list}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	// Written through a temporary file: a half-written store would lose every
	// program the operator has added, not just the one being changed.
	tmp := StorePath(root) + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, StorePath(root)); err != nil {
		return err
	}
	customMu.Lock()
	customs = list
	customMu.Unlock()
	return nil
}

// CheckCustomID validates everything knowable before the installer is copied
// into the library, so a bad id costs nothing instead of a few hundred
// megabytes of pointless copying.
func CheckCustomID(id, name, format string) error {
	switch {
	case id == "":
		return fmt.Errorf("an id is required (it is what --apps takes)")
	case !idRe.MatchString(id):
		return fmt.Errorf("id %q must be lowercase letters, digits, dot, dash or underscore, starting with a letter or digit", id)
	case name == "":
		return fmt.Errorf("a name is required — it is what the picker shows")
	case format != "msi" && format != "exe":
		return fmt.Errorf("format %q must be msi or exe", format)
	}
	// A built-in id would be ambiguous in --apps and silently shadow the other.
	if _, clash := builtinByID(id); clash {
		return fmt.Errorf("%q is already a built-in program — choose another id", id)
	}
	return nil
}

// ValidateCustom checks a complete record before it is stored, so a broken
// entry surfaces here rather than as a confusing manifest error several
// gigabytes into a build.
func ValidateCustom(c Custom) error {
	if err := CheckCustomID(c.ID, c.Name, c.Format); err != nil {
		return err
	}
	if c.SHA256 == "" || c.Filename == "" {
		return fmt.Errorf("the installer was not filed in the library")
	}
	return nil
}

// AddCustom stores a program. Without replace, an existing id is an error
// rather than a silent overwrite: re-adding usually means a new version of the
// same installer, and doing that by accident would swap what lands on every
// machine built afterwards.
func AddCustom(root string, c Custom, replace bool) error {
	if err := ValidateCustom(c); err != nil {
		return err
	}
	list := CustomApps()
	for i, existing := range list {
		if strings.EqualFold(existing.ID, c.ID) {
			if !replace {
				return fmt.Errorf("%q already exists (%s) — pass --replace to update it",
					c.ID, existing.Filename)
			}
			if c.AddedAt.IsZero() {
				c.AddedAt = existing.AddedAt
			}
			list[i] = c
			return saveCustom(root, list)
		}
	}
	if c.AddedAt.IsZero() {
		c.AddedAt = time.Now().UTC().Truncate(time.Second)
	}
	return saveCustom(root, append(list, c))
}

// UpdateCustom edits a stored program in place — a name or the silent-install
// switches — without re-importing the installer. Getting the switches wrong is
// the common mistake, and re-copying a 200 MB MSI to fix a typo would be
// silly.
func UpdateCustom(root, id string, edit func(*Custom)) (Custom, error) {
	list := CustomApps()
	for i, c := range list {
		if !strings.EqualFold(c.ID, id) {
			continue
		}
		edit(&list[i])
		if err := ValidateCustom(list[i]); err != nil {
			return Custom{}, err
		}
		return list[i], saveCustom(root, list)
	}
	return Custom{}, fmt.Errorf("no program %q — see `uplink apps`", id)
}

// RemoveCustom forgets a program. The installer stays in the library, which
// `uplink gc` reclaims once nothing references it — so removing the wrong one
// costs a re-add, not a re-download.
func RemoveCustom(root, id string) (Custom, error) {
	list := CustomApps()
	for i, c := range list {
		if strings.EqualFold(c.ID, id) {
			return c, saveCustom(root, append(list[:i:i], list[i+1:]...))
		}
	}
	return Custom{}, fmt.Errorf("no program %q — see `uplink apps`", id)
}

func builtinByID(id string) (App, bool) {
	for _, a := range builtin {
		if strings.EqualFold(a.ID, id) {
			return a, true
		}
	}
	return App{}, false
}
