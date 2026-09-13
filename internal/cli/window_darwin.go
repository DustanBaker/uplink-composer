//go:build darwin

package cli

import (
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"

	"github.com/uplinkresearch/dsky/internal/webui"
)

// The portal in its own window, on macOS.
//
// WebKit is part of macOS — it is what Safari draws with — so a window of our
// own needs nothing installed. Reaching it normally takes Objective-C and a C
// compiler, which this project does not use. purego calls the system's
// frameworks from Go instead, by dynamic lookup at run time, so the binary is
// still CGO-free and still cross-compiles from any machine.
//
// The shape follows the Windows window: one window, the portal in it, and
// closing it is quitting. It also carries what a Mac app is expected to have —
// an Edit menu, so copy and paste work in the page's fields, Cmd-W and Cmd-Q,
// and a window that remembers where it was.

// Cocoa insists on the process's first thread. The main goroutine starts on
// it; locking here, before anything else can move it, keeps it there for the
// window.
func init() { runtime.LockOSThread() }

type nsRect struct{ X, Y, W, H float64 }

const (
	styleTitled         = 1 << 0
	styleClosable       = 1 << 1
	styleMiniaturizable = 1 << 2
	styleResizable      = 1 << 3
	backingBuffered     = 2
	autoresizeWidthSize = 1 << 1
	autoresizeHeight    = 1 << 4
	alertFirstButton    = 1000
	activationRegular   = 0
)

var (
	frameworksOnce sync.Once
	frameworksOK   bool

	classesOnce sync.Once
	uiDelegate  objc.ID
	winDelegate objc.ID

	// Set by the window delegate when the window closes, however it closed.
	windowClosed atomic.Bool
	// Set from any goroutine to ask the window to close; the event loop, which
	// owns the window, does the closing.
	closeRequested atomic.Bool
)

func sel(name string) objc.SEL  { return objc.RegisterName(name) }
func class(name string) objc.ID { return objc.ID(objc.GetClass(name)) }
func nsString(s string) objc.ID { return class("NSString").Send(sel("stringWithUTF8String:"), s) }
func release(o objc.ID)         { o.Send(sel("release")) }
func menuItem(title, action, key string) objc.ID {
	return class("NSMenuItem").Send(sel("alloc")).Send(sel("initWithTitle:action:keyEquivalent:"),
		nsString(title), sel(action), nsString(key))
}

func loadFrameworks() bool {
	frameworksOnce.Do(func() {
		for _, fw := range []string{
			"/System/Library/Frameworks/AppKit.framework/AppKit",
			"/System/Library/Frameworks/WebKit.framework/WebKit",
		} {
			if _, err := purego.Dlopen(fw, purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
				return
			}
		}
		frameworksOK = class("WKWebView") != 0 && class("NSApplication") != 0
	})
	return frameworksOK
}

// blockLiteral is the start of every Objective-C block: the function to call
// sits after the isa pointer and two 32-bit fields.
type blockLiteral struct {
	isa      uintptr
	flags    int32
	reserved int32
	invoke   uintptr
}

// callBlock runs a completion handler WebKit handed us. These are the
// system's blocks, not ones purego made, so they are called through their own
// function pointer.
func callBlock(block objc.ID, args ...uintptr) {
	if block == 0 {
		return
	}
	lit := *(**blockLiteral)(unsafe.Pointer(&block))
	purego.SyscallN(lit.invoke, append([]uintptr{uintptr(block)}, args...)...)
}

// runAlert shows a native alert for the page's alert/confirm/prompt and
// reports whether the first button was chosen, with the text field's value
// for a prompt.
func runAlert(message objc.ID, buttons []string, field objc.ID) bool {
	if alertAnswer != nil {
		return alertAnswer(message, field)
	}
	alert := class("NSAlert").Send(sel("new"))
	defer release(alert)
	alert.Send(sel("setMessageText:"), message)
	for _, b := range buttons {
		alert.Send(sel("addButtonWithTitle:"), nsString(b))
	}
	if field != 0 {
		alert.Send(sel("setAccessoryView:"), field)
		alert.Send(sel("layout"))
		objc.Send[objc.ID](alert, sel("window")).Send(sel("setInitialFirstResponder:"), field)
	}
	return objc.Send[int](alert, sel("runModal")) == alertFirstButton
}

// alertAnswer, when set, answers alerts instead of showing them. Only the
// test sets it: a test cannot click a modal, and what it needs to prove is the
// part around the modal — that WebKit's questions reach Go and the answers get
// back into the page through WebKit's own completion handlers.
var alertAnswer func(message, field objc.ID) bool

// registerClasses defines the two small delegates the window needs.
//
// Without a UI delegate WebKit drops confirm() and prompt() on the floor —
// they return false and null with nothing shown — and the page uses both: to
// confirm Quit, removing an installer, and typing a path.
func registerClasses() {
	classesOnce.Do(func() {
		ui, err := objc.RegisterClass("DSKYUIDelegate", objc.GetClass("NSObject"),
			protocols("WKUIDelegate"), nil, []objc.MethodDef{
				{
					Cmd: sel("webView:runJavaScriptAlertPanelWithMessage:initiatedByFrame:completionHandler:"),
					Fn: func(self objc.ID, _ objc.SEL, _, message, _, handler objc.ID) {
						runAlert(message, []string{"OK"}, 0)
						callBlock(handler)
					},
				},
				{
					Cmd: sel("webView:runJavaScriptConfirmPanelWithMessage:initiatedByFrame:completionHandler:"),
					Fn: func(self objc.ID, _ objc.SEL, _, message, _, handler objc.ID) {
						ok := runAlert(message, []string{"OK", "Cancel"}, 0)
						var v uintptr
						if ok {
							v = 1
						}
						callBlock(handler, v)
					},
				},
				{
					Cmd: sel("webView:runJavaScriptTextInputPanelWithPrompt:defaultText:initiatedByFrame:completionHandler:"),
					Fn: func(self objc.ID, _ objc.SEL, _, prompt, defaultText, _, handler objc.ID) {
						field := class("NSTextField").Send(sel("alloc")).Send(sel("initWithFrame:"), nsRect{0, 0, 360, 24})
						defer release(field)
						if defaultText != 0 {
							field.Send(sel("setStringValue:"), defaultText)
						}
						var result objc.ID
						if runAlert(prompt, []string{"OK", "Cancel"}, field) {
							result = objc.Send[objc.ID](field, sel("stringValue"))
						}
						callBlock(handler, uintptr(result))
					},
				},
			})
		if err == nil {
			uiDelegate = objc.ID(ui).Send(sel("new"))
		}
		wd, err := objc.RegisterClass("DSKYWindowDelegate", objc.GetClass("NSObject"),
			protocols("NSWindowDelegate"), nil, []objc.MethodDef{
				{
					Cmd: sel("windowWillClose:"),
					Fn:  func(self objc.ID, _ objc.SEL, _ objc.ID) { windowClosed.Store(true) },
				},
			})
		if err == nil {
			winDelegate = objc.ID(wd).Send(sel("new"))
		}
	})
}

func protocols(names ...string) []*objc.Protocol {
	var out []*objc.Protocol
	for _, n := range names {
		if p := objc.GetProtocol(n); p != nil {
			out = append(out, p)
		}
	}
	return out
}

// mainMenu gives the app the menus every Mac app has. The Edit items are not
// decoration: WebKit's fields only cut, copy, paste and select-all through
// these selectors, so without them Cmd-V does nothing in the path field.
func mainMenu(window objc.ID, title string) objc.ID {
	bar := class("NSMenu").Send(sel("new"))

	appMenu := class("NSMenu").Send(sel("new"))
	quit := menuItem("Quit DSKY", "performClose:", "q")
	quit.Send(sel("setTarget:"), window)
	appMenu.Send(sel("addItem:"), quit)
	addSubmenu(bar, appMenu, title)

	edit := class("NSMenu").Send(sel("alloc")).Send(sel("initWithTitle:"), nsString("Edit"))
	for _, it := range [][3]string{
		{"Undo", "undo:", "z"}, {"Redo", "redo:", "Z"}, {"", "", ""},
		{"Cut", "cut:", "x"}, {"Copy", "copy:", "c"}, {"Paste", "paste:", "v"},
		{"Select All", "selectAll:", "a"},
	} {
		if it[0] == "" {
			edit.Send(sel("addItem:"), class("NSMenuItem").Send(sel("separatorItem")))
			continue
		}
		edit.Send(sel("addItem:"), menuItem(it[0], it[1], it[2]))
	}
	addSubmenu(bar, edit, "Edit")

	win := class("NSMenu").Send(sel("alloc")).Send(sel("initWithTitle:"), nsString("Window"))
	win.Send(sel("addItem:"), menuItem("Minimize", "performMiniaturize:", "m"))
	win.Send(sel("addItem:"), menuItem("Close", "performClose:", "w"))
	addSubmenu(bar, win, "Window")
	return bar
}

func addSubmenu(bar, menu objc.ID, title string) {
	item := class("NSMenuItem").Send(sel("new"))
	item.Send(sel("setTitle:"), nsString(title))
	item.Send(sel("setSubmenu:"), menu)
	bar.Send(sel("addItem:"), item)
}

func showWindow(url, title string) bool {
	if !loadFrameworks() {
		return false
	}
	registerClasses()
	windowClosed.Store(false)
	closeRequested.Store(false)

	// The menu bar's bold first item and the Dock tile come from the app
	// bundle, and the process is the dsky binary in ~/.local/bin even when
	// DSKY.app started it — so both would say "dsky" and show macOS's blank
	// program icon. The name is read from the main bundle's info dictionary
	// when the application object is created, so it goes in first; for a
	// program outside a bundle that dictionary is a mutable one AppKit made.
	info := class("NSBundle").Send(sel("mainBundle")).Send(sel("infoDictionary"))
	if info != 0 && objc.Send[bool](info, sel("respondsToSelector:"), sel("setObject:forKey:")) {
		info.Send(sel("setObject:forKey:"), nsString(title), nsString("CFBundleName"))
	}
	class("NSProcessInfo").Send(sel("processInfo")).Send(sel("setProcessName:"), nsString(title))

	app := class("NSApplication").Send(sel("sharedApplication"))
	// A regular app: a Dock icon and a menu bar while the window is open, even
	// when launched from DSKY.app, which hides its launcher from the Dock.
	app.Send(sel("setActivationPolicy:"), activationRegular)

	frame := nsRect{0, 0, 1280, 860}
	window := class("NSWindow").Send(sel("alloc")).Send(sel("initWithContentRect:styleMask:backing:defer:"),
		frame, uint(styleTitled|styleClosable|styleMiniaturizable|styleResizable), uint(backingBuffered), false)
	if window == 0 {
		return false
	}
	// Kept after closing, so the loop below can still ask it questions.
	window.Send(sel("setReleasedWhenClosed:"), false)
	window.Send(sel("setTitle:"), nsString(title))
	window.Send(sel("setDelegate:"), winDelegate)
	// Dark frame, and the page's own near-black under a transparent title bar,
	// so the frame and the header read as one surface.
	window.Send(sel("setAppearance:"),
		class("NSAppearance").Send(sel("appearanceNamed:"), nsString("NSAppearanceNameDarkAqua")))
	window.Send(sel("setBackgroundColor:"),
		class("NSColor").Send(sel("colorWithSRGBRed:green:blue:alpha:"), 5.0/255, 5.0/255, 15.0/255, 1.0))
	window.Send(sel("setTitlebarAppearsTransparent:"), true)

	config := class("WKWebViewConfiguration").Send(sel("new"))
	web := class("WKWebView").Send(sel("alloc")).Send(sel("initWithFrame:configuration:"), frame, config)
	release(config)
	if web == 0 {
		release(window)
		return false
	}
	web.Send(sel("setAutoresizingMask:"), uint(autoresizeWidthSize|autoresizeHeight))
	web.Send(sel("setUIDelegate:"), uiDelegate)
	// No white flash before the page paints.
	web.Send(sel("setValue:forKey:"), class("NSNumber").Send(sel("numberWithBool:"), false), nsString("drawsBackground"))
	window.Send(sel("setContentView:"), web)

	app.Send(sel("setMainMenu:"), mainMenu(window, title))
	app.Send(sel("finishLaunching"))
	// After finishLaunching, which sets the Dock icon from the bundle and would
	// replace this.
	if icon := webui.AppIcon(); len(icon) > 0 {
		data := class("NSData").Send(sel("dataWithBytes:length:"), unsafe.Pointer(&icon[0]), uint(len(icon)))
		if img := class("NSImage").Send(sel("alloc")).Send(sel("initWithData:"), data); img != 0 {
			app.Send(sel("setApplicationIconImage:"), img)
		}
		runtime.KeepAlive(icon)
	}

	window.Send(sel("center"))
	// Restores the size and place it had last time, and saves this one's.
	window.Send(sel("setFrameAutosaveName:"), nsString("DSKY"))

	request := class("NSURLRequest").Send(sel("requestWithURL:"),
		class("NSURL").Send(sel("URLWithString:"), nsString(url)))
	web.Send(sel("loadRequest:"), request)

	window.Send(sel("makeKeyAndOrderFront:"), objc.ID(0))
	app.Send(sel("activateIgnoringOtherApps:"), true)

	setWindowCloser(func() { closeRequested.Store(true) })
	defer setWindowCloser(nil)

	// The event loop, run here rather than with [NSApp run] so that closing
	// the window returns to the caller instead of ending the process, and so a
	// close asked for from another goroutine is a flag rather than a call into
	// AppKit from the wrong thread. Waiting for events wakes at least five
	// times a second to look at those flags.
	mode := nsString("kCFRunLoopDefaultMode")
	anyEvent := ^uint64(0)
	for !windowClosed.Load() {
		pool := class("NSAutoreleasePool").Send(sel("new"))
		until := class("NSDate").Send(sel("dateWithTimeIntervalSinceNow:"), 0.2)
		if ev := app.Send(sel("nextEventMatchingMask:untilDate:inMode:dequeue:"), anyEvent, until, mode, true); ev != 0 {
			app.Send(sel("sendEvent:"), ev)
		}
		app.Send(sel("updateWindows"))
		if closeRequested.Swap(false) {
			window.Send(sel("close"))
		}
		pool.Send(sel("drain"))
	}
	window.Send(sel("orderOut:"), objc.ID(0))
	return true
}

// focusWindow: a second launch while DSKY is running starts a second process,
// which opens its own window onto the same portal.
func focusWindow(title string) bool { return false }

// upgradeLauncher is a Linux migration; macOS never had that launcher.
func upgradeLauncher() bool { return false }
