# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

`dockprox` — Go CLI: "Inverse HTTP(S) proxy with SOCKS5 support". Local proxy that dials directly by default and routes only rule-matched hosts through named upstreams (SOCKS5 / HTTP CONNECT). Distributed via Homebrew, Docker (
multi-arch), `mise`, `go install`, and release archives. Entry point: `cmd/dockprox/dockprox.go`. Internal-only packages
under `internal/`; public reusable code under `pkg/`. Docs site under `docs/` (Bun-based).

## Toolchain

Pinned via `.mise.toml` — run `mise install` (or `make .mise`) to provision:

- Go 1.26+ (see `.golangci.yaml` `run.go`)
- `bun` 1.3.13 (docs site)
- `lefthook` 2.1.6 (git hooks)
- `golangci-lint` 2.12.1

All Go builds use build tag `safe` (`-tags=safe`). Tests honor `GO_TEST_TAGS` env var.

## Common commands

| Task                          | Command                                                   |
|-------------------------------|-----------------------------------------------------------|
| Install toolchain             | `make .mise`                                              |
| Install git hooks             | `make .lefthook`                                          |
| Build binary → `bin/dockprox` | `make build`                                              |
| Build with debug symbols      | `make build.debug`                                        |
| `go install`                  | `make install`                                            |
| Run tests (+ coverage)        | `make test`                                               |
| Run tests with `-race`        | `make test.race`                                          |
| Run single test               | `go test -tags=safe -run TestName ./path/to/pkg`          |
| Lint                          | `make lint`                                               |
| Auto-fix lint                 | `golangci-lint fmt` then `golangci-lint run --fix`        |
| `go generate ./...`           | `make generate`                                           |
| `go mod tidy`                 | `make tidy`                                               |
| Show outdated direct deps     | `make outdated`                                           |
| Upgrade direct deps           | `make upgrade`                                            |
| Vulnerability scan            | `make audit` (govulncheck)                                |
| Full preflight                | `make check` (tidy + generate + lint + test.race + audit) |
| Run docs site dev             | `make docs`                                               |
| Browse Go docs                | `make godocs`                                             |

`make help` prints all targets.

## Conventions enforced by hooks

Lefthook (`.lefthook.yaml`) blocks commits on violations. Match these locally before pushing:

- **Branch name** must start with `feature/` or `fix/`.
- **Commit message** must follow Conventional Commits: `type(scope?): subject` (≤50 chars). Allowed types:
  `build chore ci docs feat fix perf refactor style test sec wip revert`. Breaking changes use `!`.
- **Pre-commit** runs `golangci-lint fmt` (auto-stages fixes) and `golangci-lint run --new --fast-only` on staged
  `*.go`.
- **Tag push** requires SemVer `vMAJOR.MINOR.PATCH[-prerelease][+build]`.
- `post-checkout` runs `mise install` automatically.

## Linter setup

`.golangci.yaml` (v2) uses `linters.default: all` with a curated disable list. Notable:

- Build tag `safe` is required for linting (`run.build-tags`).
- `modules-download-mode: readonly` — do not let lint trigger a module download/edit.
- `staticcheck` disables `ST1003` and `SA1019`.
- `gomoddirectives` allows local `replace` and a `replace` for `github.com/iancoleman/strcase`.
- Test files exempt from `forbidigo`, `gosec`.

## Release / version injection

`.goreleaser.yaml` injects three vars into `github.com/foomo/dockprox/internal/cli` via `-ldflags`: `version`,
`commitHash`, `buildTimestamp`. Preserve these symbols when refactoring the CLI package. GoReleaser builds
`darwin/linux` × `amd64/arm64`, plus multi-arch Docker images (`foomo/dockprox`) and a Homebrew tap (
`foomo/homebrew-tap`).

## Plugin rules

Respect rules and skills from any enabled plugin (e.g. `superpowers@foomo`, `caveman@foomo`, `context-mode`). When a plugin provides a workflow, skill, or convention applicable to the task, follow it over default behavior. Ignore plugins not enabled in the current session.
