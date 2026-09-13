# Claiming the name: Bootwright

Everything here has to be done by hand — account creation, payment, and 2FA
enrolment are not things to automate, and none of it was done for you.

Availability was checked on **2026-09-13**. All of it was free. None of it is
reserved until you do the steps below, and availability is a snapshot, not a
hold — re-check anything you have not claimed within a week or so.

## Do these first (today or tomorrow)

The order matters: the packages all point at `github.com/uplinkresearch/bootwright`,
so publishing before the org exists ships a listing whose links 404.

### 1. GitHub organisation — <https://github.com/organizations/plan>

Create the org **`uplinkresearch`**. Everything lives under it: Bootwright,
Tailboard, and whatever comes next.

- Plan: **Free**
- Organization name: `uplinkresearch`
- This organization belongs to: *My personal account*

Then **transfer** the existing repo rather than creating a fresh one:
`DustanBaker/uplink-composer` → Settings → Danger Zone → *Transfer ownership* →
`uplinkresearch`, then rename it to `bootwright`.

Transfer rather than recreate, for three reasons. The releases and their assets
come with it — the product page links to `releases/latest/download/uplink-setup-amd64.exe`
and that link dies on an empty repo. GitHub leaves a redirect behind, which is
what every installed copy follows to find updates. And the stars, issues and
history come too.

Afterwards:

    git remote set-url origin https://github.com/uplinkresearch/bootwright.git

Enable 2FA on your own account first, or the org will nag you about it:
<https://github.com/settings/security>

**Why a company org rather than a product org:** `tailboard` was already gone —
taken in February 2024 by an unrelated, empty org called TailboardUAS. Product
names are contested; a coined company name is not. One org also means one set of
members, secrets and settings no matter how many products follow.

Worth ten seconds while you are there: the `bootwright` org handle is currently
free. Claiming it costs nothing and stops the Tailboard situation happening
again. The code still lives in `uplinkresearch` either way.

### 2. Domains — pick a registrar and buy in one sitting

All six were unregistered. Buy at least `.com` and `.dev`; the rest are
cheap insurance against somebody landing on a squatter later.

| Domain | Roughly |
|---|---|
| bootwright.com | $10–15/yr |
| bootwright.dev | $12–15/yr |
| bootwright.org | $10–15/yr |
| bootwright.io | $35–60/yr |
| bootwright.sh | $30–50/yr |
| bootwright.app | $12–20/yr |

Registrars that do not mark up renewals: <https://www.cloudflare.com/products/registrar/>
(at-cost, but you must already have a Cloudflare account), or
<https://porkbun.com>, or <https://www.namecheap.com>.

Buy WHOIS privacy — it is free at all three. Turn on auto-renew. Registrar
lock too, which is on by default nearly everywhere.

**This is the genuinely time-sensitive one.** Domain availability is public and
continuously scraped; checking a domain can itself attract front-running from
some registrar search boxes. Use a registrar's cart directly rather than
shopping the name around several search boxes first.

### 3. Registry accounts + 2FA

None of these reserve the name by themselves — they are the prerequisite for
step 4, and 2FA is mandatory on all three for publishing now.

| Registry | Sign up | 2FA |
|---|---|---|
| npm | <https://www.npmjs.com/signup> | <https://docs.npmjs.com/configuring-two-factor-authentication> |
| PyPI | <https://pypi.org/account/register/> | <https://pypi.org/help/#twofa> — required to upload |
| crates.io | <https://crates.io> (sign in with GitHub) | inherits your GitHub 2FA |

PyPI also wants an API token rather than a password for uploads:
<https://pypi.org/manage/account/token/>. Same for npm if you publish from CI:
<https://docs.npmjs.com/creating-and-viewing-access-tokens>.

crates.io signs in with GitHub, so do step 1 before this.

### 4. Publish the three placeholders

Scaffolded and ready in [`reservations/`](reservations/), not published. The
exact commands are in [reservations/README.md](reservations/README.md).

Do this **after** the GitHub org exists.

## Worth knowing before you publish

**A placeholder is a delay, not a deed.** npm's
[dispute policy](https://docs.npmjs.com/policies/disputes) and
[PEP 541](https://peps.python.org/pep-0541/) both exist to transfer names away
from people sitting on them unused. Neither is automatic — a human has to file
and a human decides — and both weigh whether the project is real. A 0.0.1 stub
that becomes a working package within a few weeks is fine. One that sits there
for a year is the exact case those policies undo.

crates.io has no equivalent reclamation process, so that publish is effectively
permanent.

**Not checked, and worth your own look if the name matters commercially:** a
USPTO trademark search (<https://tmsearch.uspto.gov>). "Bootwright" is a coined
compound, which is a good position to be in, but I did not check it and a
software trademark is the one collision that is expensive to discover late.

## What the rename already changed

The code rename is done — module path, commands, product name, on-disk paths.
Two consequences that live outside this repo:

**The installer asset is renamed.** It builds as `bootwright-setup-amd64.exe`
now, not `uplink-setup-amd64.exe`. The product page on uplinkresearch.com links
to the old filename at `releases/latest/download/...`, so **that download link
breaks on the next release** until the site is updated. It is a different repo,
so nothing here can fix it.

**The update feed moved.** `internal/selfupdate/selfupdate.go` now reads:

    const Repo = "uplinkresearch/bootwright"

Every copy already installed — including your v0.4.1 — still polls the old
path. Transferring the repo leaves a GitHub redirect behind, and that redirect
covers the API, so existing installs should find the new releases through it.
Confirm that once rather than assuming it.

**Your local data.** The library migrates itself: `%LOCALAPPDATA%\uplink-composer`
is renamed to `...\bootwright` on first run, which is an instant directory
rename however many gigabytes are in it. Config and workspaces do not migrate —
move them by hand *after* installing the renamed build, because your current
v0.4.1 is still reading the old paths:

    mv "$APPDATA/uplink" "$APPDATA/bootwright"

That is the one holding the catalog signing key, which cannot be regenerated.

The thing not to do is **re-create a repo at `DustanBaker/uplink-composer`**
after moving away from it. A new repo at the old path replaces the redirect, and
every installed copy would then be asking a stranger's repository what version
it should upgrade itself to. Leave the old path empty.

## Collisions

Nothing is taken. But four GitHub repos already use the name, found on
2026-09-13, none with a single star between them:

| Repo | What it is | Why it matters |
|---|---|---|
| [crmarques/bootwright](https://github.com/crmarques/bootwright) | Provisioning fleets of OpenShift/OKD clusters. **Go**, Apache-2.0, actively pushed Aug 2026, has a docs site | Closest collision: same language, adjacent problem space (provisioning machines) |
| [EdenCompiler/bootwright](https://github.com/EdenCompiler/bootwright) | Common Lisp framework for bare-metal OS development | Adjacent by subject — it is literally about booting |
| [ThomasHoussin/Bootwright](https://github.com/ThomasHoussin/Bootwright) | SaaS boilerplate — a "bootstrap" pun | Unrelated, dormant since Mar 2026 |
| [gleson/Bootwright-Wysiwyg-Editor](https://github.com/gleson/Bootwright-Wysiwyg-Editor) | Bootstrap WYSIWYG editor | Unrelated |

None of them hold the org handle, any registry name, or any domain. The cost is
shared search results rather than a blocked name — but the first two are close
enough to your subject that somebody searching "bootwright" for an OS tool could
land on the wrong one. Your call whether that is worth anything.

## Checklist

- [ ] GitHub account 2FA on
- [ ] GitHub org `uplinkresearch` created — **do first**
- [ ] `DustanBaker/uplink-composer` transferred to `uplinkresearch`, renamed `bootwright`
- [ ] `git remote set-url origin` updated locally
- [ ] `bootwright` org handle claimed as defensive insurance (optional)
- [ ] bootwright.com registered, privacy + auto-renew on — **time-sensitive**
- [ ] bootwright.dev registered
- [ ] Other domains registered (.org/.io/.sh/.app) as desired
- [ ] npm account + 2FA
- [ ] PyPI account + 2FA + API token
- [ ] crates.io signed in via GitHub
- [ ] `npm publish --access public`
- [ ] `python -m build && twine upload dist/*`
- [ ] `cargo publish`
- [ ] Trademark search, if the name matters commercially
- [ ] Re-check availability if more than a week has passed since 2026-09-13
