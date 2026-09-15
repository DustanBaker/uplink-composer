//go:build windows

package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows/registry"
)

// machineModel reads what the machine says about itself, for driver packs
// that are only for one model. The BIOS keys are what the model gate script
// used and are there before any vendor software is installed.
func (a *Agent) machineModel() (vendor, model string) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\BIOS`, registry.QUERY_VALUE)
	if err != nil {
		return "", ""
	}
	defer k.Close()
	vendor, _, _ = k.GetStringValue("SystemManufacturer")
	model, _, _ = k.GetStringValue("SystemProductName")
	return vendor, model
}

// userJob is one install handed to an unelevated copy of the agent.
type userJob struct {
	Exe  string   `json:"exe"`
	Args []string `json:"args"`
}

// userResult is what that copy reports back.
type userResult struct {
	Code int    `json:"code"`
	Out  string `json:"out"`
	Err  string `json:"err,omitempty"`
}

// runAsSignedInUser runs a program as the signed-in user with a standard-user
// token, for installers that refuse to run elevated.
//
// It works by starting the agent again through Task Scheduler, rather than by
// asking Task Scheduler to run winget and then reading the task's status:
// task status and last-result text are localised, and a machine imaged in
// another language would have had its results misread. The second copy writes
// its own result to a file, so nothing is parsed out of console output.
func (a *Agent) runAsSignedInUser(exe string, args []string) (int, error) {
	self, err := os.Executable()
	if err != nil {
		return 0, err
	}
	jobPath := filepath.Join(a.Dir, "dsky-user-job.json")
	resPath := filepath.Join(a.Dir, "dsky-user-result.json")
	os.Remove(resPath)
	b, err := json.Marshal(userJob{Exe: exe, Args: args})
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(jobPath, b, 0o644); err != nil {
		return 0, err
	}
	defer os.Remove(jobPath)

	user := os.Getenv("USERDOMAIN") + `\` + os.Getenv("USERNAME")
	xmlPath := filepath.Join(a.Dir, "dsky-user-task.xml")
	if err := os.WriteFile(xmlPath, utf16LE(taskXML(user, self, jobPath, resPath)), 0o644); err != nil {
		return 0, err
	}
	defer os.Remove(xmlPath)

	const taskName = "DSKY-user-install"
	if r := run(2*time.Minute, "schtasks", "/create", "/tn", taskName, "/xml", xmlPath, "/f"); !r.ok() {
		return 0, fmt.Errorf("could not register the task: %s", trimOut(r.Out))
	}
	defer run(2*time.Minute, "schtasks", "/delete", "/tn", taskName, "/f")

	if r := run(2*time.Minute, "schtasks", "/run", "/tn", taskName); !r.ok() {
		return 0, fmt.Errorf("could not start the task: %s", trimOut(r.Out))
	}

	deadline := time.Now().Add(25 * time.Minute)
	for {
		if b, err := os.ReadFile(resPath); err == nil {
			var res userResult
			if err := json.Unmarshal(b, &res); err != nil {
				return 0, fmt.Errorf("the standard-user install wrote an unreadable result: %w", err)
			}
			os.Remove(resPath)
			a.J.Raw(res.Out)
			if res.Err != "" {
				return res.Code, fmt.Errorf("%s", res.Err)
			}
			return res.Code, nil
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("the standard-user install did not finish within 25 minutes")
		}
		time.Sleep(5 * time.Second)
	}
}

// RunUserJob is the unelevated half: the agent started again by Task
// Scheduler in the signed-in user's session. It runs the one command it was
// given and writes the result where the elevated copy is waiting.
func RunUserJob(jobPath, resultPath string) error {
	b, err := os.ReadFile(jobPath)
	if err != nil {
		return err
	}
	var job userJob
	if err := json.Unmarshal(b, &job); err != nil {
		return err
	}
	r := run(25*time.Minute, job.Exe, job.Args...)
	res := userResult{Code: r.Code, Out: r.Out}
	if r.Err != nil {
		res.Err = r.Err.Error()
	}
	out, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return os.WriteFile(resultPath, out, 0o644)
}

// taskXML describes a one-off task that runs as the given user with a
// standard-user token. RunLevel LeastPrivilege is the whole point.
func taskXML(user, exe, jobPath, resultPath string) string {
	esc := func(s string) string {
		r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
		return r.Replace(s)
	}
	args := fmt.Sprintf(`user-install "%s" "%s"`, jobPath, resultPath)
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>DSKY: install a program that refuses to run elevated</Description>
  </RegistrationInfo>
  <Principals>
    <Principal id="Author">
      <UserId>` + esc(user) + `</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>false</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <ExecutionTimeLimit>PT25M</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>` + esc(exe) + `</Command>
      <Arguments>` + esc(args) + `</Arguments>
    </Exec>
  </Actions>
</Task>
`
}

// utf16LE encodes with a byte-order mark: schtasks /xml rejects a file it
// cannot recognise as Unicode.
func utf16LE(s string) []byte {
	out := []byte{0xff, 0xfe}
	for _, r := range utf16.Encode([]rune(s)) {
		out = append(out, byte(r), byte(r>>8))
	}
	return out
}

// setPolicy writes one registry value.
func (a *Agent) setPolicy(p policy) error {
	root, path, ok := splitHive(p.Path)
	if !ok {
		return fmt.Errorf("%s: unknown registry hive", p.Path)
	}
	k, _, err := registry.CreateKey(root, path, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if p.String != "" {
		return k.SetStringValue(p.Name, p.String)
	}
	return k.SetDWordValue(p.Name, uint32(p.DWord))
}

func splitHive(path string) (registry.Key, string, bool) {
	switch {
	case strings.HasPrefix(path, `HKLM\`):
		return registry.LOCAL_MACHINE, path[5:], true
	case strings.HasPrefix(path, `HKCU\`):
		return registry.CURRENT_USER, path[5:], true
	}
	return 0, "", false
}

// removeAppx takes the consumer apps off the machine. Appx packages have no
// command-line interface worth the name, so this is the one place the agent
// still uses PowerShell — with a fixed script written by the agent, not text
// assembled per build, and handed its list as a file so nothing has to be
// quoted into a command line.
func (a *Agent) removeAppx(prefixes []string) {
	listPath := filepath.Join(a.Dir, "dsky-appx-list.txt")
	if err := os.WriteFile(listPath, []byte(strings.Join(prefixes, "\r\n")+"\r\n"), 0o644); err != nil {
		a.J.FailDetail(stepDebloat, "could not write the app list", err.Error())
		return
	}
	defer os.Remove(listPath)
	scriptPath := filepath.Join(a.Dir, "dsky-appx.ps1")
	// The byte-order mark is deliberate: Windows PowerShell reads a file
	// without one in the machine's legacy code page.
	if err := os.WriteFile(scriptPath, append([]byte("\xef\xbb\xbf"), appxScript...), 0o644); err != nil {
		a.J.FailDetail(stepDebloat, "could not write the removal script", err.Error())
		return
	}
	defer os.Remove(scriptPath)

	r := run(30*time.Minute, "powershell", "-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass", "-File", scriptPath, "-ListFile", listPath)
	a.J.Raw(r.Out)
	removed := 0
	for _, ln := range strings.Split(r.Out, "\n") {
		ln = strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(ln, "REMOVED "):
			removed++
		case strings.HasPrefix(ln, "FAILED "):
			a.J.Fail(stepDebloat, "%s", strings.TrimPrefix(ln, "FAILED "))
		}
	}
	if r.Err != nil || (!r.ok() && removed == 0) {
		a.J.FailDetail(stepDebloat, "removing consumer apps did not run", trimOut(r.Out))
		return
	}
	a.J.Info(stepDebloat, "removed %d consumer app(s)", removed)
}

// appxScript is constant, so it is the same text on every build and can be
// checked once. It prints one line per package, which the agent counts.
const appxScript = `param([Parameter(Mandatory=$true)][string]$ListFile)
$ErrorActionPreference = 'Continue'
$names = Get-Content -LiteralPath $ListFile | Where-Object { $_.Trim() -ne '' }
$prov = @(Get-AppxProvisionedPackage -Online)
$installed = @(Get-AppxPackage -AllUsers)
foreach ($name in $names) {
  foreach ($p in ($prov | Where-Object { $_.DisplayName -like "$name*" })) {
    try {
      Remove-AppxProvisionedPackage -Online -PackageName $p.PackageName -ErrorAction Stop | Out-Null
      Write-Output ("REMOVED provisioned " + $p.DisplayName)
    } catch {
      Write-Output ("FAILED deprovision " + $p.DisplayName + ": " + $_.Exception.Message)
    }
  }
  foreach ($p in ($installed | Where-Object { $_.Name -like "$name*" })) {
    try {
      Remove-AppxPackage -AllUsers -Package $p.PackageFullName -ErrorAction Stop
      Write-Output ("REMOVED " + $p.Name)
    } catch {
      Write-Output ("FAILED remove " + $p.Name + ": " + $_.Exception.Message)
    }
  }
}
Write-Output "APPX DONE"
`
