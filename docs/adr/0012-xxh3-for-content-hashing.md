# Use XXH3 (non-cryptographic) for content hashing

Every content hash in dotsmith — the compile-idempotency check, the linker's
stale detection, and the manifest/state hashes — exists only for **change
detection**. None is an integrity or security boundary; the state file is
internal bookkeeping, not a trust artifact. We therefore drop `crypto/sha256`
for **XXH3** (`github.com/zeebo/xxh3`), using the **128-bit** digest. The three
previously independent hash sites (`compiler.hashContent`, `linker.hashBytes`,
and the inline hash in `compiledFileRefs`) collapse into a **single shared
helper** so every cross-stage comparison uses the identical algorithm.

## Considered Options

- **XXH3-128 via a single shared helper (chosen)** — fast, and 128-bit makes a
  collision (which here means dotsmith silently treating changed content as
  unchanged and leaving stale content linked) vanishingly unlikely. The digest
  is still shorter than SHA-256 (32 hex chars vs 64). One helper makes a
  mismatched-algorithm bug structurally impossible.
- **XXH3-64** — rejected, though defensible: ~2³² files for a coin-flip
  collision is far beyond any dotfiles set, but the cost of moving to 128-bit is
  negligible and removes the silent-staleness failure mode entirely. Given the
  project's paranoid posture, the conservative width is nearly free.
- **Keep SHA-256** — rejected: cryptographic strength buys nothing for change
  detection and costs throughput on every compile/link of every file.
- **XXH64 (`cespare/xxhash`)** — rejected: an older, narrower variant; XXH3 is
  the current generation and what "xxhash3" denotes.

## Consequences

- A new runtime dependency, `github.com/zeebo/xxh3`, justified in `go.mod`
  against the "dependencies only when strictly necessary" rule: fast
  non-cryptographic content hashing for change detection, not a security
  boundary.
- One-time upgrade churn: existing `.dotsmith.state` files hold SHA-256 hashes,
  so the first `link` after upgrade sees every stored hash mismatch and refreshes
  state once (everything reported `Updated`). Compile re-hashes on-disk content
  with XXH3 on both sides of its comparison, so unchanged files are still
  detected as unchanged — no needless rewrites. No migration shim; the churn is
  accepted.
- The hash is explicitly not a security or integrity guarantee. Any future need
  to *verify* content authenticity (vs merely detect change) must not reach for
  this hash.
