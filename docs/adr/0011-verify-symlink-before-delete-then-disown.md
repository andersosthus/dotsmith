# Verify a managed symlink before deleting it, and disown on conflict

The linker's two deletion sinks — `removeOrphanEntry` (orphan removal during
`Link`) and `cleanSymlinks` (`Clean`) — removed the target path
unconditionally. If a user had replaced a managed symlink with a real file of
their own, dotsmith would delete that file. We now `lstat` the target, confirm
it is a symlink, and `readlink` to confirm it resolves to the compiled source we
expect (the same check `linkExisting` already uses); only then do we remove it.
The readlink comparison is on the link's target string, so it still works after
the compiled source has been pruned.

When the target is *not* our symlink, we **disown** the path: leave the user's
file untouched, but still remove dotsmith's own compiled artifact and drop the
state entry. The orphan's source is gone, so there is nothing to recreate;
dotsmith cleanly stops managing the path rather than retrying-and-skipping every
run. Disowned paths are returned to the CLI, which warns the user.

## Considered Options

- **Verify symlink + readlink target, then disown (chosen)** — protects
  user-created files at the deletion sink, mirrors the existing `linkExisting`
  guard, and leaves no orphaned compiled file or stale state entry behind.
- **Bare is-a-symlink check** — rejected: it still deletes a symlink the *user*
  created pointing somewhere outside the compile directory. Confirming the link
  resolves to our expected source closes that gap at no extra cost.
- **Leave the whole entry when the target is a conflict** — rejected: the
  compiled artifact and state entry persist, so every subsequent run retries the
  removal and skips again, and the compile directory keeps an orphan forever.
- **Remove the artifact but keep the state entry** — rejected: worst of both,
  same perpetual retry with no upside.

## Consequences

- A managed path the user has repurposed (symlink replaced with a real file) is
  left intact; dotsmith forgets it (state entry dropped, compiled artifact
  removed) and the CLI warns that the path was skipped.
- The verify-then-delete guard is the behavioural pair to ADR 0008's containment
  guard: 0008 ensures we never delete *outside* the managed directories; this
  ensures that *inside* them we only delete our own symlinks, not files the user
  substituted.
