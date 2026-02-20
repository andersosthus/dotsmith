# Dotsmith — Product Requirements Document

## Overview

Dotsmith is a dotfile management tool written in Go that combines the symlink management of GNU Stow with a Kustomize-inspired overlay system. It introduces a subfile-based composition model that allows users to split configuration files into manageable fragments, apply host- and user-specific overrides, and compile everything into a single output that gets symlinked into place.

The tool operates in two phases: a **compile** step that assembles subfiles and applies overrides, and a **link** step that creates symlinks from the compiled output to the target directory.

## Goals

- Provide a simple, predictable dotfile management workflow with a single mental model (subfiles) that handles composition, overrides, and exclusions.
- Support multi-machine and multi-user configurations without templating languages or complex merge logic.
- Achieve 100% test coverage.
- Distribute via GitHub Releases, Homebrew, and AUR.

## Non-Goals

- Templating or variable interpolation within dotfiles.
- Arbitrary named overlay groups.
- Watch mode (file watcher for auto-apply).
- Built-in age key generation (documentation should link to age docs for `age-keygen`).

---

## Core Concepts

### Subfiles

A configuration file can be split into numbered fragments called subfiles. Subfiles use the naming convention:

```
<filename>.subfile-<number>.<ext>
```

The number determines assembly order. Subfiles are sorted using natural (alphanumeric) sorting, which evaluates numeric segments mathematically rather than character-by-character. This means `subfile-2.sh` correctly precedes `subfile-10.sh`, even without zero-padding. During compilation, all applicable subfiles for a given target filename are concatenated in sort order to produce the final output file.

**Rules:**

- The number of digits is not enforced. `001`, `01`, `1`, and `10` are all valid, and natural sort handles mixed padding correctly (`1` < `2` < `10` < `099` < `100`).
- Gaps in numbering are allowed and expected (to leave room for insertion).
- Duplicate numbers within the same effective layer (after override resolution) are an error.
- The file extension is preserved after the subfile number so that editors and tools can provide syntax highlighting.

### Regular Files (Non-Subfile)

Files without the `.subfile-NNN` naming pattern are treated as whole files. They are copied as-is during compilation. They can still be overridden or ignored using the same mechanisms as subfiles.

### Overrides

Overrides allow OS-specific, host-specific, or user-specific variations. Override layers are stored as top-level directories in the dotfiles repository, each containing subdirectories named by the relevant identity value. Override directories can contain:

- **Replacement files/subfiles:** A file with the same name as a base file or subfile replaces it entirely.
- **Additional subfiles:** A subfile with a number not present in base is added to the assembly.
- **Ignore markers:** A file named `<original-filename>.ignore` (zero-byte) excludes the corresponding base file or subfile from compilation.

### Override Precedence

Overrides are applied in layers, with each successive layer able to override the previous:

```
base → os → hostname → username → userhost
```

- **base/** — Default files, always present.
- **os/\<os\>/** — Operating system overrides. Auto-detected via `runtime.GOOS` (e.g., `linux`, `darwin`). Useful for shared configuration across all machines of the same OS.
- **hostname/\<hostname\>/** — Machine-specific overrides. Hostname is auto-detected via short hostname (domain stripped).
- **username/\<username\>/** — User-specific overrides. Username is auto-detected from the current OS user.
- **userhost/\<username\>@\<hostname\>/** — Overrides for a specific user on a specific machine. Most specific, highest precedence.

All override layers are optional. The system works with any combination, including base-only.

### Compilation Resolution

For each target file, the compile step:

1. Collects all subfiles (or the whole file) from base.
2. For each override layer in precedence order:
   - Removes any files/subfiles targeted by `.ignore` markers.
   - Replaces any files/subfiles that have a same-named counterpart in the override.
   - Adds any new subfiles not present in previous layers.
3. Sorts the remaining subfiles by natural (alphanumeric) sort order.
4. Concatenates them to produce the compiled output.

### Subfile Comment Headers

When assembling subfiles, dotsmith inserts a comment before each fragment identifying its source file and override layer. This aids debugging by making it clear where each section of a compiled file originated.

Comment insertion is only performed for file extensions with a known comment syntax. Unrecognised extensions are assembled without comments.

**Supported comment styles:**

| Extensions | Comment Style |
|---|---|
| `.sh`, `.bash`, `.zsh`, `.fish`, `.py`, `.rb`, `.pl`, `.yml`, `.yaml`, `.toml`, `.conf`, `.cfg`, `.ini` | `# ...` |
| `.js`, `.ts`, `.go`, `.c`, `.cpp`, `.java`, `.rs`, `.css`, `.scss` | `// ...` |
| `.lua`, `.sql` | `-- ...` |
| `.vim` | `" ...` |
| `.el`, `.lisp` | `;; ...` |
| `.html`, `.xml`, `.svg` | `<!-- ... -->` |

**Format:**

```
# --- dotsmith: .bashrc.subfile-020.sh (hostname/workstation) ---
```

For base files with no override:

```
# --- dotsmith: .bashrc.subfile-010.sh (base) ---
```

**Rules:**

- Files with unrecognised extensions are assembled without comment headers.
- Regular (non-subfile) files do not receive a comment header since there is only one source.
- The comment style is determined by the file extension of the compiled output file (i.e., the target extension after stripping `.subfile-NNN` and `.age`).

### State Tracking

Dotsmith maintains a state file at `<compiledir>/.dotsmith.state` (JSON) that records:

- Each managed symlink: source path (in compile dir) and target path.
- Content hash of each compiled file at the time of linking.

This enables `status` to detect staleness and conflicts, and `clean` to remove only symlinks that dotsmith created.

### Encryption

Dotsmith supports encrypted files via [age](https://github.com/FiloSottile/age) (`filippo.io/age`), integrated as a Go library. Encrypted files are stored in the dotfiles repository with an `.age` extension appended to their filename and are decrypted transparently during the compile step.

**Naming convention:**

Any file (subfile or regular) with `.age` as the final extension is treated as age-encrypted. The `.age` extension is stripped during compilation, and the file is decrypted before assembly.

```
.bashrc.subfile-030.sh.age   → decrypted during compile, assembled as subfile-030
.ssh/config.age              → decrypted during compile, output as .ssh/config
```

**Key resolution:**

Dotsmith resolves the decryption identity in the following order:

1. **age identity file** specified in config (`age.identity_file`). This is the path to a standard age key file (as generated by `age-keygen`).
2. **age identity file** at the default location: `~/.dotsmith-age-key`.

For encryption (the `encrypt` command), the same resolution applies but in reverse for the public side: the corresponding recipient (public key) is derived from the identity file.

**Behaviour:**

- Encrypted files participate fully in the override and ignore systems. An override can replace an encrypted subfile with either an encrypted or unencrypted variant, and vice versa.
- `.ignore` works on the pre-decryption filename (i.e., `.bashrc.subfile-030.sh.age.ignore`).
- If decryption fails (wrong key, corrupt file), compilation halts with a clear error identifying the file that failed.
- Decrypted content is never written to the dotfiles repository — it only exists in the compiled output directory.

---

## Directory Structure

### Dotfiles Repository

```
~/.dotfiles/              (default, configurable)
├── .dotsmith.yml         (optional repo-level config)
├── base/
│   ├── .bashrc.subfile-010.sh
│   ├── .bashrc.subfile-020.sh
│   ├── .bashrc.subfile-030.sh
│   ├── .bashrc.subfile-040.sh.age  (encrypted — e.g., API keys)
│   ├── .vimrc             (regular file, no subfile splitting)
│   └── .config/
│       └── git/
│           └── config
├── os/
│   └── darwin/                          ← OS override
│       └── .bashrc.subfile-015.sh
├── hostname/
│   └── workstation/                     ← hostname override
│       ├── .bashrc.subfile-020.sh       ← replaces base 020
│       ├── .bashrc.subfile-050.sh       ← adds new fragment
│       └── .bashrc.subfile-030.sh.ignore ← excludes base 030
├── username/
│   └── anders/                          ← username override
│       └── .vimrc                        ← replaces base .vimrc
└── userhost/
    └── anders@workstation/              ← user+host override
        └── .bashrc.subfile-020.sh       ← replaces workstation's 020
```

### Compiled Output

```
~/.dotcompiled/           (default, configurable)
├── .dotsmith.state       (state tracking file)
├── .bashrc
├── .vimrc
└── .config/
    └── git/
        └── config
```

### Symlink Targets

```
~/                        (default, configurable)
├── .bashrc → ~/.dotcompiled/.bashrc
├── .vimrc → ~/.dotcompiled/.vimrc
└── .config/
    └── git/
        └── config → ~/.dotcompiled/.config/git/config
```

---

## Configuration

### Config File Location

Dotsmith looks for configuration in this order (later overrides earlier):

1. `<dotfiles-dir>/.dotsmith.yml` — travels with the repo, provides shared defaults.
2. `~/.dotsmith.yml` — user-local overrides.
3. CLI flags — highest precedence.

### Config File Schema

```yaml
# Directory containing the dotfiles repository
dotfiles_dir: ~/.dotfiles

# Directory for compiled output
compile_dir: ~/.dotcompiled

# Directory where symlinks are created
target_dir: ~

# Override auto-detection (optional, auto-detected if omitted)
identity:
  hostname: workstation
  username: anders
  os: linux

# Age encryption (optional)
age:
  identity_file: ~/.dotsmith-age-key
```

### Defaults

| Setting            | Default              |
|--------------------|----------------------|
| dotfiles_dir       | ~/.dotfiles          |
| compile_dir        | ~/.dotcompiled       |
| target_dir         | ~                    |
| hostname           | auto-detected        |
| username           | auto-detected        |
| os                 | auto-detected (`runtime.GOOS`) |
| age.identity_file  | ~/.dotsmith-age-key  |

---

## CLI

### Commands

| Command              | Description                                                                                   |
|----------------------|-----------------------------------------------------------------------------------------------|
| `dotsmith init`      | Scaffold a new dotfiles repo. Creates directory structure (`base/`, `os/`, `hostname/`, `username/`, `userhost/`), config file. Accepts `--dotfiles-dir` and `--target-dir`. Offers to install git hooks if the target is a git repo. After init, dotsmith can be run from anywhere. |
| `dotsmith compile`   | Resolve overrides and assemble subfiles into compiled output directory.                       |
| `dotsmith link`      | Create or update symlinks from compiled output to target directory.                           |
| `dotsmith apply`     | Run compile followed by link.                                                                 |
| `dotsmith render <file>` | Compile a single file and print the result to stdout. With `--verbose`, print subfile provenance info to stderr. |
| `dotsmith encrypt <file>` | Encrypt a file in place using age. Appends `.age` to the filename. Requires an age identity file. |
| `dotsmith decrypt <file>` | Decrypt an `.age` file and print the plaintext to stdout. Does not modify the original file. Requires an age identity file. |
| `dotsmith status`    | Show current state: active symlinks, stale links, conflicts (target exists but is not a managed symlink), and whether compiled output is up-to-date. |
| `dotsmith clean`     | Remove all managed symlinks and compiled output. Uses state file to identify managed resources. |
| `dotsmith git install` | Install post-merge and post-checkout git hooks in the dotfiles repo that run `dotsmith apply --verbose`. Detects existing hooks and appends rather than overwrites. Hook runs non-fatally (warns on failure, does not block git operations). |
| `dotsmith git remove`  | Remove dotsmith git hooks from the dotfiles repo.                                           |
| `dotsmith shell <bash\|zsh\|fish>` | Generate shell completion script for the specified shell.                       |
| `dotsmith version`   | Print version information.                                                                    |
| `dotsmith help`      | Print usage information.                                                                      |

### Global Flags

| Flag                        | Description                                                      |
|-----------------------------|------------------------------------------------------------------|
| `--verbose`                 | Print detailed output including override resolution and file provenance. |
| `--dry-run`                 | Show what would happen without making any changes.               |
| `--config <path>`           | Path to config file, overriding default discovery.               |
| `--dotfiles-dir <path>`    | Path to dotfiles repository, overriding config and default.      |

---

## Behavioural Requirements

### Idempotency

Both `compile` and `link` must be safe to run repeatedly.

- **compile** should only write files whose content has actually changed (compare content hashes before writing).
- **link** should handle all symlink states gracefully: missing symlink (create), existing correct symlink (no-op), existing stale symlink (update), existing regular file at target (error with guidance).

### File Permissions

The compiled output directory may contain decrypted secrets and must be protected accordingly.

- The compile directory (`~/.dotcompiled/`) and all subdirectories must be created with `0700` permissions.
- Compiled files that originated from `.age` encrypted sources must be written with `0600` permissions.
- Non-secret compiled files should use `0644` permissions.
- The state file (`.dotsmith.state`) must be written with `0600` permissions.

### Directory Creation

The `link` command must recursively create missing parent directories in the target directory before creating symlinks. For example, if linking `~/.config/git/config`, the directories `~/.config/` and `~/.config/git/` must be created if they do not exist. Created directories should use `0755` permissions. The `clean` command should remove empty parent directories it created, working upward until reaching the target dir root or a non-empty directory.

### Dry Run

When `--dry-run` is active, no filesystem modifications occur. All commands must report what they would do. This applies to compile, link, apply, clean, init, and git hook management.

### Error Handling

- Duplicate subfile numbers within the same effective resolution: hard error.
- `.ignore` targeting a file that doesn't exist: warning (not an error).
- Target path occupied by a regular file (not a symlink): error with a clear message explaining the conflict and how to resolve it.
- Git hook already exists from another tool: warn and append rather than overwrite.
- Hostname, username, or OS auto-detection failure: error with guidance to set identity explicitly in config.
- Decryption failure during compile (wrong key, corrupt file, missing identity): hard error identifying the specific file.
- `encrypt` called on a file that already has `.age` extension: error.
- `decrypt` called on a file without `.age` extension: error.
- No identity file found and stdin is not a terminal (non-interactive context): error with guidance to configure an identity file.

### Verbose Output

When `--verbose` is active, dotsmith should report:

- Active identity: os, hostname, username, resolved override layers.
- Per-file: which subfiles contributed, from which layer, any replacements or ignores applied.
- For `render`: provenance info goes to stderr so stdout remains pipeable.

---

## Distribution

### Build and Release

- **Goreleaser** for automated cross-platform builds on tagged releases.
- Target platforms: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64.
- Goreleaser produces binaries, tarballs, checksums, and shell completion scripts.

### Package Channels

| Channel            | Details                                                                                       |
|--------------------|-----------------------------------------------------------------------------------------------|
| GitHub Releases    | Prebuilt binaries and tarballs for all platforms. Baseline distribution method.               |
| Homebrew Tap       | Goreleaser auto-updates the tap formula on release. Covers macOS and Linux Homebrew users.   |
| AUR (`-bin`)       | PKGBUILD that pulls prebuilt binary from GitHub Releases. Targets the Arch Linux audience.   |
| Install script     | `curl -sSL https://... \| sh` installer. Detects platform, downloads correct binary from GitHub Releases. Supports updating an existing installation to the latest version. |

### Shell Completions

Generated via Cobra's built-in completion generation for bash, zsh, and fish. Available both as:

- Release artifacts bundled into packages.
- Runtime generation via `dotsmith shell <shell>`.

---

## Technical Stack

| Component     | Choice     | Notes                                                    |
|---------------|------------|----------------------------------------------------------|
| Language      | Go         |                                                          |
| CLI framework | Cobra      | Provides command structure, flag parsing, help generation, and shell completion. |
| Config        | Viper      | Natural companion to Cobra. Supports YAML config files with env/flag override. |
| Encryption    | age        | `filippo.io/age` Go library. Supports identity file (public key) encryption. |
| Build/Release | Goreleaser | Cross-compilation, packaging, Homebrew tap automation.   |
| Testing       | Go stdlib  | `testing` package. 100% coverage target.                 |

---

## Testing Strategy

The 100% test coverage target requires a deliberate approach to testability.

### Unit Tests

- **Subfile discovery and sorting:** Given a directory structure, verify correct file enumeration and natural sort ordering. Specifically test mixed zero-padding (`1`, `02`, `10`, `099`) to confirm numeric-aware sorting.
- **Override resolution:** Given base + override layers (including OS layer), verify correct precedence, replacement, addition, and ignore behaviour for every combination.
- **Compilation:** Given resolved file lists, verify correct concatenation output.
- **Comment insertion:** Verify correct comment style selection per extension, correct provenance labels (base vs override layer), no comments for unrecognised extensions, and no comments for regular (non-subfile) files.
- **Config loading:** Test discovery order, merging, defaults, and flag overrides.
- **State file:** Test read, write, staleness detection, and corruption handling.
- **Encryption/decryption:** Test identity-file-based encrypt/decrypt round-trips, compile with mixed encrypted/unencrypted subfiles, and failure modes (wrong key, corrupt file, missing identity file).

### Integration Tests

- **End-to-end compile → link → status → clean cycle** using temporary directories.
- **Init scaffolding** verification.
- **Git hook install/remove** using a temporary git repo.
- **Render output** verification for both simple and complex file assemblies.
- **Dry-run** verification: assert no filesystem side effects.
- **File permissions:** Verify compile directory is `0700`, decrypted files are `0600`, non-secret files are `0644`, state file is `0600`.
- **Directory creation:** Verify link creates missing parent directories and clean removes empty directories it created.
- **Idempotency:** run compile and link twice, verify identical results and no unnecessary writes.
- **Encrypt/decrypt commands:** verify in-place encryption with rename, stdout decryption, and round-trip integrity with identity file mode.

### Filesystem Concerns

- All tests that touch the filesystem should use `t.TempDir()` for isolation.
- Tests should cover edge cases: empty directories, deeply nested paths, symlink cycles, permission errors, missing directories.

---

## Open Questions / Future Considerations

- **Additional comment styles:** The initial set of supported extensions covers common dotfile formats. The list may need expanding based on user feedback. Consider making it extensible via config.
- **Compile performance:** For large dotfile repos, content-hash-based change detection should keep recompilation fast. Monitor whether decryption of many `.age` files becomes a bottleneck.
