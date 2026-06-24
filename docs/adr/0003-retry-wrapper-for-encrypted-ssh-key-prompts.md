# Retry passphrase prompts in a wrapper around the agessh encrypted identity

A matched passphrase-protected SSH key is prompted for up to three times before dotsmith gives up, and it is unlocked at most once per run. Both behaviours live in a `retryingEncryptedIdentity` wrapper around `agessh.NewEncryptedSSHIdentity` rather than in the passphrase callback we hand to agessh. This is recorded because the obvious place to put a retry loop — inside the callback — does not work, and a future contributor will likely try to "simplify" the wrapper away.

## Considered Options

- **Retry in a wrapper around agessh's identity (chosen)** — `agessh.EncryptedSSHIdentity.Unwrap` calls the passphrase callback at most once and aborts the entire decrypt on a wrong passphrase rather than re-prompting. To get three attempts we therefore re-run `Unwrap` (which re-invokes the lazy callback) in a loop one level up. The wrapper also classifies the error each pass: `age.ErrIncorrectIdentity` (tag mismatch) is returned unwrapped so age moves to the next candidate without prompting; a no-TTY or prompt-I/O error surfaced via the shared `pendingErr` is terminal and never retried; only a genuine wrong-passphrase failure is retried.
- **Retry inside the passphrase callback** — rejected: the callback fires once per `Unwrap`, so looping inside it cannot drive a second agessh decrypt attempt after a wrong passphrase. agessh has already aborted by the time control would return.
- **No retry (one attempt, then fail)** — rejected: a single typo would fail the whole compile, which is poor ergonomics for an interactive prompt.

## Consequences

- The no-TTY hard error must be threaded out of the callback through a shared `pendingErr` pointer, because agessh only propagates errors the callback itself returns; the wrapper reads `pendingErr` to distinguish a terminal failure (do not retry) from a wrong passphrase (retry).
- **Unlock-once-per-run** is a property of this layering, not extra code: agessh caches the decrypted key after the first successful `Unwrap`, and the same resolved `IdentitySet` is reused across every file in a run, so a key shared by many `.age` files prompts exactly once. Re-resolving per file, or discarding the wrapper's inner identity between files, would silently break this and re-prompt.
- The retry count is a single constant (`maxPassphraseAttempts = 3`); the prompt is still lazy, so non-matching encrypted keys never enter the loop and never prompt.
