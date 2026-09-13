# Claiming the name: DSKY

Checked **2026-09-13**. Availability is a snapshot, not a hold — re-check
anything still unclaimed a week from now.

Everything here is done by hand. Account creation, payment and 2FA enrolment
are not things to automate, and none of it was done for you.

## What is free, and what is gone

| Target | Status |
|---|---|
| npm `dsky` | **free** |
| PyPI `dsky` | **free** |
| crates.io `dsky` | **free** |
| dsky.sh | **free** |
| GitHub `dsky` | **taken** — a User account, 0 repos, created and abandoned in December 2020 |
| dsky.com | taken — registered 2000-11-01 |
| dsky.net | taken — registered 2002-05-09 |
| dsky.dev · dsky.org · dsky.io · dsky.app | taken |

Two things follow from that table.

**The GitHub handle is gone and is not coming back.** It is held by a dormant
account with nothing in it, which is the most annoying kind of unavailable —
GitHub does not release names merely for being unused. This does not block
anything: the repo lives at `uplinkresearch/dsky`, inside the org you already
own, exactly as decided when Tailboard's own name turned out to be taken too.

**The domain situation is worse than the last name's.** `dsky.com` has been held
for twenty-five years, so it is somebody's, not a squatter's. Only `.sh` is
available. If a memorable domain matters, that is an argument about the name
itself and belongs before the registry grabs below, not after.

## Order of operations

### 1. Rename the repo — first, and free

`uplinkresearch/bootwright` → Settings → *Repository name* → `dsky`.

GitHub leaves a redirect, so the old path keeps resolving. Then locally:

    git remote set-url origin https://github.com/uplinkresearch/dsky.git

This is the time-sensitive item now, not because anybody is racing you for it,
but because everything else points at it. `internal/selfupdate` already reads
`uplinkresearch/dsky`, and installed copies keep updating through the redirect —
but do not create anything at the old path afterwards, or installed copies will
be asking a stranger's repository what version to become.

### 2. Registry accounts and 2FA

Prerequisites for step 3, and none of them reserve the name by themselves.

| Registry | Sign up | 2FA |
|---|---|---|
| npm | <https://www.npmjs.com/signup> | <https://docs.npmjs.com/configuring-two-factor-authentication> |
| PyPI | <https://pypi.org/account/register/> | <https://pypi.org/help/#twofa> — required to upload |
| crates.io | <https://crates.io> (sign in with GitHub) | inherits your GitHub 2FA |

PyPI wants an API token rather than a password:
<https://pypi.org/manage/account/token/>.

### 3. Publish the three placeholders

Scaffolded in [`reservations/`](reservations/) and not published. Commands are
in [reservations/README.md](reservations/README.md). Do this **after** the repo
rename, so the listings do not link to a path that has moved.

### 4. dsky.sh, if you want it

The only one available. <https://porkbun.com> or
<https://www.cloudflare.com/products/registrar/> (at cost, needs an account
already). Privacy on, auto-renew on.

Worth deciding whether a `.sh` alone is the domain story or whether the missing
`.com` changes your mind about the name. That question is cheaper to answer now
than after three registries carry it.

## Before publishing anything

**A placeholder buys time, not title.** npm's
[dispute policy](https://docs.npmjs.com/policies/disputes) and
[PEP 541](https://peps.python.org/pep-0541/) both exist to take names off people
sitting on them unused. Neither is automatic and both weigh whether the project
is real. A 0.0.1 stub that becomes a working package within weeks is fine; one
that sits for a year is the case those policies were written for.

crates.io has no equivalent process. That publish is effectively permanent.

**Not checked:** a USPTO search (<https://tmsearch.uspto.gov>). DSKY is a real
historical acronym for a NASA-era instrument rather than a coined word, which
makes it likelier than "Bootwright" was to collide with something — worth your
own look before it goes on anything commercial.

## Your own machine, after installing a DSKY build

The library moves itself: `%LOCALAPPDATA%\bootwright` is renamed to `…\dsky` on
first run, instantly however many gigabytes are in it.

Config does not, because moving it would break any older build still installed.
Once you are on DSKY:

    move "%APPDATA%\bootwright" "%APPDATA%\dsky"

That directory holds the catalog signing key, which cannot be regenerated.

## Checklist

- [ ] Repo renamed to `uplinkresearch/dsky`
- [ ] `git remote set-url origin` updated locally
- [ ] Nothing re-created at the old repo path
- [ ] npm account + 2FA
- [ ] PyPI account + 2FA + API token
- [ ] crates.io signed in via GitHub
- [ ] `npm publish --access public`
- [ ] `python -m build && twine upload dist/*`
- [ ] `cargo publish`
- [ ] dsky.sh registered, or a decision made about the domain story
- [ ] `%APPDATA%\bootwright` moved to `%APPDATA%\dsky`
- [ ] Trademark search, if the name is going on anything commercial
- [ ] Re-check availability if more than a week has passed since 2026-09-13
