# Name reservations

Placeholder packages for the name **Bootwright**, on the three registries where
a name is first-come and cannot be recovered once somebody else takes it.

Nothing here is published. Nothing here is part of the build — these are not Go
packages and `go build ./...` does not see them.

Checked available on 2026-09-13: npm, PyPI, crates.io, the GitHub org handle,
and bootwright.{com,org,dev,io,sh,app}. See [CLAIM.md](../CLAIM.md) for what has
to be done by hand, and in what order.

| Directory | Registry | Name |
|---|---|---|
| `npm/` | npmjs.com | `bootwright` |
| `pypi/` | pypi.org | `bootwright` |
| `crate/` | crates.io | `bootwright` |

## Publish commands

Do not run these until the GitHub org exists — every package points at
`github.com/uplinkresearch/bootwright`, and publishing first means the registry
listing links to a 404 on day one.

npm:

    cd reservations/npm
    npm publish --access public

PyPI (build first, then upload the artefacts). The manifest uses PEP 639
licence metadata, so it needs reasonably current tooling — `hatchling` 1.27 or
newer, which `build` will fetch into its isolated environment on its own:

    cd reservations/pypi
    python -m pip install --upgrade build twine
    python -m build
    python -m twine upload dist/*

crates.io:

    cd reservations/crate
    cargo publish

## A caveat worth reading before publishing

npm and PyPI both have policies against reserving names you are not using —
[npm's dispute policy](https://docs.npmjs.com/policies/disputes) and
[PEP 541](https://peps.python.org/pep-0541/). Neither is automatic, both need a
human to file a claim, and both weigh "is this actually being worked on". A
placeholder that sits at 0.0.1 forever is the case they exist to undo; a
placeholder that becomes a real package in a few weeks is not.

So the reservation buys time, not title. Replace these with something that does
something as soon as there is something to ship.

crates.io has no equivalent reclamation process, so that one is effectively
permanent once published.
