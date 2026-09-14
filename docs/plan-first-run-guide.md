# Plan: a first-run guide

A clickable guide that explains how everything works the first time someone
opens DSKY. Built in v0.7.5; this is the plan it was built from. In v0.7.6 it
became one short guide for the start screen plus one for each screen, each shown
the first time that screen opens, because a single tour only explained installing.

**Decided (2026-09-13):** no themed names. An Apollo-era naming scheme was
proposed alongside this and dropped in favour of keeping every label as plain as
possible; the guide uses the app's existing names.

---

## The guide

### What it is

A short, clickable tour that runs the first time DSKY opens. Each step dims the
page, highlights one real part of the screen, and says in two or three sentences
what it is and when you would use it. Next, Back, Skip. Five or six steps, about
a minute.

It is orientation, not documentation. DSKY must still be usable by someone who
skips it: the labels say what things are, and the guide shows where they are and
how they connect.

### Steps

Using the app's own names, so nothing in the guide is a word the screen doesn't
use.

1. **The start screen.** "Three jobs start here: install an operating system,
   copy a drive, or look at a disk. Pick the one you came to do."
2. **Install an operating system.** Opens that screen and highlights the OS
   picker. "Choose an operating system and its options. You don't need a USB
   stick yet — you can download it or set up the image now and write it to a
   stick later."
3. **Recipes.** Highlights the list. "Save the options you use often as a
   recipe — one per customer or type of machine — and reuse them."
4. **Built images.** "Images you've set up wait here. Write one to as many
   sticks as you like without building it again."
5. **Activity.** Highlights the strip under the header, showing an example row.
   "Anything running shows here, on every screen, until you dismiss it."
6. **Before anything is erased.** Shows an example disk card. "DSKY describes the
   disk the way you'd recognise it — model, size, what's on it — and asks you to
   type its size. It never writes to the disk this computer is running from."

Done: "You can open this guide again from the ? in the header."

### Behaviour

- **Shown once**, on the first launch after installing. Whether it has been seen
  is stored in DSKY's settings file (`config.json`), not in browser storage:
  that belongs to whichever engine draws the window (WebView2, WebKit, or a
  Chromium profile), differs by platform, and can be cleared on its own.
- **Always re-openable** from a `?` button in the header.
- **Never interrupts work.** It does not start while something is running or a
  dialog is open, and closing it at any step is final for that run.
- **Nothing on screen changes for real.** Steps may open a screen to point at it,
  but the guide never starts a download, saves anything, or opens the erase
  confirmation. Example rows (step 5, 6) are drawn by the guide, clearly marked
  as examples, and removed when it ends.
- **Keyboard and accessibility**: Enter/→ next, ← back, Esc closes; focus stays
  in the guide while it is open; honours reduced-motion (no animated spotlight).
- **Works offline and inside the app window** on all three platforms — it is part
  of the embedded page, no network, no extra files.
- **Fits a small screen**: the explanation box moves to stay on screen, and at
  narrow widths becomes a card at the bottom.

### How it would be built

- A small tour module in `index.html`: a list of steps (target element, text,
  which screen to show), an overlay with a cut-out around the target, and a
  positioned card.
- `GET/POST /api/settings` for a `guide_seen` flag in `appconfig`.
- The `?` header button.
- Tests:
  - Go: the settings flag round-trips.
  - Headless Chromium: first load shows step 1; Next/Back/Esc/keys work; the
    guide never calls a mutating API (fail on any POST other than the settings
    one during the tour); it doesn't show on the second load; `?` reopens it.
  - Screenshots of each step checked by eye, desktop and phone width.
  - The macOS window workflow captures one step, to see it in the native window.

Size: two days, including the step writing and checking it on all three
platforms.

---

## Order

1. Build the guide against the current names.
2. Check it on all three platforms, including the macOS window workflow.
3. Release.

## Already done alongside this plan

**Progress on every screen.** Starting an update from the start screen used to
show no progress anywhere: Activity was missing from that screen and sat at the
bottom of the others. It is now one strip pinned under the header on every
screen, hidden when empty, with dismiss and "Clear finished". On main as
`ec74bf1`, not yet released. The guide's fifth step points at it.
