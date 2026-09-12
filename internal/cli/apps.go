package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DustanBaker/uplink-composer/internal/appcatalog"
	"github.com/DustanBaker/uplink-composer/internal/library"
	"github.com/DustanBaker/uplink-composer/internal/manifest"
)

const appsUsage = `Manage the programs Quick Install can add.

  apps                            list every program
  apps add <installer> [flags]    add your own .msi or .exe
  apps set <id> [flags]           change a name, category or switches
  apps remove <id>                forget one (the file stays in the library)
  apps show <id>                  everything recorded about one

add flags:
  --id <name>        what --apps takes (default: derived from the filename)
  --name "<text>"    what the picker shows (default: the filename)
  --args "<switches>"  silent-install switches (default: /qn for .msi)
  --category "<text>"  picker grouping (default: %q)
  --replace          update an existing id instead of refusing

Example:
  uplink apps add "C:\\installers\\MaculaAgent.msi" --id macula --name "Macula Agent"
  uplink install windows-11 --apps macula,chrome
`

func cmdApps(ctx context.Context, env *Env, args []string) error {
	if len(args) == 0 {
		return listApps()
	}
	switch args[0] {
	case "add":
		return appsAdd(env, args[1:])
	case "set":
		return appsSet(env, args[1:])
	case "remove", "rm", "delete":
		return appsRemove(env, args[1:])
	case "show":
		return appsShow(args[1:])
	case "help", "--help", "-h":
		fmt.Printf(appsUsage, appcatalog.CustomCategory)
		return nil
	}
	return fmt.Errorf("unknown apps command %q\n\n"+appsUsage, args[0], appcatalog.CustomCategory)
}

func listApps() error {
	fmt.Println("Programs `uplink install --apps` can add (comma-separated ids):")
	for _, cat := range appcatalog.Categories() {
		fmt.Printf("\n  %s\n", cat)
		for _, a := range appcatalog.Catalog() {
			if a.Category != cat {
				continue
			}
			switch {
			case a.Custom != nil:
				fmt.Printf("    %-18s %s\n", a.ID, a.Name)
				fmt.Printf("      %s · %s%s\n", a.Custom.Filename,
					strings.ToUpper(a.Custom.Format), argsNote(a.Custom))
			case a.Winget == "":
				fmt.Printf("    %-18s %s  (no Windows package)\n", a.ID, a.Name)
			default:
				fmt.Printf("    %-18s %s\n", a.ID, a.Name)
			}
		}
	}
	fmt.Println("\nExample:")
	fmt.Println("  uplink install windows-11 --drivers --apps chrome,7zip,vlc")
	fmt.Println("\nBuilt-in programs install at first boot with winget, so the machine")
	fmt.Println("needs to be online then — staging its network driver (--drivers) helps.")
	fmt.Println("Your own installers ride on the stick and need no network.")
	fmt.Println("\nAdd one:  uplink apps add <installer.msi> --id <name>")
	return nil
}

func argsNote(c *appcatalog.Custom) string {
	if len(c.Args) == 0 {
		if c.Format == "msi" {
			return " · /qn"
		}
		return " · no switches"
	}
	return " · " + strings.Join(c.Args, " ")
}

func appsAdd(env *Env, args []string) error {
	fs := flag.NewFlagSet("apps add", flag.ContinueOnError)
	id := fs.String("id", "", "picker id (default: derived from the filename)")
	name := fs.String("name", "", "what the picker shows")
	argsFlag := fs.String("args", "", "silent-install switches")
	category := fs.String("category", "", "picker grouping")
	replace := fs.Bool("replace", false, "update an existing id")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("apps add <installer.msi|.exe> [--id x] [--name \"X\"] [--args \"/qn\"]")
	}
	path := fs.Arg(0)
	format, err := installerFormat(path)
	if err != nil {
		return err
	}
	lib, err := env.library()
	if err != nil {
		return err
	}

	c := appcatalog.Custom{
		ID:       firstNonEmpty(*id, deriveAppID(path)),
		Name:     firstNonEmpty(*name, filepath.Base(path)),
		Category: *category,
		Format:   format,
		Filename: filepath.Base(path),
		Args:     splitArgs(*argsFlag),
	}
	// Everything knowable is checked before the file is copied, so a rejected
	// add costs nothing rather than a few hundred megabytes of pointless
	// copying — the duplicate-id case included, which is the one people hit.
	if err := appcatalog.CheckCustomID(c.ID, c.Name, c.Format); err != nil {
		return err
	}
	if existing, ok := appcatalog.GetCustom(c.ID); ok && !*replace {
		return fmt.Errorf("%q already exists (%s) — pass --replace to update it",
			c.ID, existing.Filename)
	}

	entry, err := importInstaller(lib, c, path)
	if err != nil {
		return err
	}
	c.SHA256, c.Size = entry.SHA256, entry.Size
	if err := appcatalog.AddCustom(env.libraryRoot(), c, *replace); err != nil {
		return err
	}
	fmt.Printf("Added %s (%s) — %d MiB, sha256 %s\n", c.ID, c.Name, c.Size>>20, c.SHA256[:16])
	fmt.Printf("  runs as: %s\n", runLine(c))
	fmt.Printf("\nUse it:  uplink install windows-11 --apps %s\n", c.ID)
	return nil
}

// importInstaller files the installer in the library under the app's source
// id, so the generated recipe can refer to it by ref like any other payload.
func importInstaller(lib *library.Library, c appcatalog.Custom, path string) (library.Entry, error) {
	st, err := os.Stat(path)
	if err != nil {
		return library.Entry{}, err
	}
	if st.IsDir() {
		return library.Entry{}, fmt.Errorf("%s is a directory — point at the installer file", path)
	}
	fmt.Printf("Filing %s in the library...\n", filepath.Base(path))
	return lib.Import(&manifest.Source{
		ID:       c.SourceID(),
		Kind:     manifest.KindPayload,
		Format:   manifest.Format(c.Format),
		Filename: filepath.Base(path),
	}, path)
}

func appsSet(env *Env, args []string) error {
	fs := flag.NewFlagSet("apps set", flag.ContinueOnError)
	name := fs.String("name", "", "what the picker shows")
	argsFlag := fs.String("args", "", "silent-install switches")
	category := fs.String("category", "", "picker grouping")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("apps set <id> [--name \"X\"] [--args \"/qn /norestart\"] [--category \"X\"]")
	}
	// Only what was actually passed is changed: an unset flag must not wipe a
	// field, and "" is a legitimate value for --args meaning "no switches".
	changed := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { changed[f.Name] = true })
	if len(changed) == 0 {
		return fmt.Errorf("nothing to change — pass --name, --args or --category")
	}
	c, err := appcatalog.UpdateCustom(env.libraryRoot(), fs.Arg(0), func(c *appcatalog.Custom) {
		if changed["name"] {
			c.Name = *name
		}
		if changed["category"] {
			c.Category = *category
		}
		if changed["args"] {
			c.Args = splitArgs(*argsFlag)
		}
	})
	if err != nil {
		return err
	}
	fmt.Printf("Updated %s (%s)\n  runs as: %s\n", c.ID, c.Name, runLine(c))
	return nil
}

func appsRemove(env *Env, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("apps remove <id>")
	}
	c, err := appcatalog.RemoveCustom(env.libraryRoot(), args[0])
	if err != nil {
		return err
	}
	fmt.Printf("Removed %s (%s).\n", c.ID, c.Name)
	fmt.Printf("  %s is still in the library; `uplink gc` reclaims it once nothing refers to it.\n", c.Filename)
	return nil
}

func appsShow(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("apps show <id>")
	}
	a, ok := appcatalog.Get(args[0])
	if !ok {
		return fmt.Errorf("unknown program %q — see `uplink apps`", args[0])
	}
	fmt.Printf("%s — %s\n", a.ID, a.Name)
	fmt.Printf("  category: %s\n", a.Category)
	if a.Custom == nil {
		fmt.Printf("  built in\n")
		if a.Winget != "" {
			fmt.Printf("  winget:   %s\n", a.Winget)
		}
		if a.Apt != "" {
			fmt.Printf("  apt:      %s\n", a.Apt)
		}
		return nil
	}
	c := a.Custom
	fmt.Printf("  yours, added %s\n", c.AddedAt.Format("2006-01-02"))
	fmt.Printf("  file:     %s (%d MiB)\n", c.Filename, c.Size>>20)
	fmt.Printf("  sha256:   %s\n", c.SHA256)
	fmt.Printf("  runs as:  %s\n", runLine(*c))
	return nil
}

// runLine shows the command the first-boot script will run, because a silent
// switch that is wrong produces a machine that sits on an installer dialog
// forever and nobody finds out until they look at it.
func runLine(c appcatalog.Custom) string {
	if c.Format == "msi" {
		args := strings.Join(c.Args, " ")
		if args == "" {
			args = "/qn"
		}
		return fmt.Sprintf(`msiexec /i "%s" %s`, c.Filename, args)
	}
	return strings.TrimSpace(fmt.Sprintf(`"%s" %s`, c.Filename, strings.Join(c.Args, " ")))
}

func installerFormat(path string) (string, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".msi":
		return "msi", nil
	case ".exe":
		return "exe", nil
	}
	return "", fmt.Errorf("%s is not a .msi or .exe — those are what the first-boot script can run",
		filepath.Base(path))
}

// deriveAppID makes a usable picker id from a filename, so the common case
// needs no --id at all.
func deriveAppID(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	var b strings.Builder
	for _, r := range strings.ToLower(base) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-._")
}

// splitArgs turns a switch string into arguments. Quoted runs are kept whole,
// since installer switches routinely carry paths and keys with spaces.
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	quote := rune(0)
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
