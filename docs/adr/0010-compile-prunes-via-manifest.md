# Compile prunes its output via a persisted manifest

When a source target is removed, its compiled artifact must disappear from the
compile directory. We make this **compile's** responsibility: `WriteCompiled`
now makes the compile directory reflect the current source by *pruning*
compiled files it no longer produces. Compile decides what to prune from a
**manifest** — the set of files (relative path → content hash) it recorded on
the previous run, persisted as a new `Compiled` field in the existing
`.dotsmith.state`. Compile deletes only files present in the prior manifest but
absent from the current result, so it never touches anything it did not create.

Pruning the compiled file can leave a *dangling symlink* in the target
directory. Removing that symlink stays the linker's job, not compile's: compile
never creates symlinks, so it should not delete them, and reaching into the
target directory would duplicate the linker's hardened deletion sink. `apply`
runs `link` immediately after `compile`, so the dangle never surfaces on the
common path; a bare `compile` reports the dangle and tells the user to run
`link`.

## Considered Options

- **Manifest-based prune owned by compile (chosen)** — compile owns its output
  directory end-to-end and is correct on its own, with no dependence on whether
  `link` runs. The manifest means compile deletes only its own prior output,
  matching the project's deletion-safety posture (ADRs 0005, 0006, 0008, 0009).
- **Mirror by walking the compile directory and deleting anything not in the
  current result** — rejected: a blunt `rm` of a whole directory minus a
  whitelist would silently delete stray files, leftover decrypted secrets, or
  editor swapfiles a user placed there. This runs directly against the
  delete-only-what-we-created stance.
- **Leave pruning to the linker's existing state-driven orphan removal** —
  rejected: it only fires when `link` builds its file list in-memory (the
  `apply` path). Standalone `compile` then `link` is broken — `link` rebuilds
  its list by walking the compile directory, so the stale file is still present
  and gets re-linked. It also lets the compile directory accumulate cruft
  whenever `compile` runs without `link`.
- **A separate manifest file** — rejected: a second reserved filename to guard
  against being emitted as a managed dotfile, a second containment-validated
  loader, a second set of perms. Folding `Compiled` into `.dotsmith.state`
  reuses all of that hardening.
- **Have compile remove the dangling symlink too** — rejected: compile never
  creates symlinks, so deleting them is asymmetric and surprising; it would
  require plumbing the target directory into the compile config and cloning the
  linker's `safeJoin` / `removeEmptyParents` / state bookkeeping — the most
  safety-sensitive code in the project — into a second place.

## Consequences

- Compile now reads and writes `.dotsmith.state`, which it did not touch before.
  In `apply` the state file is written twice in sequence (compile saves
  `Compiled`, then link saves `Symlinks`); each stage loads the whole `State`,
  mutates only its own field, and saves, so neither drops the other's. `Clean`'s
  `state.New()` must zero both fields.
- The manifest stores a content hash per compiled file even though pruning only
  needs the key set. The hash is recorded now so a future compile can detect a
  locally-edited compiled file and refuse to clobber or prune it. A linked
  file's hash is therefore stored twice — `Symlinks[x].ContentHash` (what was
  linked) and `Compiled[x].ContentHash` (what was compiled). These answer
  different questions and are intentionally not deduped.
- On first run after upgrade the `Compiled` field is absent, so nothing is
  pruned and the manifest is established from that run. Compiled files that went
  stale *before* this feature shipped are grandfathered in and never pruned —
  accepted, as the alternative (a one-time reconciling walk) reintroduces the
  blunt delete we rejected.
- Under `--dry-run` compile computes and reports the prune set (read-only) but
  deletes nothing and saves no state; two consecutive dry-runs report the same
  set.
- `clean` now removes every compiled file in the manifest, not just those with a
  symlink entry, so it leaves the compile directory empty rather than stranding
  compiled-but-never-linked files.
