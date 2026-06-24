# Probe `--dry-run` decryption without unlocking, prompting, or parsing the age header ourselves

Under `--dry-run`, dotsmith reports which identity *would* decrypt each `.age` file (or that none on this machine matches) without unlocking any key, firing any passphrase prompt, or writing anything. Two non-obvious techniques make this possible, and both are recorded here because a future contributor will be tempted to "simplify" them and will reintroduce prompting or break matching by doing so.

First, recipient stanzas are recovered from the file header via a **sentinel `captureIdentity`** handed to `age.Decrypt`: its `Unwrap` records the stanzas and returns `age.ErrIncorrectIdentity`, so age parses the header for us, hands the stanzas to the sentinel, and then declines every real identity — never prompting or decrypting. The success signal is therefore an `age.NoIdentityMatchError` from `Decrypt`; any other error means the header genuinely failed to parse.

Second, SSH matching is decided **purely from public-key data**: `sshTagFor` recomputes the recipient tag age embeds in an `ssh-ed25519` / `ssh-rsa` stanza — the first four bytes of `SHA-256(pubkey.Marshal())`, raw-std-base64 — and compares it to each stanza's tag, exactly as `agessh.sshFingerprint` does. This needs only the public key, so an encrypted (passphrase-protected) SSH key is reported without ever prompting.

## Considered Options

- **Sentinel capture + public-key tag replication (chosen)** — reads the header through age's own parser via the sentinel, then matches SSH candidates on the tag alone and native-age candidates with an in-memory ECDH unwrap of an already-loaded key. No passphrase callback can fire, because the SSH probe never touches the private key and the native key is never passphrase-protected.
- **Parse the age header ourselves to read the recipient stanzas** — rejected: age's header parser is in an `internal` package and not importable. The sentinel borrows age's parser without forking it.
- **Reuse the real decrypt path and suppress the prompt** — rejected: the real path unlocks keys and would prompt (or hard-error with no TTY) for a matched passphrase-protected key, which is exactly the side effect dry-run must avoid. A probe that decides matches from public-key data alone sidesteps the unlock entirely.

## Consequences

- **`sshTagFor` is a deliberate duplication of an unexported age function (`agessh.sshFingerprint`) and is coupled to age's wire format.** If age ever changed the stanza tag derivation, the probe would silently report wrong matches while real decryption still worked. This is accepted because the tag derivation is part of the stable age file format, not an implementation detail, but it is the one thing to re-verify on an age upgrade.
- **Native age identities cannot be probed by tag** — age's X25519 stanzas carry no public-key tag, so the only way to detect a match is to run the unwrap. `nativeProbe` does exactly that. This is safe only because a native age identity is already loaded in memory and is never passphrase-protected; the same trick must not be extended to encrypted keys, or dry-run would unlock them.
- **`render` and `decrypt` are intentionally excluded from dry-run probing** — they stream plaintext to stdout, so a "preview without side effects" has no meaning for them; only `compile` and `apply` carry `DryRunReports`.
- A file matched by no candidate is reported during dry-run (`Matched == false`) from the stanzas alone, surfacing a "not encrypted to any key on this machine" situation before a real compile is attempted.
