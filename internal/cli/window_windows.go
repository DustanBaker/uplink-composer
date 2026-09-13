package cli

import (
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows/registry"
)

// The portal in its own window.
//
// Nothing about the portal changes: same page, same server, same API. The only
// difference is where it is drawn. Opening it as a tab in somebody's browser
// is what made it feel like a dev tool rather than an application — there is a
// URL bar, a token in the address, other tabs beside it, and closing it is a
// gesture that means "close a web page" rather than "quit this program".
//
// WebView2 renders it, which is the browser engine already on the machine, so
// nothing is bundled and the binary stays a few megabytes rather than the
// hundred-odd that shipping a browser costs. The bindings are pure Go, so the
// CGO-free single-binary build is untouched.
func showWindow(url, title string) bool {
	dpiAware()
	width, height := windowSize()
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  title,
			Width:  width,
			Height: height,
			Center: true,
		},
	})
	// nil means no WebView2 runtime on this machine. It ships with Windows 11
	// and reaches almost every Windows 10, but "almost" is not "always", so
	// this reports rather than crashes and the caller opens a browser instead.
	if w == nil {
		return false
	}
	defer w.Destroy()

	// Terminate is documented as safe from a background thread, which is the
	// only reason the updater can close this window: it is asking from an HTTP
	// handler's goroutine, not from the loop below.
	setWindowCloser(w.Terminate)
	defer setWindowCloser(nil)

	// The frame is Windows' to draw, not ours, and left alone it draws a light
	// title bar above a dark page. Tell it which way we are going, and keep
	// telling it, so flipping the system theme is reflected while the window
	// is open rather than at the next launch.
	stopTheme := followSystemTitleBar(uintptr(w.Window()))
	defer stopTheme()
	// On Windows 11 the frame can go further than dark: the page's own
	// near-black, so the title bar and the header read as one surface.
	// Windows 10 rejects the attribute and keeps the dark frame above.
	setTitleBarColor(uintptr(w.Window()), 0x05, 0x05, 0x0f, 0xc8, 0xc8, 0xd4)

	w.Navigate(url)
	// Blocks until the window is closed, which is the whole lifecycle: the
	// close button means quit, with nothing to remember and nothing left
	// running behind it.
	w.Run()
	return true
}

var (
	user32                        = syscall.NewLazyDLL("user32.dll")
	findWindowW                   = user32.NewProc("FindWindowW")
	setForegroundWindow           = user32.NewProc("SetForegroundWindow")
	showWindowAsync               = user32.NewProc("ShowWindowAsync")
	isIconic                      = user32.NewProc("IsIconic")
	setProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	setProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	getDpiForSystem               = user32.NewProc("GetDpiForSystem")
	systemParametersInfoW         = user32.NewProc("SystemParametersInfoW")
)

var dwmSetWindowAttribute = syscall.NewLazyDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")

// followSystemTitleBar paints the title bar to match the system theme and
// keeps it matching, returning a function that stops watching.
//
// There is no way to be told about this through the window, because the
// message loop belongs to the webview, so the setting is read instead. It is
// one small registry value, read every couple of seconds and only acted on
// when it actually changes — cheap enough to be the boring answer, and it
// means someone flipping Windows into light mode sees the window follow rather
// than sitting there conspicuously wrong until they restart it.
func followSystemTitleBar(hwnd uintptr) func() {
	return watchTheme(systemPrefersDark,
		func(dark bool) { setTitleBarDark(hwnd, dark) }, 2*time.Second)
}

// watchTheme applies what read reports now, then re-applies whenever it
// changes, until the returned function is called.
//
// The first apply is synchronous, before the window is navigated: doing it in
// the goroutine races the window becoming visible, and losing that race shows
// a light title bar that turns dark a moment later.
func watchTheme(read func() bool, apply func(bool), every time.Duration) func() {
	cur := read()
	apply(cur)

	done := make(chan struct{})
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if now := read(); now != cur {
					cur = now
					apply(now)
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// systemPrefersDark reads Windows' own app-theme setting. Absent or
// unreadable means light, which is the Windows default.
func systemPrefersDark() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`,
		registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	light, _, err := k.GetIntegerValue("AppsUseLightTheme")
	if err != nil {
		return false
	}
	return light == 0
}

// setTitleBarDark asks DWM for a dark or light frame.
func setTitleBarDark(hwnd uintptr, dark bool) {
	if hwnd == 0 || dwmSetWindowAttribute.Find() != nil {
		return
	}
	var on int32
	if dark {
		on = 1
	}
	// 20 is DWMWA_USE_IMMERSIVE_DARK_MODE. It was 19 before Windows 10 20H1,
	// when the attribute was undocumented, and the older builds that still use
	// 19 reject 20 — so try the current one and fall back.
	for _, attr := range []uintptr{20, 19} {
		if r, _, _ := dwmSetWindowAttribute.Call(hwnd, attr,
			uintptr(unsafe.Pointer(&on)), unsafe.Sizeof(on)); r == 0 {
			return
		}
	}
}

// setTitleBarColor paints the caption background and text. The values are
// COLORREFs, 0x00BBGGRR. 35 is DWMWA_CAPTION_COLOR and 36 DWMWA_TEXT_COLOR,
// both Windows 11 only; an explicit caption colour outlasts the light/dark
// switch above, which is why the text colour is set with it.
func setTitleBarColor(hwnd uintptr, r, g, b, tr, tg, tb uint32) {
	if hwnd == 0 || dwmSetWindowAttribute.Find() != nil {
		return
	}
	caption := b<<16 | g<<8 | r
	text := tb<<16 | tg<<8 | tr
	dwmSetWindowAttribute.Call(hwnd, 35, uintptr(unsafe.Pointer(&caption)), unsafe.Sizeof(caption))
	dwmSetWindowAttribute.Call(hwnd, 36, uintptr(unsafe.Pointer(&text)), unsafe.Sizeof(text))
}

const (
	swRestore = 9
	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2, which is the handle value -4.
	perMonitorAwareV2 = ^uintptr(3)
	spiGetWorkArea    = 0x0030
)

// dpiAware asks Windows for real pixels.
//
// Without this the process is DPI-unaware, so Windows quietly renders the whole
// window at 96 DPI and scales the result up like a bitmap — on a display at
// 150% that is every glyph blown up 1.5x and blurred. It is the single most
// visible difference between something that looks like an application and
// something that looks like a wrapper around a web page, and it costs one call.
//
// Must run before the window exists, which is why showWindow calls it first.
func dpiAware() {
	// Per-monitor v2 where it exists (Windows 10 1703+); it also keeps the
	// window sharp when dragged to a monitor with a different scale factor.
	if setProcessDpiAwarenessContext.Find() == nil {
		if ok, _, _ := setProcessDpiAwarenessContext.Call(perMonitorAwareV2); ok != 0 {
			return
		}
	}
	// Older Windows: one system-wide scale factor, still sharp at that factor.
	if setProcessDPIAware.Find() == nil {
		setProcessDPIAware.Call()
	}
}

// windowSize is the default window in physical pixels: the size we actually
// want, scaled for the display, and never bigger than the screen it opens on.
//
// The clamp is not hypothetical. 860 points at 150% is 1290 pixels tall, and a
// 1080p laptop — the machine a technician is most likely to be holding — has
// about 1040 pixels of usable height. Unclamped, the window would open with its
// bottom edge off the screen.
func windowSize() (uint, uint) {
	const wantW, wantH = 1280, 860

	scale := 1.0
	if getDpiForSystem.Find() == nil {
		if dpi, _, _ := getDpiForSystem.Call(); dpi > 0 {
			scale = float64(dpi) / 96
		}
	}
	w, h := float64(wantW)*scale, float64(wantH)*scale

	// The work area excludes the taskbar, so "fits the screen" means fits the
	// part of it a window may actually occupy.
	var wa struct{ left, top, right, bottom int32 }
	if systemParametersInfoW.Find() == nil {
		ok, _, _ := systemParametersInfoW.Call(spiGetWorkArea, 0,
			uintptr(unsafe.Pointer(&wa)), 0)
		if ok != 0 {
			if aw := float64(wa.right - wa.left); aw > 0 && w > aw*0.95 {
				w = aw * 0.95
			}
			if ah := float64(wa.bottom - wa.top); ah > 0 && h > ah*0.95 {
				h = ah * 0.95
			}
		}
	}
	return uint(w), uint(h)
}

// focusWindow brings an already-open portal window to the front, reporting
// whether it found one.
//
// Launching the app a second time should give you back the window you already
// have — that is what double-clicking the icon of a running application does
// everywhere else. The alternative we had was a second window onto the first
// process's server, which looks the same until the first window closes and
// takes the server with it, leaving the second showing a dead page.
//
// Matched on the title, with a nil class, so it finds our window and not a
// browser showing the same page (a browser appends its own name to the title).
func focusWindow(title string) bool {
	name, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return false
	}
	h, _, _ := findWindowW.Call(0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return false
	}
	// Minimized windows need un-minimizing before raising; the async form
	// cannot be blocked by the other process being busy.
	if min, _, _ := isIconic.Call(h); min != 0 {
		showWindowAsync.Call(h, swRestore)
	}
	// Windows may refuse to hand focus to a process that is not already in the
	// foreground, and flashes the taskbar button instead. Either way the window
	// is there and visible, which is what the caller needs to know — so the
	// result of this call is deliberately not the answer we return.
	setForegroundWindow.Call(h)
	return true
}

// upgradeLauncher has nothing to do on Windows, where the shortcuts have
// always started dsky-app.
func upgradeLauncher() bool { return false }
