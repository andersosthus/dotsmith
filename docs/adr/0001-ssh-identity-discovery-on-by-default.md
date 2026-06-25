# Discover SSH decryption identities from `~/.ssh/` by default

The decryption identity is a **candidate identity set** — the union of the configured native age key, explicit `age.identities` paths, and SSH private keys discovered by scanning `~/.ssh/` — and that scan is **on by default** (opt out with `age.ssh_discovery: false`). We chose default-on discovery because the motivating use case is one encrypted file committed once and decrypted on many machines using each machine's existing SSH key; requiring a config line per machine would defeat the zero-config goal. age matches a file's embedded recipient tags against the set, so supplying many candidates is safe.

## Considered Options

- **Default-on discovery (chosen)** — zero config on a normal machine; a kill-switch for anyone who wants dotsmith to never read `~/.ssh/`.
- **Opt-in discovery** — rejected: every machine needs one config line, undercutting the multi-machine ergonomics that justify the feature.
- **Config-only, no scanning** — rejected for the same reason.

## Consequences

- dotsmith reads files in `~/.ssh/` the user did not explicitly name. This stays within the trust boundary established in #18 (configuration is a user-level concern): `~/.ssh/` is user-owned, the same trust tier as user config, and an untrusted dotfiles repo still cannot redirect it. The added surface is the user's own home directory, not the repo.
- Surprise is bounded by the lazy-passphrase design: non-matching keys are skipped by tag and never prompt, so default-on discovery is not noisy in practice.
