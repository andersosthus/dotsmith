# Compile reuses unchanged targets via a persisted source signature

Compile decrypted every `.age` source on every run, because decryption happens
eagerly in `Compile` (`sourceContent` → `encrypt.DecryptFile`) before
`WriteCompiled` ever sees the result. The existing content-hash check in
`writeCompiledFile` only skips the disk *write* — by then the age passphrase
prompt (painful for a passphrase-protected SSH identity) has already been paid.
We now let compile **reuse** the already-compiled output and skip decryption
entirely when nothing relevant changed.

Reuse is gated on two independent checks, both of which must pass *before*
decryption:

1. **Source signature (gate 1)** — the **ordered** digest of each contributing
   subfile's content hash, recorded per target in the manifest. For an `.age`
   source the hash is of the **ciphertext** bytes on disk; for a plain source,
   of its bytes. It moves when any subfile's body content changes, when subfiles
   are added/removed, or when their order changes.
2. **Output integrity (gate 2)** — the compiled file still on disk hashes to the
   `ContentHash` already in the manifest, i.e. a valid artifact actually exists
   to reuse (it was not wiped by a half-failed run, pruned, or hand-edited).

If both pass, the target is reused: not decrypted, not reassembled, not
rewritten — the existing compiled file is served untouched (its mode still
re-asserted, see Consequences). If *either* fails, compile falls back to the
current behaviour: decrypt every encrypted subfile and reassemble. **Reuse is a
pure optimization; on any mismatch, missing output, or doubt, compile
decrypts.**

The decision is folded into `Compile`: decryption lives there, so the decision
not to decrypt must precede it. `Compile` therefore gains `CompileDir`, loads the
prior state once, and threads it into `WriteCompiled` (one state load, not two).
A reused target still appears in `CompileResult.Files` (flagged, `Content` nil,
carrying the prior `ContentHash` and signature) — `apply` builds the linker's
`FileRef` list from that slice, so a dropped entry would make the linker remove
the symlink.

## Considered Options

- **Two-gate reuse of the on-disk output, decision folded into `Compile`
  (chosen)** — the only artifact reused is the compiled file that already
  legitimately exists; no plaintext is ever cached. Gate 2 makes reuse safe
  against a missing/corrupt output. Folding into `Compile` is where decryption
  lives, so it is the only place the skip can actually avoid the prompt; the
  source-vs-output coupling this introduces is essential to the feature, not
  accidental.
- **Hash the decrypted plaintext of `.age` sources for the signature** —
  rejected on two grounds: it would require decrypting to compute the signature
  (defeating the entire purpose), and it would store a plaintext-derived
  fingerprint of a secret in the state file. Hashing the ciphertext needs no key
  and leaks nothing the ciphertext does not already.
- **Cache decrypted plaintext and reassemble from it** — rejected: it puts
  secrets on disk outside the `0600` compiled file and defeats the encryption.
  The compiled output is the only thing we reuse.
- **Per-subfile reuse (decrypt only the changed subfiles of a target)** —
  rejected: we deliberately do not cache plaintext, so a target cannot be
  reassembled from a mix of cached and fresh fragments. Reuse is therefore
  **all-or-nothing per target**.
- **Gate 1 only, verify the output later in `WriteCompiled`** — rejected: if
  gate 2 failed there, decryption has already been skipped upstream and the
  output cannot be produced without a late decrypt (lazy thunks), which would
  move passphrase prompting into a function named "write" and complicate the
  dry-run probe flow.
- **Include `sourceName`/`layer` (the header inputs) in the signature** —
  rejected: they feed only the provenance comment, so including them would force
  a full decrypt whenever a subfile is renamed or moved between layers with
  identical bytes — a change that alters only a best-effort comment. Provenance
  headers are explicitly *not* part of the gate; a future `--force`/`--no-skip`
  flag covers the rare "I changed the header format and want everything
  refreshed" release.
- **A separate signature map or sidecar file** — rejected for the same reasons
  ADR 0010 folded `Compiled` into `.dotsmith.state`: a second key set to keep in
  sync, or a second reserved/containment-guarded file. The signature extends the
  existing `CompiledEntry` instead.

## Consequences

- `CompiledEntry` gains a `SourceSignature` field alongside `ContentHash`; the
  two answer the two gates ("have the inputs changed?" / "is the artifact still
  the one I wrote?"). `Compile` now loads and (via the threaded state)
  contributes to the saved manifest; `WriteCompiled` records the carried
  signature for reused targets and a freshly computed one for recompiled ones.
- **Reuse tolerates an unopenable identity.** Because the gate is the source
  signature, not decryptability, a target whose `.age` source the *current*
  identity can no longer open is still reused as long as its source is unchanged
  and its output intact — decryption is never attempted, so a rotated or broken
  key stays invisible until some source actually changes. This is deliberate
  (the whole point is not to prompt when nothing changed). `--dry-run` is the
  escape hatch: it probes every encrypted source unconditionally (ADR 0004,
  non-unlocking) and so remains the reuse-independent way to verify key health,
  now also annotating which targets would be reused.
- **Provenance headers are best-effort.** A subfile renamed or moved between
  layers with byte-identical content refreshes neither the body (correctly
  unchanged) nor its `# --- dotsmith: … ---` header label until a real change or
  a `--force`. An upgrade that changes header/assembly *format* can likewise
  leave old headers on reused targets until their sources change — accepted, as
  the body stays correct and forcing a global recompile on a cosmetic change
  reintroduces the prompt this feature removes.
- **The mode is still re-asserted on reuse** (continuing ADR 0009): a reused
  `FromEncrypted` file is `chmod`ed to `0600` (others `0644`) without a content
  rewrite, and the compile dir is still tightened to `0700`. Reuse is a more
  aggressive idempotent early-return, and 0009 explicitly requires the mode be
  repaired on exactly that path; skipping it would reopen the gap 0009 closed,
  on the path now hit most often.
- A new `Reused` count joins `Written`/`Unchanged`/`Pruned` in `WriteStats` and
  the `compile`/`apply` output. "Reused" (decryption skipped) is kept distinct
  from "Unchanged" (decrypted and reassembled, but byte-identical output, so no
  rewrite) so the avoided-prompt win is visible; post-reuse, "Unchanged" mostly
  means a re-encrypted-but-identical secret or the first post-upgrade run.
- **No migration shim** (matching ADRs 0010, 0012). Existing state files have no
  `source_signature`; the zero value can never match a computed signature, so
  the first compile after upgrade recompiles (and decrypts) every target once
  and records the signatures — reuse begins on the next run. The `ContentHash`
  side already migrated to xxh3 in 0012, so gate 2 is consistent on both sides.
  Downgrade is self-healing: an older binary ignores and then drops the unknown
  field, and the next new-binary run re-records it.
- Re-encrypting an unchanged secret changes its ciphertext bytes, so its
  signature moves and that target is decrypted once — an accepted false
  "changed". An unchanged file on disk is never needlessly decrypted, which is
  the case that matters.
