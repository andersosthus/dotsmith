# Dotsmith — Project Instructions

Dotsmith is a dotfile management tool that combines GNU Stow-style symlink management with a
Kustomize-inspired overlay system and subfile-based composition.

## Go

- **Version:** 1.26
- **Module path:** `github.com/andersosthus/dotsmith`
- **Entry point:** `cmd/dotsmith/main.go`

## Project Structure

```
cmd/dotsmith/        CLI entry point (main.go only — wire cobra root, call os.Exit)
internal/
  compiler/          subfile discovery, override resolution, concatenation
  linker/            symlink creation and management
  state/             state file read/write
  config/            config loading and merging (Viper)
  encrypt/           age encrypt/decrypt wrappers
  comment/           comment-header insertion per extension
  identity/          OS/hostname/username auto-detection
```

Keep packages small and focused. No `pkg/` or `util/` grab-bags.

## Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/spf13/cobra` | Command structure, flags, shell completions |
| `github.com/spf13/viper` | YAML config with env/flag override |
| `filippo.io/age` | Age encryption/decryption |

No other runtime dependencies unless strictly necessary. Each new dependency requires a justification
comment in `go.mod`.

## Code Style

Follow standard idiomatic Go conventions plus:

- Prefer `for` loops with mutable accumulators over long iterator chains
- Use `errors.New` / `fmt.Errorf` with `%w` for wrapping; never discard errors
- Use `let...else`-style early returns with `if err != nil { return ..., err }` — keep happy path
  unindented
- Newtypes for domain concepts where confusion is likely (e.g., `type OverrideLayer string`)
- Enums as `const` blocks with a named string type for state machines (override precedence, etc.)
- `log/slog` for structured logging; never `fmt.Println` in library code
- Use `context.Context` as the first param for any function that does I/O
- Google-style docstrings on all exported symbols

### Error messages

Include what operation failed, which file/path, and a suggested fix where applicable:

```go
// Good
fmt.Errorf("compile %s: duplicate subfile number %d — rename one to resolve", targetFile, n)

// Bad
fmt.Errorf("duplicate subfile")
```

## Testing

**Target: 100% coverage** (enforced in CI with `go test -coverprofile` threshold check).

### Rules

- All filesystem tests use `t.TempDir()` — never hardcoded or `/tmp` paths
- Table-driven tests for any function with multiple input/output combinations
- Test behaviour, not implementation; refactoring should not break tests
- Test every error path the code handles
- Only mock the filesystem or external I/O when the real thing is impractical
- Integration tests live in `internal/<pkg>/<pkg>_integration_test.go` with build tag
  `//go:build integration`

### Key areas to test (from PRD)

- Natural sort order: mixed zero-padding (`1`, `02`, `10`, `099`) must sort correctly
- Override resolution: every combination of base + override layers, replacement, addition, ignore
- Comment header insertion: correct style per extension, provenance labels, no header for
  unrecognised extensions or regular (non-subfile) files
- Age encrypt/decrypt round-trips: identity-file mode
- Compile → link → status → clean end-to-end using `t.TempDir()`
- File permissions: compile dir `0700`, decrypted files `0600`, regular compiled files `0644`,
  state file `0600`
- Idempotency: run compile and link twice, assert identical results and no unnecessary writes
- Dry-run: assert zero filesystem side effects

## Linting

Use `golangci-lint` with the config at `.golangci.yml`. Minimum enabled linters:

```yaml
linters:
  enable:
    - errcheck
    - gosimple
    - govet
    - ineffassign
    - staticcheck
    - unused
    - gocyclo       # max complexity 8
    - funlen        # max 100 lines
    - wrapcheck     # errors from external packages must be wrapped
    - exhaustive    # switch on enum must be exhaustive
    - noctx         # http calls must use context
    - bodyclose
    - revive
```

Zero warnings policy: fix every lint warning before committing.

## Build and Release

- **Goreleaser** manages cross-platform builds: `linux/amd64`, `linux/arm64`, `darwin/amd64`,
  `darwin/arm64`
- Config at `.goreleaser.yml`
- Goreleaser produces binaries, tarballs, checksums, and shell completion scripts
- Shell completions are also generated at runtime via `dotsmith shell <bash|zsh|fish>`

## Workflow

```bash
go build ./...
go test ./...
go test -tags integration ./...
golangci-lint run
```

Run all three before committing. Fix every error and warning — a clean output is the baseline.

Use `prek` for pre-commit hooks (`prek install`).
