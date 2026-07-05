# Contributing

Thanks for your interest in improving Contract Billing Core. This document covers
the local development workflow.

## Prerequisites

- Go 1.25 or later
- `make`
- `golangci-lint` (for `make lint`)

## Common commands

```bash
make check              # build + lint + test (run before every commit)
make test               # library module tests (go test ./... -race -count=1)
make test-e2e           # end-to-end tests (separate module — see below)
make test-all           # library tests + e2e
make lint               # vet + gofmt check + golangci-lint
make cover              # coverage report
```

## Module layout: the library vs. the e2e suite

This repository is a **Go workspace** (`go.work`) containing two modules:

| Module | Path | Purpose |
|--------|------|---------|
| `github.com/contract-to-cash/core` | `.` (repo root) | The library. Its only direct dependency is `github.com/oklog/ulid/v2`. |
| `github.com/contract-to-cash/core/tests/e2e` | `tests/e2e/` | The end-to-end suite. Depends on [`runn`](https://github.com/k1LoW/runn) and pulls its large transitive graph (GCP/AWS SDKs, a headless-browser driver, SQL drivers). |

The e2e suite lives in its own module so that `runn` and its heavy transitive
dependencies **stay out of the library's `go.mod`**. Downstream users doing
`go get github.com/contract-to-cash/core/...` only resolve `ulid/v2`, not the
E2E-only dependency graph. The `tests/e2e/go.mod` uses a `replace` directive to
point back at the parent module, and the root `go.work` ties the two together
for local development.

Because `tests/e2e/` is a separate module, it is **not** matched by `./...` from
the repo root — `make test` / `make check` do not run it. Run it explicitly:

```bash
make test-e2e
# or directly:
cd tests/e2e && go test ./... -race -count=1 -v
```

`tests/integration/` and `tests/lintcheck/` remain in the library module (they
only use the in-tree in-memory repositories and have no heavy dependencies).

### Working with dependencies

- Changing library dependencies: edit/tidy at the repo root. Use `GOWORK=off go mod tidy`
  (or just `go mod tidy` — the library graph is what matters) and confirm the root
  `go.mod` still lists only `ulid/v2` as a direct dependency.
- Changing e2e dependencies: `cd tests/e2e && go mod tidy`.
- CI runs the library module with `GOWORK=off` so its job never resolves the e2e
  dependency graph; the e2e suite runs as a separate job.

## Before you commit

Run `make check` and make sure it passes. Keep the library `go.mod` minimal — if
a change adds a non-`ulid` direct dependency to the root module, that almost
certainly belongs in `tests/e2e/` (or is a design discussion worth an issue).
