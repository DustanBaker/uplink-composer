package agent

const stepDebloat = "debloat"

// policy is one registry value the debloat step writes.
type policy struct {
	Path   string
	Name   string
	DWord  int
	String string
}

// policies are DSKY's opinion about a clean machine, not the operator's list,
// so they live in the agent rather than in the manifest. The manifest carries
// what the operator chose: the preset and the apps to remove.
//
// Everything here is a documented policy or per-user setting. The installed
// Windows is untouched official media, so the machine stays updatable and
// activation-safe.
func policiesFor(preset string) []policy {
	ps := []policy{
		// Consumer promotions: suggested third-party apps, "fun facts",
		// Start menu promos.
		{Path: `HKLM\SOFTWARE\Policies\Microsoft\Windows\CloudContent`, Name: "DisableWindowsConsumerFeatures", DWord: 1},
		{Path: `HKLM\SOFTWARE\Policies\Microsoft\Windows\CloudContent`, Name: "DisableSoftLanding", DWord: 1},
		{Path: `HKLM\SOFTWARE\Policies\Microsoft\Windows\CloudContent`, Name: "DisableCloudOptimizedContent", DWord: 1},
		// Copilot off, by policy and on this user's taskbar.
		{Path: `HKLM\SOFTWARE\Policies\Microsoft\Windows\WindowsCopilot`, Name: "TurnOffWindowsCopilot", DWord: 1},
		{Path: `HKCU\Software\Policies\Microsoft\Windows\WindowsCopilot`, Name: "TurnOffWindowsCopilot", DWord: 1},
		{Path: `HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced`, Name: "ShowCopilotButton", DWord: 0},
		// Widgets and news.
		{Path: `HKLM\SOFTWARE\Policies\Microsoft\Dsh`, Name: "AllowNewsAndInterests", DWord: 0},
		{Path: `HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced`, Name: "TaskbarDa", DWord: 0},
		// Advertising ID and tailored experiences.
		{Path: `HKLM\SOFTWARE\Policies\Microsoft\Windows\AdvertisingInfo`, Name: "DisabledByGroupPolicy", DWord: 1},
		// Telemetry to the Pro floor; 0 is Enterprise-only and is ignored
		// elsewhere, which would look like it had worked.
		{Path: `HKLM\SOFTWARE\Policies\Microsoft\Windows\DataCollection`, Name: "AllowTelemetry", DWord: 1},
	}
	cdm := `HKCU\Software\Microsoft\Windows\CurrentVersion\ContentDeliveryManager`
	for _, v := range []string{
		"SubscribedContent-338388Enabled", "SubscribedContent-338389Enabled",
		"SubscribedContent-353694Enabled", "SubscribedContent-353696Enabled",
		"SystemPaneSuggestionsEnabled", "SilentInstalledAppsEnabled",
		"SoftLandingEnabled", "RotatingLockScreenOverlayEnabled",
	} {
		ps = append(ps, policy{Path: cdm, Name: v, DWord: 0})
	}
	if preset == "aggressive" {
		ps = append(ps, policy{
			Path: `HKCU\Software\Policies\Microsoft\Windows\Explorer`,
			Name: "DisableSearchBoxSuggestions", DWord: 1,
		})
	}
	return ps
}

// debloatStep removes the consumer apps and writes the policies.
func (a *Agent) debloatStep() {
	d := a.Manifest.Debloat
	if d == nil {
		return
	}
	a.J.Info(stepDebloat, "removing consumer apps and promotions (preset %s)", d.Preset)
	if len(d.Apps) > 0 {
		a.removeAppx(d.Apps)
	}
	set, refused := 0, 0
	for _, p := range policiesFor(d.Preset) {
		if err := a.setPolicy(p); err != nil {
			// Windows protects a few of these from being written at all;
			// 24H2 protects the taskbar search setting. One line, not a page
			// of red text.
			a.J.Fail(stepDebloat, `%s\%s could not be set: %v`, p.Path, p.Name, err)
			refused++
			continue
		}
		set++
	}
	a.J.Info(stepDebloat, "set %d setting(s), %d refused by Windows", set, refused)
}
