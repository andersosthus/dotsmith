# Reject managed dotfiles that compile to the reserved `.dotsmith.state` name

A managed dotfile whose compiled target equals `.dotsmith.state` (the state file dotsmith keeps inside the compile directory) is a hard error at discover time, in every layer. This is recorded because the guard is deliberately narrow — root-level target only, `.ignore` markers exempt — and a future contributor "tightening" or "loosening" it for consistency would either reintroduce a persistent self-DoS or break ignore semantics.

The state file lives at `<compiledir>/.dotsmith.state`. Without the guard, a dotfile named `.dotsmith.state` (or `.dotsmith.state.age`, or a `.subfile-NNN.state` set resolving to that name) would compile straight over the real state file. Because `compile` writes it and `link`/`status`/`clean` read it, a single bad compile would corrupt dotsmith's own bookkeeping and stay broken on every subsequent run — a persistent denial of service, not a transient one. The reserved basename is exported once as `state.FileName` so the producer (compile) and the consumer that skips it (`compiledFileRefs`) cannot drift apart.

The check sits in `applyFile` (in `internal/compiler/discover.go`), on both the subfile and the regular-file branch, so every command that routes through `Discover` — apply/link/status/clean — is covered uniformly without per-command guards.

## Considered Options

- **Guard the compiled target in `Discover`, root-level only, ignore markers exempt (chosen)** — matches `target == state.FileName` exactly. The state file only ever exists at the compile root, and `compiledFileRefs` likewise skips only `rel == state.FileName`, so producer and consumer agree on precisely one path. `.ignore` markers are exempt because they only *remove* entries; one can never write output and so can never clobber the state file.
- **Match any path basename `.dotsmith.state`, at any depth** — rejected: a `.dotsmith.state` nested under a subdirectory compiles to `<dir>/.dotsmith.state`, which cannot collide with the root state file. Rejecting it would forbid a legitimate managed dotfile for no safety gain and diverge from what `compiledFileRefs` skips.
- **Special-case the write in compile (skip or rename the offending output)** — rejected: it would silently drop a file the user asked to manage. Failing loudly at discover with a rename suggestion is the safer, more honest behavior.

## Consequences

- The error names the offending source path, its layer, and the reserved target, and tells the user to rename the file to manage it as a dotfile.
- `.ignore` markers are intentionally not checked. Extending the guard to them would be harmless today but pointless, and the exemption is documented in code so it is not "fixed" later.
- The guard is keyed on `state.FileName`. If the state file is ever renamed, the reserved name, the compile-time rejection, and the `compiledFileRefs` skip all move together through that single constant — they must not be hardcoded apart again.
