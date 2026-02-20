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
	// AgeIdentity is the path to the age identity file for encryption.
	AgeIdentity string
	// Verbose enables verbose output.
	Verbose bool
	// DryRun suppresses all filesystem changes.
	DryRun bool
}

// Load reads and merges configuration from disk and the provided flags.
// Discovery order: <dotfilesDir>/.dotsmith.yml → ~/.dotsmith.yml → flags.
// Missing config files are silently ignored.
func Load(_ context.Context, flags Flags) (Config, error) {
	v := viper.New()
	setDefaults(v)

	dotfilesDir := flags.DotfilesDir
	if dotfilesDir == "" {
		dotfilesDir = expandHome(v.GetString("dotfiles_dir"))
	}

	if err := loadConfigFiles(v, flags.ConfigPath, dotfilesDir); err != nil {
		return Config{}, err
	}

	id, err := resolveIdentity(v)
	if err != nil {
		return Config{}, err
	}

	return Config{
		DotfilesDir: coalesce(dotfilesDir, expandHome(v.GetString("dotfiles_dir"))),
		CompileDir:  coalesce(flags.CompileDir, expandHome(v.GetString("compile_dir"))),
		TargetDir:   coalesce(flags.TargetDir, expandHome(v.GetString("target_dir"))),
		Identity:    id,
		AgeIdentity: coalesce(flags.AgeIdentity, expandHome(v.GetString("age.identity_file"))),
		Verbose:     flags.Verbose || v.GetBool("verbose"),
		DryRun:      flags.DryRun || v.GetBool("dry_run"),
	}, nil
}

// setDefaults configures Viper defaults.
func setDefaults(v *viper.Viper) {
	v.SetDefault("dotfiles_dir", "~/.dotfiles")
	v.SetDefault("compile_dir", "~/.dotcompiled")
	v.SetDefault("target_dir", "~")
	// age.identity_file has no default — user must configure it or pass --age-identity.
}

// loadConfigFiles adds config sources in precedence order (lowest first).
func loadConfigFiles(v *viper.Viper, explicitPath, dotfilesDir string) error {
	if explicitPath != "" {
		v.SetConfigFile(expandHome(explicitPath))
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("load config %s: %w", explicitPath, err)
		}
		return nil
	}

	// Load repo config. Skip silently if file is absent; error if present but invalid.
	for _, path := range configSearchPaths(dotfilesDir) {
		if _, statErr := os.Stat(path); statErr != nil {
			continue // file does not exist or is inaccessible
		}
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("load config %s: %w", path, err)
		}
		break
	}

	// Try ~/.dotsmith.yml on top of any repo config.
	homeConf := filepath.Join(userHomeDir(), ".dotsmith.yml")
	v.SetConfigFile(homeConf)
	_ = v.MergeInConfig() // silently ignore missing file

	return nil
}

// configSearchPaths returns the ordered list of config file paths to check.
func configSearchPaths(dotfilesDir string) []string {
	return []string{
		filepath.Join(dotfilesDir, ".dotsmith.yml"),
	}
}

// resolveIdentity builds an Identity from Viper config, falling back to
// auto-detection for any unset fields.
func resolveIdentity(v *viper.Viper) (identity.Identity, error) {
	detected, err := identity.DetectFunc()
	if err != nil {
		return identity.Identity{}, fmt.Errorf("resolve identity: %w", err)
	}

	// Config overrides take precedence over auto-detected values.
	if h := v.GetString("identity.hostname"); h != "" {
		detected.Hostname = h
	}
	if u := v.GetString("identity.username"); u != "" {
		detected.Username = u
	}
	if o := v.GetString("identity.os"); o != "" {
		detected.OS = o
	}
	return detected, nil
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
