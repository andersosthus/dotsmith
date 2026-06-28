# Stop the empty-parent climb at symlinks, and refuse to link through one

The verify-then-disown guard (ADR 0011) protects the **leaf** target a deletion
sink touches, but not the **parent directories** the cleanup climbs through.
`safepath.RemoveEmptyParents` — reached from `cleanSymlinks` (`Clean`) and
`removeOrphanEntry` (`Link` orphan removal) — called `os.Remove` on each
ancestor unconditionally. `os.Remove` issues `unlink(2)`, which acts on a
*symlink itself*, so a symlink-to-directory ancestor is removed regardless of
whether the directory it points at is empty (the `rmdir` emptiness fallback
never runs). A user's non-managed symlinked parent dir — the common
`~/.config -> ~/cloud-synced/config` case — is the first ancestor climbed and
sits below `stopAt = TargetDir`, so the stop guard never protected it. The link
got silently unlinked; the data behind it survived, but subsequent writes to
`~/.config` landed in a fresh plain dir, diverging from the synced location.

We now `Lstat` each ancestor and **stop the climb at the first symlink**: a
symlinked directory is never something dotsmith created via `MkdirAll`, so it is
always left in place. As a paired guard, `linkNew` now calls
`guardSymlinkedParents`, which **refuses (conflict error) to plant a managed link
through a symlinked parent component** — mirroring `linkExisting`'s
"exists and is not our symlink" conflict semantics — so managed links are never
created inside a user's symlinked directory in the first place. The two guards
are independent on purpose: the removal-side guard protects users who already
have such a link from earlier runs or other tooling; the link-side guard stops
new ones forming.

`filepath.IsLocal` / `safepath.Join` do not help here: they are purely lexical,
and `.config/app.conf` *is* local. The escape is an on-disk symlink in a parent
component, invisible to string checks — and `classifyTarget` (verify-then-disown)
only inspects the leaf.

## Considered Options

- **Lstat-and-stop on the removal side, plus a link-side conflict guard
  (chosen)** — closes the gap from both directions. The removal-side check is the
  authoritative backstop (it also covers links planted by older binaries or other
  tools); the link-side conflict surfaces the problem early, at `link` time, with
  an actionable message instead of silently planting the link.
- **Removal-side Lstat-and-stop only** — defensible and the minimal fix, but it
  leaves dotsmith happily creating managed links inside the user's symlinked dir;
  the user only learns the layout is unsupported indirectly. The link-side guard
  is cheap and makes the contract explicit.
- **Resolve symlinks (`EvalSymlinks`) and operate on the real path** — rejected:
  that is the opposite of what is wanted. Following the symlink is exactly how the
  managed link ends up inside the user's synced directory; we want to refuse to
  cross it, not chase it.
- **Lexical guard via `IsLocal` / `safeJoin`** — rejected: the offending
  component is local; only an on-disk `Lstat` can see the symlink.

## Consequences

- A non-managed symlinked parent dir (and the data behind it) survives `clean`
  and orphan removal; the climb stops at it. A non-symlink, real, empty ancestor
  dotsmith created is still removed as before. An ancestor that cannot be `Lstat`d
  (e.g. removed concurrently) also stops the climb silently — leaving a directory
  in place is never a failure.
- `link` (and `apply`) now report a **conflict** for a target whose parent
  component is a symlink, refusing to create the link rather than planting it
  inside the symlinked directory. The fix is to replace the symlink with a real
  directory or move the target out from under it. The guard fires in `--dry-run`
  too, so a dry run faithfully reports the conflict it would hit. The leaf itself
  is not inspected by the guard (a symlinked *leaf* remains `linkExisting`'s job).
- This is the third member of the linker's safety triad: ADR 0008 keeps deletions
  *inside* the managed directories, ADR 0011 ensures that inside them dotsmith
  removes only its own leaf symlinks, and this ADR ensures the empty-parent climb
  never crosses — and the link planting never sits below — a user's symlink.
