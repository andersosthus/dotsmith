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

```sh
dotsmith shell bash >> ~/.bashrc
dotsmith shell zsh  >> ~/.zshrc
dotsmith shell fish > ~/.config/fish/completions/dotsmith.fish
```

## Commands

| Command | Description |
|---------|-------------|
| `init` | Scaffold a new dotfiles repository structure |
| `compile` | Discover, decrypt, and assemble dotfiles into the compile directory |
| `link` | Create symlinks from the compile directory to the target directory |
| `apply` | Compile dotfiles and link them to the target directory (compile + link) |
| `render <relpath>` | Compile a single dotfile and print it to stdout |
| `encrypt <file>` | Encrypt a file with age, writing `<file>.age` and removing the original |
| `decrypt <file.age>` | Decrypt an age-encrypted file and print it to stdout |
| `status` | Report the status of managed symlinks |
| `identity` | Print the resolved OS, hostname, username, and user@host |
| `clean` | Remove managed symlinks and compiled files |
| `git install` | Append dotsmith hook to `post-merge` and `post-checkout` |
| `git remove` | Remove dotsmith hook from `post-merge` and `post-checkout` |
| `shell <bash\|zsh\|fish>` | Generate shell completion script |
| `version` | Print the dotsmith version |

### Global flags

| Flag | Description |
|------|-------------|
| `--config <path>` | Path to `.dotsmith.yml` (overrides default search) |
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
├── .dotsmith.yml          # repo-level config (optional)
│
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

Override any field in `.dotsmith.yml`:

```yaml
identity:
  hostname: workstation
  username: alice
  os: linux
```

## Configuration

Config is loaded from two files (lowest to highest precedence), then CLI flags override both:

1. `<dotfiles-dir>/.dotsmith.yml` — repo-level config
2. `~/.dotsmith.yml` — user-level config (merged on top)
3. CLI flags — highest precedence

Missing files are silently ignored. If `--config` is given, only that file is loaded.

**Full YAML schema:**

```yaml
# Path to the dotfiles repository.
# Default: ~/.dotfiles
dotfiles_dir: ~/dotfiles

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

# Age encryption settings.
age:
  identity_file: ~/.age/key.txt
```

**Defaults:**

| Key | Default |
|-----|---------|
| `dotfiles_dir` | `~/.dotfiles` |
| `compile_dir` | `~/.dotcompiled` |
| `target_dir` | `~` |
| `age.identity_file` | *(none — must be set to use encryption)* |

## Encryption

Dotsmith uses [age](https://age-encryption.org) for file encryption. Encrypted files carry an
`.age` extension and participate in the override system the same way as plaintext files.

**Key resolution order:**
1. `age.identity_file` from config (or `--age-identity` flag)
2. `~/.dotsmith-age-key` (default location)

**Encrypt a file:**

```sh
dotsmith encrypt base/.ssh/config
# writes base/.ssh/config.age and removes base/.ssh/config
```

**Inspect an encrypted file:**

```sh
dotsmith decrypt base/.ssh/config.age
```

Decrypted content is printed to stdout. The `.age` file is not removed.

During `compile` and `apply`, encrypted subfiles and regular files are decrypted in memory and
written with mode `0600` in the compile directory.

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
