# Re-assert the local-path invariant at the linker's deletion sinks

The linker deletes files in two places: `removeOrphans` (via `removeOrphanEntry`) prunes the symlink and compiled source for a state entry that no longer has a managed file, and `cleanSymlinks` removes every managed symlink and its compiled source on `clean`. Both build their delete targets as `filepath.Join(cfg.TargetDir, entry.Target)` / `filepath.Join(cfg.CompileDir, entry.Source)` and call `os.Remove`. The safety of those removes rested entirely on an implicit invariant — every `Source`/`Target` in `state.State` was pre-validated `filepath.IsLocal` by `state.Load` — which was neither enforced nor documented at the sink. We now route every join through a `safeJoin` helper (in `internal/linker/linker.go`) that refuses any non-local entry, returning an error and performing no deletion.

This is **defense-in-depth, not a vulnerability fix**. `state.Load` is today the only producer of these entries and already rejects non-local `Source`/`Target`, so the path-escape this guards against is not reachable through any current code path. The risk is a future producer of `state.State` — a migration, an import, or a new constructor — that populates entries without going through `Load` and silently reintroduces an out-of-tree `os.Remove` with no local signal at the place that does the deleting. We make the invariant explicit at the sink so it cannot be lost.

## Considered Options

- **Re-check containment at each deletion sink via a shared `safeJoin` (chosen)** — the guard lives at the point of use, so any feed into `state.State` (not just `state.Load`) is covered, and the check is colocated with the `os.Remove` it protects. A single helper keeps `removeOrphans`, `removeOrphanEntry`, and `cleanSymlinks` consistent and makes the invariant impossible to read past.
- **Document the invariant in a comment only** — rejected: a comment is not enforcement. The next contributor who adds a non-`Load` state producer would have to notice and honour a prose note; the whole point is to fail closed without relying on that.
- **Validate only in `state.Load`** — rejected: that is the current state of the world, and it leaves the linker trusting that every producer pre-validated. The invariant should hold at the sink regardless of how the entry arrived, mirroring the paired-guard reasoning in 0006.
- **Do nothing (rely on `state.Load`)** — rejected: the check is cheap, the failure mode is deleting arbitrary files outside `TargetDir`/`CompileDir`, and an implicit safety invariant on a deletion path is exactly the kind of thing that should be explicit.

## Consequences

- A state entry whose `Source` or `Target` is not `filepath.IsLocal` (absolute, or escaping via `..`) is refused at the deletion sink with an error naming the offending path; no `os.Remove` runs for that entry, so no out-of-tree file is touched.
- `safeJoin` and `state.Load`'s `IsLocal` check are independent on purpose and must stay paired. `state.Load` still fails fast at read time with the actionable message; `safeJoin` is the backstop for any entry that did not come through `Load`. Neither should be removed on the assumption the other suffices.
- For the only producer that exists today (`state.Load`), behaviour is unchanged — every entry is already local, so `safeJoin` always takes the join path.
