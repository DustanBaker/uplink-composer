package agent

import (
	"strings"
	"time"
)

// Provisioning outlives a single sign-in. Windows asks for a restart after a
// driver sweep, machines crash part way through -- an HP EliteBook bugchecked
// four minutes after 282 driver packages were installed under it -- and people
// close lids. The agent has always kept a state file so it could carry on
// where it left off, and until now nothing on the machine ever started it
// again: firstboot.cmd runs from the answer file's FirstLogonCommands, which
// fire once, at the first sign-in, and never again. A machine interrupted at
// the wrong moment stopped being provisioned, silently, with no error anywhere
// to say so.
//
// So the agent registers a task at the start of every run that starts it again
// at the next sign-in, and removes that task when it has genuinely finished.

// resumeTask is the name of the registered task, and the name to look for on a
// machine that is behaving oddly.
const resumeTask = "DSKY-resume"

// restartDelaySeconds is how long Windows warns before going down, once the
// decision is made, and how long somebody has to run `shutdown /a`.
const restartDelaySeconds = 10

// restartCountdown is how long the screen counts down in front of whoever is
// standing there before restarting on its own.
const restartCountdown = 60 * time.Second

// maxReboots bounds the restarts one build may ask for. Two covers the driver
// sweep asking and something later asking again; a third would mean a step
// that always asks, and looping a machine is worse than finishing without the
// restart.
const maxReboots = 2

// MaxRestarts is maxReboots for the answer file's sake: the number of
// automatic sign-ins it must ask for, beyond the first boot, so a machine the
// agent restarts comes back and carries on.
const MaxRestarts = maxReboots

// resumeTaskXML describes the task: at sign-in, elevated, once, with no time
// limit, and not held back by running on battery -- a laptop part way through
// provisioning is often on its own.
func resumeTaskXML(user, exe, dir string) string {
	esc := func(s string) string {
		r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
		return r.Replace(s)
	}
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>DSKY: carry on provisioning this machine after a restart</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>` + esc(user) + `</UserId>
      <Delay>PT1M</Delay>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>` + esc(user) + `</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>HighestAvailable</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>false</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>` + esc(exe) + `</Command>
      <Arguments>apply "` + esc(dir) + `"</Arguments>
    </Exec>
  </Actions>
</Task>
`
}

// The two acts that cannot be watched on the machine DSKY is developed on,
// reached through variables so a test can watch them: arranging to be started
// again, and taking the machine down.
var (
	ensureResumeFn    = (*Agent).ensureResume
	restartFn         = (*Agent).restart
	clearResumeFn     = (*Agent).clearResume
	disarmAutoLogonFn = (*Agent).disarmAutoLogon
	keepAwakeFn       = keepAwake
)

// finishUp is what the agent does once there is nothing left to carry on
// with: stop asking to be started again, and put away the automatic sign-in
// that was arranged so provisioning could get this far. Both only happen at
// the real end of the work -- never on the way into a restart, where the
// machine still needs both of them to come back.
func (a *Agent) finishUp() {
	clearResumeFn(a)
	disarmAutoLogonFn(a)
}

// restartAndResume restarts the machine and reports whether this run is over.
//
// It refuses to restart unless it knows the agent will be started again
// afterwards: a machine that reboots and never carries on is worse off than
// one that carries on now on a servicing stack that wanted a restart.
func (a *Agent) restartAndResume() bool {
	reason := a.rebootWanted
	a.rebootWanted = ""
	if reason == "" {
		return false
	}
	if a.State.Reboots >= maxReboots {
		a.J.Info("", "%s, but this build has already restarted the machine %d time(s); carrying on without another",
			reason, a.State.Reboots)
		return false
	}
	if err := ensureResumeFn(a); err != nil {
		a.J.FailDetail("", "not restarting: the machine would not carry on by itself afterwards", err.Error())
		return false
	}
	// In front of whoever is there. Nobody there means the countdown runs
	// out and the machine restarts, which is what a bench of machines being
	// built unattended needs.
	switch a.UI.AskRestart(reason, restartCountdown) {
	case choiceNow:
		a.J.Info("", "%s; restarting now, at the operator's say-so", reason)
	default:
		a.J.Info("", "%s; restarting and carrying on at the next sign-in", reason)
	}
	a.UI.Restarting(reason)
	a.State.CountReboot()
	if err := restartFn(a, reason); err != nil {
		a.J.FailDetail("", "could not restart the machine, carrying on without the restart", err.Error())
		return false
	}
	return true
}
