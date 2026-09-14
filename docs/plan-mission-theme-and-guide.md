# Plan: Apollo-era names, and a first-run guide

Two pieces of work, planned together because the guide has to use whichever
names the first one settles on.

1. Name the things in the app with an old NASA / Apollo theme.
2. A clickable guide that explains how everything works the first time someone
   opens DSKY.

Nothing here is built yet. The decisions marked **Decide** are Dusty's.

---

## 1. Apollo-era names

### Why it fits

DSKY is already named after the Apollo Guidance Computer's keyboard and display.
The theme is not decoration bolted on; the product is named for it, and the
vocabulary of that era is unusually good at the things this tool does. A rocket
stage and a staged image, an engine burn and burning a stick, a flight plan and a
recipe.

### Rules, so the theme never costs anyone a mistake

- **Every themed name keeps its plain meaning beside it**, on screen, not in a
  tooltip or a glossary: `LAUNCH — install an operating system`. A technician who
  has never heard of Apollo must still be able to use DSKY without the guide.
- **The dangerous screens stay plain.** Erase confirmations, the disk
  identification card, the system-disk refusal and error messages keep ordinary
  words. `docs/design.md` already says the destructive confirmation's only job is
  to be read; "Erase everything on this disk" beats any theme.
- **Machine-readable names do not change.** The CLI commands, API paths, recipe
  YAML, job kinds and file names stay as they are. Renaming those would break
  scripts and workspaces people already have for a change that is only about how
  the app reads. This is only the portal's labels.
- **No effects the design plan rules out**: no scanlines, CRT curvature or
  sound. The theme is in the words, plus at most one small indicator lamp.
- **One vocabulary, used consistently.** A name that appears on a button appears
  identically in the guide, the Activity strip, and the README.

### Proposed names

First column is what the app says today; the proposal is the themed name with
its plain subtitle. Alternatives are there to pick from.

| Today | Proposed | Plain subtitle shown with it | Alternatives |
|---|---|---|---|
| Start screen | **Mission Control** | — | Flight Deck |
| Install an operating system | **Launch** | Install an operating system | Liftoff |
| Copy a drive | **Rendezvous** | Copy a drive onto one or many | Relay, Docking |
| Disk utility | **Systems Check** | See what is on a disk and fix it | Diagnostics |
| Workspaces and sources | **Ground Systems** | Workspaces and downloads | Mission Archive |
| Recipe | **Flight Plan** | Saved install settings | Checklist |
| Save as recipe… | **Save flight plan…** | | |
| Built images (artifacts) | **Staged images** | Built and ready to write | Flight-ready |
| Set up image | **Stage** | Build the image to write later | Prepare |
| Download only | **Fuel** | Download the OS, nothing else | Load, Acquire |
| Write to stick… / Erase & install | **Burn…** | Write to a USB stick | Commit |
| Activity | **Telemetry** | What is running | Comp Acty |
| Verify phase | **Verify** | Reading back every byte | (unchanged) |
| Finished and verified | **Splashdown** | Written and verified, byte for byte | |
| Devices | **Hardware on the bus** | Disks this computer can see | Vehicles |
| Your installers | **Payload** | Your own .msi and .exe files | |
| Sources (manifests) | **Manifest** | What each OS downloads from | |
| Workspace | **Mission** | A folder of flight plans for one organisation | |
| Check for updates / update banner | **Uplink** | Update DSKY | |
| Quit | Quit | (stays plain) | |

Notes on a few:

- **Burn** is the best fit in the list: an engine burn and burning install media
  mean the same thing to two different audiences. It is also the most
  destructive action, so the button reads `Burn…` but the confirmation it opens
  says "This erases everything on the stick" in plain words, per the rules above.
- **Uplink** is both what Apollo called sending data up to the spacecraft and the
  company's name.
- **Payload** is already the word recipes use for bundled installers
  (`windows.payload`), so this makes the UI agree with the files.
- **Telemetry, with a COMP ACTY lamp.** The real DSKY had a COMP ACTY light that
  lit while the computer was busy. A small lamp by the logo that is lit while
  anything is running would tell someone at a glance, from across a bench, that
  DSKY is working. This is the one visual addition proposed.
- **OPR ERR** was the DSKY lamp for an operator keying something invalid. It is
  tempting for the typed-size mismatch, but that is a destructive screen and
  stays plain. Not proposed.

**Decide:**

1. Which names to keep. The table is a proposal, not a list to adopt whole;
   several (Rendezvous, Fuel) are the weakest and have plainer alternatives.
2. Whether the theme reaches the README and website copy, or stays in the app.
3. Whether to add the COMP ACTY lamp.

### How it would be built

- Every label lives in `internal/webui/index.html`. Collect them into one table
  of strings at the top of the script, so the vocabulary is defined once and the
  guide reads from the same place. No runtime translation system; a plain object.
- Update the empty-state and help text that mentions old names ("Save as recipe",
  "Built images").
- Update the README's portal descriptions if decided.
- Tests: the headless-Chromium walkthrough already used for the Install screen
  checks the labels a person clicks; extend it to fail on any leftover old name
  in visible text (a short denylist), so a missed label is caught rather than
  shipped.

Size: a day, most of it reading every string in context.

---

## 2. First-run guide

### What it is

A short, clickable tour that runs the first time DSKY opens. Each step dims the
page, highlights one real part of the screen, and says in two or three sentences
what it is and when you would use it. Next, Back, Skip. Five or six steps, about
a minute.

It is orientation, not documentation. DSKY must still be usable by someone who
skips it, which is what rule one of the naming section is for: the labels carry
their meaning, the guide shows where things are and how they connect.

### Steps

Written with the proposed names; they follow whatever is decided.

1. **Mission Control.** "Three jobs start here: Launch installs an operating
   system, Rendezvous copies a drive, Systems Check looks at a disk. Pick the job
   you came to do."
2. **Launch.** Opens the Launch screen and highlights the OS picker. "Choose an
   operating system and its options. You don't need a USB stick yet — you can
   fuel (download) or stage (build) now and burn it to a stick later."
3. **Flight plans.** Highlights the list. "Save the options you use often as a
   flight plan — one per customer or type of machine — and reuse them."
4. **Staged images.** "Built images wait here. Burn one to as many sticks as you
   like without building it again."
5. **Telemetry.** Highlights the Activity strip, showing an example row. "Anything
   running shows here, on every screen, until you dismiss it."
6. **Before anything is erased.** Shows an example disk card. "DSKY describes the
   disk the way you'd recognise it — model, size, what's on it — and asks you to
   type its size. It never writes to the disk this computer is running from."
   Plain words, deliberately.

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

1. **Decide the names** (the table above). Nothing else depends on anything
   more.
2. **Names**, in one change, with the leftover-label check.
3. **Guide**, written against the final names.
4. One release for both, so nobody sees a guide describing names they don't
   have.

## Already done alongside this plan

**Progress on every screen.** Starting an update from the start screen used to
show no progress anywhere: Activity was missing from that screen and sat at the
bottom of the others. It is now one strip pinned under the header on every
screen, hidden when empty, with dismiss and "Clear finished". On main as
`ec74bf1`, not yet released. When the names are decided, this is what becomes
Telemetry.
