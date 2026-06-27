// Package config loads and merges dotsmith configuration from YAML files,
// environment variables, and CLI flags using Viper.
package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"

	"github.com/andersosthus/dotsmith/internal/identity"
)

// Flags holds the CLI flag values that override config file settings.
type Flags struct {
	// ConfigPath is an explicit path to a config file (-c / --config).
	ConfigPath string
	// DotfilesDir overrides the dotfiles directory.
	DotfilesDir string
	// CompileDir overrides the compile output directory.
	CompileDir string
	// TargetDir overrides the symlink target directory.
	TargetDir string
	// AgeIdentity overrides the age identity file path.
	AgeIdentity string
	// Verbose enables verbose output.
	Verbose bool
	// DryRun suppresses all filesystem changes.
	DryRun bool
}

// Config is the fully resolved configuration for a dotsmith run.
type Config struct {
	// DotfilesDir is the path to the dotfiles repository.
	DotfilesDir string
	// CompileDir is the directory where compiled output is written.
	CompileDir string
	// TargetDir is the directory where symlinks are created.
	TargetDir string
	// Identity is the resolved identity used for override layer selection.
	Identity identity.Identity
	// AgeIdentity is the path to the native age identity file for decryption.
	AgeIdentity string
	// AgeIdentityExplicit is true when AgeIdentity was set by the user (config or
	// flag) rather than left at its default. A missing default key is tolerated;
	// a missing explicit key is a hard error.
	AgeIdentityExplicit bool
	// AgeIdentities holds extra identity paths (native age or SSH), each
	// format-auto-detected. Every entry is treated as explicit.
	AgeIdentities []string
	// AgeSSHDiscovery toggles scanning ~/.ssh/ for usable SSH private keys.
	// Defaults to true.
	AgeSSHDiscovery bool
	// Verbose enables verbose output.
	Verbose bool
	// DryRun suppresses all filesystem changes.
	DryRun bool
}

// Load reads and merges configuration from disk and the provided flags.
// Discovery order (first existing file wins): --config →
// $XDG_CONFIG_HOME/dotsmith/config.yml → ~/.dotsmith.yml, then flags override.
// The repo-local <dotfilesDir>/.dotsmith.yml is never read: configuration is a
// user-level concern only, so an untrusted dotfiles repo cannot redirect the
// age identity, compile/target directories, or identity. Missing config files
// are silently ignored.
func Load(_ context.Context, flags Flags) (Config, error) {
	v := viper.New()
	setDefaults(v)

	if err := loadConfigFiles(v, flags.ConfigPath); err != nil {
		return Config{}, err
	}

	id, err := resolveIdentity(v)
	if err != nil {
		return Config{}, err
	}

	// AgeIdentity is explicit when set by the --age-identity flag or by the user
	// in config; the un-overridden default is tolerated if absent. Viper's IsSet
	// reports a config-provided key only because age.identity_file has no Viper
	// default.
	ageIdentityExplicit := flags.AgeIdentity != "" || v.IsSet("age.identity_file")
	ageIdentity := flags.AgeIdentity
	if ageIdentity == "" {
		ageIdentity = v.GetString("age.identity_file")
	}
	if ageIdentity == "" {
		ageIdentity = defaultAgeIdentityFile
	}

	return Config{
		DotfilesDir:         coalesce(flags.DotfilesDir, expandHome(v.GetString("dotfiles_dir"))),
		CompileDir:          coalesce(flags.CompileDir, expandHome(v.GetString("compile_dir"))),
		TargetDir:           coalesce(flags.TargetDir, expandHome(v.GetString("target_dir"))),
		Identity:            id,
		AgeIdentity:         expandHome(ageIdentity),
		AgeIdentityExplicit: ageIdentityExplicit,
		AgeIdentities:       expandHomeAll(v.GetStringSlice("age.identities")),
		AgeSSHDiscovery:     v.GetBool("age.ssh_discovery"),
		Verbose:             flags.Verbose || v.GetBool("verbose"),
		DryRun:              flags.DryRun || v.GetBool("dry_run"),
	}, nil
}

// setDefaults configures Viper defaults.
func setDefaults(v *viper.Viper) {
	v.SetDefault("dotfiles_dir", "~/.dotfiles")
	v.SetDefault("compile_dir", "~/.dotcompiled")
	v.SetDefault("target_dir", "~")
	// age.identity_file is deliberately left without a Viper default so IsSet can
	// distinguish a user-configured path from the built-in default; the default is
	// applied in Go (see defaultAgeIdentityFile).
	v.SetDefault("age.ssh_discovery", true)
}

// defaultAgeIdentityFile is the built-in native age identity path used when the
// user has not configured one. A missing file at this path is tolerated.
const defaultAgeIdentityFile = "~/.dotsmith-age-key"

// loadConfigFiles reads the single applicable user-level config file into v.
// An explicit --config path takes precedence; otherwise the first existing file
// in userConfigSearchPaths wins. A file that exists but is invalid is an error;
// a missing file is silently ignored. The repo-local config is never consulted.
func loadConfigFiles(v *viper.Viper, explicitPath string) error {
	if explicitPath != "" {
		v.SetConfigFile(expandHome(explicitPath))
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("load config %s: %w", explicitPath, err)
		}
		return nil
	}

	for _, path := range userConfigSearchPaths() {
		if _, statErr := os.Stat(path); statErr != nil {
			continue // file does not exist or is inaccessible
		}
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("load config %s: %w", path, err)
		}
		return nil // first existing file wins
	}

	return nil
}

// userConfigSearchPaths returns the ordered list of user-level config paths to
// check, highest precedence first.
func userConfigSearchPaths() []string {
	return []string{
		filepath.Join(xdgConfigHome(), "dotsmith", "config.yml"),
		filepath.Join(userHomeDir(), ".dotsmith.yml"),
	}
}

// UserConfigPath returns the preferred location for the user's config file:
// $XDG_CONFIG_HOME/dotsmith/config.yml (default ~/.config/dotsmith/config.yml).
// It is where `dotsmith init` writes the config template.
func UserConfigPath() string {
	return userConfigSearchPaths()[0]
}

// xdgConfigHome returns $XDG_CONFIG_HOME, falling back to ~/.config.
func xdgConfigHome() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(userHomeDir(), ".config")
}

// resolveIdentity builds an Identity from Viper config, falling back to
// auto-detection for any unset fields.
func resolveIdentity(v *viper.Viper) (identity.Identity, error) {
	detected, err := identity.DetectFunc()
	if err != nil {
		return identity.Identity{}, fmt.Errorf("resolve identity: %w", err)
	}

	// Config overrides take precedence over auto-detected values. Each override
	// becomes a path component when building override-layer directories
	// (see compiler.Discover), so reject values that could escape the dotfiles
	// directory. This is defense-in-depth: overrides come only from trusted user
	// config, never an untrusted repo, but enforcing the "layer dirs live inside
	// the repo" invariant here guards against self-inflicted misconfiguration.
	if h := v.GetString("identity.hostname"); h != "" {
		if err := validateIdentityValue("identity.hostname", h); err != nil {
			return identity.Identity{}, err
		}
		detected.Hostname = h
	}
	if u := v.GetString("identity.username"); u != "" {
		if err := validateIdentityValue("identity.username", u); err != nil {
			return identity.Identity{}, err
		}
		detected.Username = u
	}
	if o := v.GetString("identity.os"); o != "" {
		if err := validateIdentityValue("identity.os", o); err != nil {
			return identity.Identity{}, err
		}
		detected.OS = o
	}
	return detected, nil
}

// validateIdentityValue rejects identity-override values that would escape the
// dotfiles directory when used as an override-layer path component. A value
// containing a path separator ('/' or '\') or equal to ".." is rejected, since
// either could make the resolved layer directory point outside the repository.
func validateIdentityValue(key, value string) error {
	if value == ".." ||
		strings.ContainsRune(value, '/') ||
		strings.ContainsRune(value, '\\') {
		return fmt.Errorf(
			"resolve identity: %s value %q contains a path separator or %q — "+
				"identity overrides are used as directory names and must be a single path component",
			key, value, "..")
	}
	return nil
}

// expandHome replaces a leading ~ with the user's home directory.
// Handles both "~" and "~/...".
func expandHome(path string) string {
	if path == "~" {
		return userHomeDir()
	}
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	return filepath.Join(userHomeDir(), path[2:])
}

// expandHomeAll applies expandHome to each path in the slice.
func expandHomeAll(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = expandHome(p)
	}
	return out
}

// userHomeDir returns the current user's home directory or "~" on error.
var userHomeDirFunc = os.UserHomeDir

func userHomeDir() string {
	home, err := userHomeDirFunc()
	if err != nil {
		return "~"
	}
	return home
}

// coalesce returns first if non-empty, otherwise second.
func coalesce(first, second string) string {
	if first != "" {
		return first
	}
	return second
}
