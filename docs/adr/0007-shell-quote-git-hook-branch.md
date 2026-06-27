# Shell-quote the git-hook `--branch` value in generated hooks

`dotsmith git install --branch <name>` wraps the apply call in a branch guard inside the generated `post-merge` / `post-checkout` hooks (`hookBlockForBranch` in `internal/cli/git.go`). The branch name was concatenated directly into the generated shell, double-quoted: `if [ "$(git branch --show-current)" = "<branch>" ]; then`. A branch name containing a double quote or shell metacharacters would close the quote early and emit a syntactically broken hook. We now pass the value through a `shellQuote` helper that wraps it in single quotes and escapes any embedded single quote with the standard `'\''` sequence, so the branch is always a single shell word regardless of its characters.

This is **a correctness/robustness fix, not a vulnerability fix**. The `--branch` value is self-supplied on the command line; no untrusted source feeds it, so this was never an injection vector. The failure mode it prevents is self-inflicted: a user with an unusual branch name silently getting a hook that does not parse. (The other two items originally filed alongside this in the security audit — the non-X25519 age key type assertion and the `compile_dir`/`target_dir` containment check — were resolved by #18, which removed the `encrypt` path and stopped reading repo-local config.)

## Considered Options

- **Single-quote with `'\''` escaping (chosen)** — wraps the value as one shell word for any input, including quotes, spaces, and metacharacters, without imposing a charset restriction the user has not opted into. Matches how the rest of the generated hook treats values literally.
- **Validate the branch name against a git ref-name charset and reject** — rejected: more restrictive than necessary and duplicates rules git already enforces; quoting handles every valid (and invalid) name without an extra failure mode at install time.
- **Double-quote with backslash escaping** — rejected: double quotes still interpolate `$`, backticks, and `\`, so the guard could expand or execute parts of the branch name. Single quotes are inert.
- **Do nothing** — rejected: cheap to fix, and a silently broken hook is hard to diagnose because the hook only misbehaves when the named branch is checked out.

## Consequences

- The generated branch guard now reads `if [ "$(git branch --show-current)" = '<branch>' ]; then` (single-quoted). For ordinary branch names this is behaviourally identical to the previous double-quoted form.
- Branch names containing quotes or shell metacharacters produce a valid hook that matches the literal branch name rather than broken shell.
- An empty `--branch` still yields the plain unconditional hook block (no guard), unchanged.
