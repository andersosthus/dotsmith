# dotsmith

Dotfile manager combining GNU Stow-style symlink management with a Kustomize-inspired overlay
system and subfile-based composition.

Each machine gets the right dotfiles: a base layer everyone shares, overlaid by OS, hostname,
username, and user@host overrides. Subfiles let you split a single config across fragments from
multiple layers; they're assembled in order at compile time.

## Getting started

### Install

**Install script** (linux and darwin, amd64 and arm64):

```sh
curl -sSL https://raw.githubusercontent.com/andersosthus/dotsmith/main/install.sh | sh
```

Installs to `~/.local/bin` by default. Override with `DOTSMITH_INSTALL_DIR`:

```sh
DOTSMITH_INSTALL_DIR=/usr/local/bin curl -sSL \
  https://raw.githubusercontent.com/andersosthus/dotsmith/main/install.sh | sh
```

**AUR** (Arch Linux):

```sh
yay -S dotsmith-bin
```

**Manual download** from [GitHub Releases](https://github.com/andersosthus/dotsmith/releases):

```sh
tar -xzf dotsmith_<version>_<os>_<arch>.tar.gz
mv dotsmith ~/.local/bin/
```

**From source** with Go 1.26+:

```sh
go install github.com/andersosthus/dotsmith/cmd/dotsmith@latest
```

### Quick start

```sh
# Scaffold a new dotfiles repo (defaults to ~/.dotfiles)
dotsmith init

# Add your files to base/
cp ~/.bashrc ~/.dotfiles/base/.bashrc

# Compile + symlink in one step
dotsmith apply
```

After `apply`, `~/.bashrc` is a symlink pointing into the compile directory (`~/.dotcompiled`),
which in turn was assembled from your dotfiles layers.

### Shell completions

**From a release archive or AUR package:**

Release archives include pre-generated completion files in `completions/`. The AUR package installs
them automatically. For manual installs from a release archive:

- **Bash**: `cp completions/dotsmith.bash ~/.local/share/bash-completion/completions/dotsmith`
- **Zsh**: `cp completions/dotsmith.zsh /usr/local/share/zsh/site-functions/_dotsmith`
  (or any directory on `$fpath`)
- **Fish**: `cp completions/dotsmith.fish ~/.config/fish/completions/dotsmith.fish`

**Generate at runtime** (installs from source or when completions aren't bundled):

- **Bash**: `dotsmith shell bash > ~/.local/share/bash-completion/completions/dotsmith`
- **Zsh**: `dotsmith shell zsh > "${fpath[1]}/_dotsmith"`
  (or a custom directory added to `$fpath` before `compinit`)
- **Fish**: `dotsmith shell fish > ~/.config/fish/completions/dotsmith.fish`

How each shell loads completions:

- **Bash**: the `bash-completion` package lazy-loads from
  `~/.local/share/bash-completion/completions/`
- **Zsh**: loaded via `compinit` from directories listed in `$fpath`
- **Fish**: auto-loaded by command name from `~/.config/fish/completions/`

## Commands

| Command | Description |
|---------|-------------|
| `init` | Scaffold a new dotfiles repository structure |
| `compile` | Discover, decrypt, and assemble dotfiles into the compile directory; prune compiled files whose source no longer exists |
| `link` | Create symlinks from the compile directory to the target directory |
| `apply` | Compile dotfiles and link them to the target directory (compile + link) |
| `render <relpath>` | Compile a single dotfile and print it to stdout |
| `decrypt <file.age>` | Decrypt an age-encrypted file and print it to stdout |
| `status` | Report the status of managed symlinks |
| `identity` | Print the resolved OS, hostname, username, and user@host |
| `clean` | Remove managed symlinks and every compiled file dotsmith produced, emptying the compile directory |
| `git install` | Append dotsmith hook to `post-merge` and `post-checkout`; `--branch <name>` restricts the hook to that branch |
| `git remove` | Remove dotsmith hook from `post-merge` and `post-checkout` |
| `shell <bash\|zsh\|fish>` | Generate shell completion script |
| `version` | Print the dotsmith version |

### Global flags

| Flag | Description |
|------|-------------|
| `--config <path>` | Path to a config file (overrides default search) |
| `--dotfiles-dir <path>` | Path to dotfiles repository |
| `--compile-dir <path>` | Path to compiled output directory |
| `--target-dir <path>` | Path to symlink target directory |
| `--age-identity <path>` | Path to age identity file |
| `--verbose` | Enable verbose output |
| `--dry-run` | Print actions without writing any files |

## Directory structure

A dotsmith repository looks like this:

```
~/.dotfiles/
├── base/                  # applied to every machine
│   ├── .profile           # regular file — copied as-is
│   ├── .subfile-010.bashrc             # subfile fragment 010
│   ├── .subfile-020.bashrc             # subfile fragment 020
│   └── .config/
│       ├── git/
│       │   └── config     # regular file in a subdirectory
│       └── fish/
│           └── config.subfile-010.fish # subfile fragment 010 for config.fish
│
├── os/
│   ├── linux/             # applied on Linux machines
│   │   └── .subfile-050.bashrc
│   └── darwin/            # applied on macOS machines
│       └── .subfile-050.bashrc
│
├── hostname/
│   └── workstation/       # applied on host named "workstation"
│       ├── .subfile-020.bashrc         # replaces base fragment 020
│       └── .ssh/
│           └── config.age              # encrypted regular file
│
├── username/
│   └── alice/             # applied when logged in as alice
│       └── .subfile-090.bashrc
│
└── userhost/
    └── alice@workstation/ # applied for alice on workstation only
        └── .subfile-020.bashrc.ignore  # suppress fragment 020
```

After `dotsmith compile`, the compiled output (`~/.dotcompiled/` by default) mirrors the
relative paths of each target file. After `dotsmith link`, each compiled file is symlinked
into the target directory (`~` by default):

```
~/.bashrc  →  ~/.dotcompiled/.bashrc
~/.profile  →  ~/.dotcompiled/.profile
~/.config/git/config  →  ~/.dotcompiled/.config/git/config
```

### Pruning

`compile` owns the compile directory. It records every file it produces in a
manifest inside the state file (`.dotsmith.state`), and on each run it prunes any
compiled file that the manifest lists but the current compile no longer produces
— i.e. whose source was removed from your dotfiles repo. Pruning is scoped
strictly to the compile directory (dotsmith only deletes files it created itself)
and removes any parent directories left empty. The compile output line reports
the count: `compiled: N written, M unchanged, P pruned`.

When a pruned file still has a symlink pointing at it, that symlink is left
dangling until the next `link`. A bare `compile` prints a warning listing the
affected symlinks (capped, with a count of any remainder) and advising
`dotsmith link`. `apply` does not print this warning: it runs `link` immediately
after compiling, so the dangling symlinks are resolved before it returns.

`--dry-run` reports the same pruned and dangling sets without writing, removing,
or saving anything.

### Disowned paths

Before `link` removes an orphaned symlink, or `clean` removes a managed symlink,
it verifies the target really is the symlink dotsmith created: it must be a
symlink that still resolves to the expected compiled source. If the target is
something else — most often because you replaced the symlink with a real file of
your own — dotsmith *disowns* the path instead of deleting it. Your file is left
untouched, but dotsmith stops tracking the path: it removes its own compiled
artifact and drops the state entry, so it does not warn about the same path on
every run.

`link`, `clean`, and `apply` print a warning on stderr listing any disowned
paths (capped, with a count of any remainder). A correctly managed symlink is
still removed as before.

The same protection extends to **symlinked parent directories**. If a target's
parent is itself a symlink you created — for example `~/.config` pointing at a
cloud-synced location — dotsmith refuses to plant a managed link inside it:
`link` reports a conflict and tells you to replace the symlink with a real
directory or move the target out from under it. And when cleaning up empty
directories after a removal, the climb stops at the first symlinked ancestor, so
`clean` and orphan removal never unlink your symlinked parent dir.

### Conflicts and dry-run blockers

A *conflict* is a target dotsmith cannot link because it is occupied by
something that is not our managed symlink: a real file or non-symlink, a symlink
pointing somewhere else, or a symlinked parent directory (above). A real
`link`/`apply` run **fails fast** on the first conflict it hits — it stops
before touching the filesystem further and reports that single conflict with a
suggested fix.

`--dry-run` instead previews the whole picture: it continues past every
conflict, collecting each as a **blocker**, and reports them all together in one
run rather than one-at-a-time across repeated runs. The `linked: …` summary
(reflecting the files that *would* link) goes to stdout; the sorted blocker list
goes to stderr. A dry-run that finds any blockers exits non-zero so scripts and
CI get a "this would fail" signal even though nothing was written; a dry-run
with no blockers prints no blocker section and exits zero.

### Clean

`clean` tears down everything dotsmith created. It removes every managed symlink
(applying the disown guard above), then removes every compiled file recorded in
the manifest — not just the ones that had a symlink, but also files that were
compiled and never linked — so the compile directory is left holding no compiled
files, only the state file. Now-empty parent directories within the compile
directory are removed too. Finally it zeroes both state fields (the symlink
records and the compile manifest).

Each manifest entry is re-validated as living inside the compile directory before
removal, and an already-missing file is tolerated rather than treated as an
error.

## Subfiles

Subfiles let you split a single output file across multiple fragments, each potentially from a
different override layer.

**Naming convention:**

```
<stem>.subfile-<NNN>[.<ext>][.age]
```

The compiled target is `<stem><ext>` — the stem and extension joined without any separator.

Examples:
- `.subfile-010.bashrc` — fragment 010, compiles to `.bashrc`
- `.subfile-020.bashrc.age` — encrypted fragment 020, compiles to `.bashrc`
- `config.subfile-001.fish` — fragment 001, compiles to `config.fish`

The number `<NNN>` controls assembly order. Fragments are sorted using natural (numeric-aware)
order, so `subfile-2` sorts before `subfile-10` regardless of zero-padding. Gaps are allowed;
duplicate numbers within the same resolved set are an error.

The `<ext>` suffix determines the comment style for the provenance header inserted before each
fragment:

```sh
# --- dotsmith: .subfile-020.bashrc (hostname/workstation) ---
```

Supported comment styles: `#` (sh/py/yml/toml/conf), `//` (js/ts/go/rs/css), `--` (lua/sql),
`"` (vim), `;;` (lisp/el), `<!-- -->` (html/xml/svg). Unrecognised extensions get no header.

Regular files (not matching the subfile pattern) are copied as-is with no comment insertion.

## Overrides

Layers are applied in order of increasing specificity. Each layer can add new fragments,
replace existing ones, or suppress them.

**Precedence order:**

```
base  →  os/<goos>  →  hostname/<host>  →  username/<user>  →  userhost/<user@host>
```

**Three override actions:**

| File in a higher layer | Effect |
|------------------------|--------|
| Subfile with a **new** number | Added to the assembled output |
| Subfile with an **existing** number | Replaces the base layer's fragment with that number |
| `<stem>.subfile-<NNN>.<ext>.ignore` | Suppresses that fragment from the output |
| `<filename>.ignore` | Suppresses the entire regular file from the output |

**Identity auto-detection:**

| Field | Source |
|-------|--------|
| `os` | `runtime.GOOS` (e.g., `linux`, `darwin`) |
| `hostname` | `os.Hostname()`, domain suffix stripped |
| `username` | `user.Current().Username` |

Override any field in your config file:

```yaml
identity:
  hostname: workstation
  username: alice
  os: linux
```

Each identity value becomes a directory name under the matching override layer
(e.g. `os/linux`, `hostname/workstation`), so it must be a single path
component. A value that contains a path separator (`/` or `\`) or equals `..`
is rejected with an error rather than silently resolving a layer directory
outside the dotfiles repository.

## Configuration

Configuration is a **user-level** concern and is read only from the locations below — never from
a `.dotsmith.yml` inside the dotfiles repository. This keeps a cloned or adopted repo from
redirecting security-sensitive settings (the age identity, compile/target directories) on your
machine.

The first file found is used (highest precedence first):

1. `--config <path>` — explicit path (when given, only this file is loaded)
2. `$XDG_CONFIG_HOME/dotsmith/config.yml` (default `~/.config/dotsmith/config.yml`)
3. `~/.dotsmith.yml` — legacy fallback

CLI flags override the loaded file. Missing files are silently ignored, and a `.dotsmith.yml`
committed inside the repo is ignored. Run `dotsmith init` to scaffold a config at location 2.

**Full YAML schema:**

```yaml
# Path to the dotfiles repository.
# Default: ~/.dotfiles
dotfiles_dir: ~/.dotfiles

# Directory where compiled output is written. Kept private (mode 0700).
# Default: ~/.dotcompiled
compile_dir: ~/.dotcompiled

# Directory where symlinks are created.
# Default: ~
target_dir: ~

# Enable verbose output globally.
# Default: false
verbose: false

# Suppress all filesystem changes globally.
# Default: false
dry_run: false

# Identity overrides (auto-detected when not set).
identity:
  os: linux
  hostname: workstation
  username: alice

# Age decryption settings.
age:
  identity_file: ~/.age/key.txt   # native age key (optional)
  identities:                     # extra identity paths, any format (auto-detected)
    - ~/.ssh/id_ed25519
  ssh_discovery: true             # scan ~/.ssh for usable SSH keys (default: true)
```

**Defaults:**

| Key | Default |
|-----|---------|
| `dotfiles_dir` | `~/.dotfiles` |
| `compile_dir` | `~/.dotcompiled` |
| `target_dir` | `~` |
| `age.identity_file` | `~/.dotsmith-age-key` (tolerated if absent) |
| `age.identities` | *(empty)* |
| `age.ssh_discovery` | `true` |

## Encryption

Dotsmith uses [age](https://age-encryption.org) to **decrypt** files during compilation. Encrypted
files carry an `.age` extension and participate in the override system the same way as plaintext
files. Dotsmith does not encrypt files for you — encrypt them yourself with `age` (or `rage`) before
adding them to the repo, using a recipient that matches one of your identities.

Both age encodings are accepted transparently: the **binary** default (plain `age -r …`) and the
**ASCII-armored** form (`age -a -r …`). Dotsmith detects which by inspecting the file's leading
bytes, so you never declare or configure the encoding — a `.age` file produced either way compiles
the same. A file that is neither a valid binary nor armored age file surfaces age's own header error.

### Candidate identity set

Dotsmith does not decrypt with a single key. Each run it assembles a **candidate identity set** and
hands the whole set to age, which matches the file's recipients against it and uses whichever key
fits. The set is the union of:

1. the native age identity at `age.identity_file` (or `--age-identity`); a **missing default**
   `~/.dotsmith-age-key` is silently skipped, but a **missing explicitly configured** path is a hard
   error,
2. every path in `age.identities` (each auto-detected as a native age or SSH key), and
3. SSH private keys discovered in `~/.ssh/` when `age.ssh_discovery` is `true` (the default).

Supported SSH key types are exactly those age supports: `ssh-ed25519` and `ssh-rsa` (≥ 2048-bit).
Discovery skips `config`, `authorized_keys`, `known_hosts*`, `*.pub`, sockets, directories, and any
key age can't use (ecdsa, dsa, FIDO, undersized RSA) — silently, unless `--verbose` is set. Set
`age.ssh_discovery: false` to disable `~/.ssh/` scanning entirely.

### Decrypt with SSH keys on every machine, zero config

Because a single `.age` file can be encrypted to several SSH public keys at once, you can commit one
encrypted file and have each machine decrypt it with the SSH key it already has — no per-machine age
key to distribute:

```sh
# On each machine, grab its public key once (out of band).
# machine-a.pub, machine-b.pub, ...

# Encrypt to all machines' SSH public keys with the standalone age tool.
age -a \
  -R machine-a.pub -R machine-b.pub \
  -o base/.ssh/config.age base/.ssh/config
rm base/.ssh/config        # keep only the .age file in the repo
```

On machine A the file opens with A's `~/.ssh/id_ed25519`; on machine B with B's — from the same
committed file, with no dotsmith configuration. You can also encrypt to a native age recipient:

```sh
RECIPIENT=$(age-keygen -y ~/.dotsmith-age-key)
age -a -r "$RECIPIENT" -o base/.ssh/config.age base/.ssh/config
```

> **ssh-agent is not and cannot be supported.** age's SSH decryption needs the private key itself to
> perform the Ed25519→X25519 conversion and ECDH (or RSAES-OAEP), operations ssh-agent will not do —
> it only signs. Use an unencrypted SSH key for non-interactive (git-hook) compiles.

### Passphrase-protected SSH keys

If the SSH key that matches a file is passphrase-protected, dotsmith unlocks it lazily — only when a
file's recipients actually match that key, and never for keys that don't match:

- **On a terminal**, dotsmith prompts for the passphrase (up to three attempts) and unlocks the key
  **once per run** — a repo with many `.age` files encrypted to the same key prompts only once. With
  several keys in your set, only the key that matches the file is ever prompted for.
- **With no terminal** (e.g. a git hook), a file that needs a passphrase-protected key fails with a
  hard error naming the file and the key, rather than hanging. Files that match an unencrypted key
  still decrypt silently, so non-interactive compiles keep working as long as the matching key is
  unencrypted.

The passphrase is read only from the terminal. Dotsmith deliberately **never** reads a key passphrase
from an environment variable or a config field, so your passphrase is never parked in plaintext.

**Inspect an encrypted file:**

```sh
dotsmith decrypt base/.ssh/config.age
```

`dotsmith decrypt` uses the same candidate-set resolution as `compile`. Decrypted content is printed
to stdout; the `.age` file is not removed.

If no identity matches a file, dotsmith reports the file and the identities it tried (path and type),
so you know to re-encrypt including this machine's recipient.

During `compile` and `apply`, encrypted subfiles and regular files are decrypted in memory and
written with mode `0600` in the compile directory; decrypted content is never written back into the
dotfiles repo.

## Git hooks

Install dotsmith hooks so your dotfiles are re-applied whenever you pull changes to the
dotfiles repo:

```sh
cd ~/.dotfiles
dotsmith git install
```

This appends the following block to `.git/hooks/post-merge` and `.git/hooks/post-checkout`,
creating the files if they don't exist:

```sh
# --- dotsmith hook begin ---
dotsmith apply --verbose || true
# --- dotsmith hook end ---
```

### Branch-restricted hooks

To only run `dotsmith apply` when the current branch matches a specific name, pass `--branch`:

```sh
dotsmith git install --branch main
```

This wraps the apply call in a branch guard:

```sh
# --- dotsmith hook begin ---
if [ "$(git branch --show-current)" = 'main' ]; then
  dotsmith apply --verbose || true
fi
# --- dotsmith hook end ---
```

Useful when you keep feature branches in your dotfiles repo and want to avoid recompiling while
working on experimental changes.

Remove the hooks:

```sh
dotsmith git remove
```

## Development

**Prerequisites:** Go 1.26, [golangci-lint](https://golangci-lint.run)

```sh
go build ./...
go test ./...
go test -tags integration ./...
golangci-lint run
```

Run all three before committing. Fix every error and warning.

## Building

```sh
go build -o dotsmith ./cmd/dotsmith
```

Inject a version string at build time:

```sh
go build -ldflags "-X github.com/andersosthus/dotsmith/internal/cli.Version=1.0.0" \
  -o dotsmith ./cmd/dotsmith
```

## Releasing

Tag a version and push; GoReleaser runs automatically via GitHub Actions:

```sh
git tag v1.0.0
git push origin v1.0.0
```

GoReleaser produces:
- Binaries for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`
- `tar.gz` archives named `dotsmith_<version>_<os>_<arch>.tar.gz`
- `checksums.txt` (SHA-256)
- Auto-generated changelog (excludes `docs:`, `test:`, `chore:` commits)
