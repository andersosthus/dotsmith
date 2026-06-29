# Dry-run link collects all blockers; a real run still fails fast

`Link` processes each compiled file in a loop, and the first conflict — a target
occupied by a non-symlink, a symlink pointing elsewhere, or a symlinked parent
directory (`linkExisting`, `guardSymlinkedParents`) — returns an error that
aborts the whole call. For a real run that is correct: stop before mutating the
filesystem any further. But `--dry-run` shares the same code path, so a dry-run
that is supposed to *preview* the outcome also bailed on the first conflict,
showing the user one problem at a time across repeated runs instead of the full
picture.

We now make a **dry-run** continue past every per-file failure, collect each as
a **blocker**, and report them together; a real `link`/`apply` run is unchanged
and still fails fast on the first blocker with byte-identical error messages.

A blocker names the file and a `Kind`: a **conflict** (the three cases above —
the same situation `status` reports as `StatusConflict`) or an **io-error** (an
unexpected `lstat`/`readlink` failure other than not-exist). Both kinds are
collected; a blocked file is counted as neither created/updated/unchanged.
`LinkResult` gains `Blockers []Blocker`, sorted by relative path for stable,
testable output.

The classification has a single site: the conflict/io leaf paths return a typed
`*blockerError` whose `Error()` is today's exact leaf message and whose
`Unwrap()` preserves the underlying `%w` chain. `linkFile` extracts it with
`errors.As` only when `cfg.DryRun` is set, appending a `Blocker` and continuing;
otherwise it returns the error and the real run aborts exactly as before. In
dry-run `Link` therefore returns the populated result with a **nil** error —
blockers are data, not a library-level failure.

Exit-code policy stays in the CLI: when `len(result.Blockers) > 0`, the command
prints the normal `linked: …` summary to stdout (reflecting the non-blocked
files), the sorted blocker list to stderr, and returns a sentinel error so the
process exits non-zero. Because a real run never populates `Blockers` (it returns
on the first one), the CLI branch fires only in dry-run without gating on the
flag.

## Considered Options

- **Dry-run collects, real run fails fast, non-zero exit (chosen)** — a dry-run
  is a preview; its job is to surface *everything* the user must fix before the
  real run, in one pass. A real run must still stop before doing more filesystem
  work once a conflict proves the plan is unsafe, so its fail-fast behaviour and
  messages are left untouched. The asymmetry is deliberate: the two modes have
  different jobs. The non-zero exit preserves the "this would fail" signal for
  scripts/CI even though nothing was written.
- **Collect in both modes, then refuse to apply anything (all-or-nothing)** —
  rejected: it changes real-run semantics for no user-visible gain (the run
  still does nothing on conflict) while doing strictly more filesystem probing
  before failing. The fail-fast path is already correct and well-tested.
- **Collect in both modes and apply the non-conflicting files (partial apply)**
  — rejected: partial application gives weaker guarantees and a confusing
  half-linked state; dotsmith favours an all-or-nothing real run.
- **Dry-run exits zero (report only)** — rejected: a dry-run that finds blockers
  is previewing a run that *would* fail, so callers (CI, scripts) need a
  non-zero signal without parsing output. The full list is still printed first.
- **String-match the error messages to classify blockers** — rejected: fragile
  and couples classification to wording. The typed `*blockerError` gives a
  single classification site and keeps real-run messages byte-identical.
- **Have `Link` itself return a sentinel error alongside the populated result**
  — rejected: it pushes exit-code policy into the library and forces every
  caller to handle "error but also valid result." A dry-run that ran to
  completion succeeded; it merely found things. The CLI owns exit policy.
- **A four-value `BlockerKind` (occupied / wrong-link / symlinked-parent /
  io-error)** — rejected: no consumer needs to branch on the conflict sub-cause,
  the flat report does not group by kind, and the sub-cases are already
  distinguishable in `Detail`. Two kinds (`conflict`, `io-error`) align with the
  existing `StatusConflict` vocabulary.

## Consequences

- `LinkResult` gains `Blockers []Blocker`; `Blocker` carries `RelPath`, `Kind`
  (`conflict` | `io-error`), and `Detail` (the leaf message). Blockers are sorted
  by `RelPath` before return so output and tests are deterministic regardless of
  input ordering.
- The conflict/io leaf paths return `*blockerError` instead of bare
  `fmt.Errorf`. Its `Error()` reproduces the previous message verbatim and
  `Unwrap()` preserves the wrapped OS error, so `errors.Is` checks and the
  real-run fail-fast output are unchanged.
- Both `link` and `apply` use one shared CLI reporter (sibling to
  `warnDisowned`): summary to stdout, blocker list to stderr, and a
  `"<cmd>: N blocker(s) would prevent linking"` sentinel error driving exit 1.
  `main` prints that sentinel as the final stderr line.
- **The orphan-removal path is unchanged.** A dry-run still counts would-be
  disowns as removes on that path; classifying it accurately is a separate
  concern, out of scope here, because a foreign orphan target is gracefully
  disowned and never aborts the run.
- This is purely additive to the on-disk contract — no state-file change, no
  migration. The only observable changes are richer dry-run output and a
  dry-run exit code that is non-zero when blockers exist.
