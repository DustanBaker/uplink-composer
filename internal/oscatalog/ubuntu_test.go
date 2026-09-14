package oscatalog

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/uplinkresearch/dsky/internal/appcatalog"
	"github.com/uplinkresearch/dsky/internal/library"
)

type autoinstallDoc struct {
	Autoinstall struct {
		Version     int      `yaml:"version"`
		Interactive []string `yaml:"interactive-sections"`
		Identity    any      `yaml:"identity"`
		Storage     struct {
			Layout struct {
				Name string `yaml:"name"`
			} `yaml:"layout"`
		} `yaml:"storage"`
		Packages []string `yaml:"packages"`
		Snaps    []struct {
			Name    string `yaml:"name"`
			Classic bool   `yaml:"classic"`
		} `yaml:"snaps"`
		LateCommands []string `yaml:"late-commands"`
		Shutdown     string   `yaml:"shutdown"`
	} `yaml:"autoinstall"`
}

func TestUbuntuUserData(t *testing.T) {
	plan, err := appcatalog.ResolveUbuntu([]string{"vlc", "vscode", "obsidian", "chrome"})
	if err != nil {
		t.Fatal(err)
	}
	for _, desktop := range []bool{false, true} {
		out := ubuntuUserData(plan, desktop)
		if !strings.HasPrefix(out, "#cloud-config\n") || strings.Contains(out, "{{") {
			t.Fatalf("desktop=%v: not a plain cloud-config:\n%s", desktop, out)
		}
		var doc autoinstallDoc
		if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("desktop=%v: not YAML: %v\n%s", desktop, err, out)
		}
		a := doc.Autoinstall
		if a.Version != 1 || a.Storage.Layout.Name != "direct" || a.Identity != nil {
			t.Errorf("desktop=%v: %+v (no account may be in DSKY's answers)", desktop, a)
		}
		// Server asks for the account on screen; Desktop's installer asks itself.
		if server := !desktop; server != (len(a.Interactive) == 1 && a.Interactive[0] == "identity") {
			t.Errorf("desktop=%v: interactive sections %v", desktop, a.Interactive)
		}
		if strings.Join(a.Packages, ",") != "vlc" || len(a.Snaps) != 1 || !a.Snaps[0].Classic {
			t.Errorf("desktop=%v: packages %v snaps %+v", desktop, a.Packages, a.Snaps)
		}
		// The first-boot script travels base64-encoded in a late-command;
		// decode it and check bash can parse it.
		var script string
		for _, c := range a.LateCommands {
			if strings.Contains(c, "dsky-apps.sh") && strings.HasPrefix(c, "echo ") {
				b64 := strings.Fields(c)[1]
				raw, err := base64.StdEncoding.DecodeString(b64)
				if err != nil {
					t.Fatal(err)
				}
				script = string(raw)
			}
		}
		for _, want := range []string{"flatpak install --system -y --noninteractive flathub md.obsidian.Obsidian", "google-chrome-stable", "/var/lib/dsky/apps-done"} {
			if !strings.Contains(script, want) {
				t.Errorf("first-boot script lacks %q", want)
			}
		}
		if bash, err := exec.LookPath("bash"); err == nil {
			f := filepath.Join(t.TempDir(), "dsky-apps.sh")
			os.WriteFile(f, []byte(script), 0o755)
			if out, err := exec.Command(bash, "-n", f).CombinedOutput(); err != nil {
				t.Errorf("first-boot script does not parse: %v\n%s", err, out)
			}
		}
	}

	// Nothing for first boot: no late-commands at all.
	plain, _ := appcatalog.ResolveUbuntu([]string{"vlc"})
	if strings.Contains(ubuntuUserData(plain, true), "late-commands") {
		t.Error("late-commands written with nothing to install on first boot")
	}
}

func TestUbuntuProgramsReachTheRecipe(t *testing.T) {
	lib, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for id, wantPatch := range map[string]bool{"ubuntu-26.04-server": true, "ubuntu-24.04-server": true, "ubuntu-26.04-desktop": false} {
		e, ok := Get(id)
		if !ok {
			t.Fatalf("%s missing", id)
		}
		if !e.ProgramsSupported() {
			t.Fatalf("%s: programs not offered", id)
		}
		dir, err := scaffoldQuickWorkspace(lib, e, Options{Apps: []string{"vlc", "brave"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		r := assertLoads(t, dir, e.ID)
		a := r.Linux
		if a == nil || a.Autoinstall == nil || a.Autoinstall.PatchKernel() != wantPatch {
			t.Fatalf("%s: autoinstall %+v, want kernel_patch %v", id, a, wantPatch)
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(a.Autoinstall.UserData))); err != nil {
			t.Fatalf("%s: user data not written: %v", id, err)
		}
		// Without programs, Ubuntu is written as it is.
		dir, _ = scaffoldQuickWorkspace(lib, e, Options{}, nil)
		if r := assertLoads(t, dir, e.ID); r.Linux != nil && r.Linux.Autoinstall != nil {
			t.Fatalf("%s: autoinstall without programs", id)
		}
	}
	fedora, _ := Get("fedora-44-workstation")
	if err := CheckPrograms(fedora, []string{"vlc"}); err == nil {
		t.Fatal("programs accepted for Fedora")
	}
	win, _ := Get("windows-11")
	if err := CheckPrograms(win, []string{"nosuchprogram"}); err == nil {
		t.Fatal("an unknown program accepted for Windows")
	}
}
