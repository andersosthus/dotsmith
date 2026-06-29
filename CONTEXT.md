# Dotsmith

Dotfile management: subfile composition + Kustomize-style overlays + Stow-style
symlinks. This glossary pins the vocabulary that is easy to confuse — especially
around age decryption, where "key", "recipient", and "identity" are not
interchangeable.

## Language

### Encryption / decryption

**Identity**:
The secret key material dotsmith uses to *decrypt* an `.age` file. Dotsmith only
ever decrypts, so it only ever holds identities — never recipients.
_Avoid_: key, private key (too vague — say which kind of identity).

**Recipient**:
A *public* key an `.age` file was encrypted *to*. Recipients are chosen
out-of-band when the file is encrypted; dotsmith never holds or manages them, it
only reasons about which of its identities matches a file's recipients.
_Avoid_: public key, pubkey (when the age-format role is what matters).

**Native age identity**:
An identity in age's own key format (`AGE-SECRET-KEY-1…`), e.g. from
`age-keygen`. Configured explicitly via `age.identity_file`.
_Avoid_: age key, x25519 key.

**SSH identity**:
An identity backed by an existing SSH private key — `ssh-ed25519` or `ssh-rsa`
(RSA ≥ 2048-bit) only. Lets the same `.age` file be opened on different machines
by each machine's own SSH key, without per-machine config.
_Avoid_: ssh key (ambiguous between the public and private halves).

**Candidate identity set**:
The full collection of identities dotsmith offers to age for one decrypt — the
union of the configured native identity and all discovered SSH identities. age
matches a file's recipients against this set and uses whichever fits.
_Avoid_: keyring, identity list.

**Identity discovery**:
Building the candidate set from the machine automatically — primarily by reading
the SSH keys already present on the machine — rather than requiring each one to
be named in config.
_Avoid_: key scanning, auto-load.

**Binary age file**:
An `.age` file in age's default encoding — the raw binary format whose intro line
is the literal `age-encryption.org/v1`. This is what plain `age -r …` produces.
_Avoid_: raw age file, unarmored file.

**Armored age file**:
An `.age` file in age's ASCII-armor encoding — a PEM-style envelope opening with
`-----BEGIN AGE ENCRYPTED FILE-----`, produced only when `age` is run with
`-a`/`--armor`. Dotsmith accepts both encodings transparently, detecting which by
inspecting the leading bytes; `.age` on disk implies neither encoding.
_Avoid_: PEM age file, text age file.

### Compilation and linking

**Source signature**:
The per-target gate that lets compile *reuse* the existing compiled output
instead of decrypting and reassembling. It is the **ordered** digest of each
contributing subfile's content hash — the ciphertext hash for an `.age` source,
the plaintext hash for a plain one. It moves when any subfile's body content
changes, when subfiles are added or removed, or when their order changes; it
deliberately ignores header-only changes (a rename or layer move with identical
bytes), which affect only provenance comments. Distinct from a content hash,
which fingerprints a single file's bytes.
_Avoid_: source hash (overloads "content hash"), checksum.

**Reuse** (compile):
Skipping a target's decryption and reassembly because its source signature still
matches the recorded one *and* its compiled output is still intact on disk —
serving the already-compiled file untouched. A pure optimization: on any
mismatch, missing output, or doubt, compile falls back to a full decrypt and
recompile. Reuse is the point of the signature — it avoids the age passphrase
prompt when nothing relevant changed.
_Avoid_: cache, bare "skip" (too vague — say what is reused).

**Compile manifest**:
The recorded set of files the previous compile produced (relative path → content
hash), persisted in the state file. It is the authority for what compile may
*prune* — compile deletes only files it knows it created, never whatever it finds
in the compile directory.
_Avoid_: file list, index, cache.

**Prune**:
Removing a compiled file from the compile directory because its source no longer
exists. A compile-time act, scoped to the compile directory only.
_Avoid_: clean (that is the user-facing teardown command), delete (too vague).

**Orphaned compiled file**:
A file in the compile directory present in the previous manifest but absent from
the current compile result — i.e. its source was removed. Pruning targets exactly
these.
_Avoid_: bare "orphan" (ambiguous with the symlink sense below).

**Orphaned symlink**:
A managed symlink in the state file with no corresponding file in the current
compile result. The linker removes these; pruning does not.
_Avoid_: bare "orphan".

**Dangling symlink**:
A managed symlink in the target directory whose compiled source has been pruned.
A transient state a bare `compile` can leave behind; the next `link` resolves it.
_Avoid_: broken link, stale link (stale has a distinct meaning — content changed,
not source removed).

**Disown**:
Ceasing to manage a path — dropping its state entry and removing dotsmith's own
artifact — without touching what the user has put in its place. Done when a
managed symlink has been replaced by a real file the user created.
_Avoid_: orphan, abandon, release.

**Blocker**:
A reason a single file could not be linked during a `link` operation — naming
the file, a *kind*, and a human-readable detail. Kinds: a **conflict** (the
target is occupied by something that is not our managed symlink — the same
situation `status` reports as `StatusConflict`, covering an occupied path, a
symlink pointing elsewhere, or a symlinked parent directory) or an **io-error**
(an unexpected filesystem error other than not-exist while inspecting the
target). A real `link` run still fails fast on the first blocker; a dry-run
collects *all* blockers across every file and reports them together.
_Avoid_: bare "conflict" for the whole set (conflict is one kind of blocker),
error (too vague).
