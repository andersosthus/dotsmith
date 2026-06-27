# Enforce that override-layer directories stay inside the dotfiles directory

Identity overrides (`identity.os` / `identity.hostname` / `identity.username`) are used verbatim as path components when building each override-layer directory (`layerDir := filepath.Join(dotfilesDir, string(layer.Layer), layer.Key)` in `internal/compiler/discover.go`). Because `filepath.Join` cleans `../` sequences, a value like `../../etc` would resolve `layerDir` outside `dotfilesDir`, and `applyLayer` would walk it and compile its readable files. We now enforce the implicit "layer dirs live inside the repo" invariant at two points: `resolveIdentity` rejects override values that contain a path separator (`/`, `\`) or equal `..`, and `Discover` asserts each computed `layerDir` resolves inside `dotfilesDir` (the relative path from `dotfilesDir` must not begin with `..`).

This is **defense-in-depth, not a vulnerability fix**. Since #18 removed repo-local config, identity overrides come only from trusted user config (`~/.config/dotsmith/config.yml`, `~/.dotsmith.yml`, or flags) — never an untrusted cloned repo. The traversal mechanism survived #18 but is no longer reachable from an untrusted source; the only remaining blast radius is self-inflicted misconfiguration. We close the gap anyway so the containment invariant is actually enforced rather than implied.

## Considered Options

- **Guard at both the config boundary and the discover boundary (chosen)** — `validateIdentityValue` fails fast with a clear, key-named error at config load, which is where a user can act on it; `assertWithinDotfilesDir` is the belt-and-suspenders check at the point of use, so any future feed into the layer key (not just config overrides) still cannot escape the repo. The two checks are independent on purpose.
- **Validate only in `resolveIdentity`** — rejected: it leaves the compiler trusting that every caller pre-validated the layer key, an invariant that is easy to break later when a new identity source is added.
- **Assert only in `Discover`** — rejected: it would accept a bad value at config time and only surface the failure deep in discovery, with a less actionable message than naming the offending config key.
- **Do nothing (rely on the trust boundary)** — rejected: cheap to enforce, and the implicit invariant should be explicit so a contributor cannot reintroduce the escape by relaxing one side.

## Consequences

- An identity override containing a path separator or `..` is rejected at config load with an error naming the offending key (`identity.os` / `.hostname` / `.username`); a layer that still resolves outside `dotfilesDir` is rejected at discover time with an error naming the layer label and the escaped path.
- The two guards must stay paired. If a future identity source bypasses `resolveIdentity`, the `Discover` containment check remains the backstop; neither should be removed on the assumption the other suffices.
- Override values are now constrained to a single path component, which matches how they are already used (`os/<goos>`, `hostname/<host>`, etc.) and forbids no legitimate configuration.
