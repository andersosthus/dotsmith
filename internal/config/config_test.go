package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andersosthus/dotsmith/internal/identity"
)

func TestLoad_Defaults(t *testing.T) {
	ctx := context.Background()
	// Use a non-existent dotfiles dir so no config file is found.
	c, err := Load(ctx, Flags{DotfilesDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	home, _ := os.UserHomeDir()
	if c.CompileDir != filepath.Join(home, ".dotcompiled") {
		t.Errorf("CompileDir = %q, want ~/.dotcompiled", c.CompileDir)
	}
	if c.TargetDir != home {
		t.Errorf("TargetDir = %q, want ~", c.TargetDir)
	}
	if c.DotfilesDir == "" {
		t.Error("DotfilesDir should not be empty")
	}
	if c.Identity.OS == "" {
		t.Error("Identity.OS should be auto-detected")
	}
}

func TestLoad_ConfigFileOverridesDefaults(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	content := `compile_dir: /tmp/mycompile
target_dir: /tmp/mytarget
`
	if err := os.WriteFile(filepath.Join(dir, ".dotsmith.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	c, err := Load(ctx, Flags{DotfilesDir: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.CompileDir != "/tmp/mycompile" {
		t.Errorf("CompileDir = %q, want %q", c.CompileDir, "/tmp/mycompile")
	}
	if c.TargetDir != "/tmp/mytarget" {
		t.Errorf("TargetDir = %q, want %q", c.TargetDir, "/tmp/mytarget")
	}
}

func TestLoad_FlagsOverrideConfig(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	c, err := Load(ctx, Flags{
		DotfilesDir: dir,
		Verbose:     true,
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.Verbose {
		t.Error("Verbose flag should be respected")
	}
	if !c.DryRun {
		t.Error("DryRun flag should be respected")
	}
}

func TestLoad_DotfilesDirFromFlag(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	c, err := Load(ctx, Flags{DotfilesDir: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.DotfilesDir != dir {
		t.Errorf("DotfilesDir = %q, want %q", c.DotfilesDir, dir)
	}
}

func TestLoad_TildeExpansion(t *testing.T) {
	ctx := context.Background()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir:", err)
	}

	dir := t.TempDir()
	content := "compile_dir: ~/mycompile\ntarget_dir: ~/mytarget\n"
	if err := os.WriteFile(filepath.Join(dir, ".dotsmith.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	c, err := Load(ctx, Flags{DotfilesDir: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.CompileDir != filepath.Join(home, "mycompile") {
		t.Errorf("CompileDir = %q, want %q", c.CompileDir, filepath.Join(home, "mycompile"))
	}
	if c.TargetDir != filepath.Join(home, "mytarget") {
		t.Errorf("TargetDir = %q, want %q", c.TargetDir, filepath.Join(home, "mytarget"))
	}
}

func TestLoad_IdentityOverridesFromConfig(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	content := `identity:
  hostname: myhost
  username: myuser
  os: myos
`
	if err := os.WriteFile(filepath.Join(dir, ".dotsmith.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	c, err := Load(ctx, Flags{DotfilesDir: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Identity.Hostname != "myhost" {
		t.Errorf("Identity.Hostname = %q, want %q", c.Identity.Hostname, "myhost")
	}
	if c.Identity.Username != "myuser" {
		t.Errorf("Identity.Username = %q, want %q", c.Identity.Username, "myuser")
	}
	if c.Identity.OS != "myos" {
		t.Errorf("Identity.OS = %q, want %q", c.Identity.OS, "myos")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// explicit config path with bad YAML
	badConf := filepath.Join(dir, "bad.yml")
	if err := os.WriteFile(badConf, []byte("{{{{invalid yaml"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load(ctx, Flags{ConfigPath: badConf, DotfilesDir: dir})
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestLoad_InvalidYAML_InDotfilesDir(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Bad YAML in the dotfiles repo config (not an explicit path).
	if err := os.WriteFile(filepath.Join(dir, ".dotsmith.yml"), []byte("not: valid: yaml: {"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load(ctx, Flags{DotfilesDir: dir})
	if err == nil {
		t.Fatal("expected error for invalid YAML in dotfiles dir, got nil")
	}
}

func TestLoad_FlagsDirOverrides(t *testing.T) {
	ctx := context.Background()
	c, err := Load(ctx, Flags{
		DotfilesDir: t.TempDir(),
		CompileDir:  "/flag/compile",
		TargetDir:   "/flag/target",
		AgeIdentity: "/flag/age.key",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.CompileDir != "/flag/compile" {
		t.Errorf("CompileDir = %q, want /flag/compile", c.CompileDir)
	}
	if c.TargetDir != "/flag/target" {
		t.Errorf("TargetDir = %q, want /flag/target", c.TargetDir)
	}
	if c.AgeIdentity != "/flag/age.key" {
		t.Errorf("AgeIdentity = %q, want /flag/age.key", c.AgeIdentity)
	}
}

func TestLoad_ExplicitConfigPath(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	conf := filepath.Join(dir, "my.yml")
	content := "compile_dir: /explicit/compile\n"
	if err := os.WriteFile(conf, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	c, err := Load(ctx, Flags{ConfigPath: conf, DotfilesDir: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.CompileDir != "/explicit/compile" {
		t.Errorf("CompileDir = %q, want %q", c.CompileDir, "/explicit/compile")
	}
}

func TestLoad_DefaultDotfilesDirFromConfig(t *testing.T) {
	ctx := context.Background()
	// Don't pass DotfilesDir — config should use the default.
	c, err := Load(ctx, Flags{})
	if err != nil {
		t.Fatalf("Load with empty flags: %v", err)
	}
	if c.DotfilesDir == "" {
		t.Error("DotfilesDir should not be empty when using defaults")
	}
}

func TestLoad_DetectError(t *testing.T) {
	// Inject a failing identity detector.
	origDetect := identity.DetectFunc
	t.Cleanup(func() { identity.DetectFunc = origDetect })
	identity.DetectFunc = func() (identity.Identity, error) {
		return identity.Identity{}, errors.New("injected detect error")
	}

	ctx := context.Background()
	_, err := Load(ctx, Flags{DotfilesDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error from identity detect failure, got nil")
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}

	tests := []struct {
		input string
		want  string
	}{
		{"~", home},
		{"~/foo", filepath.Join(home, "foo")},
		{"/abs/path", "/abs/path"},
		{"relative", "relative"},
		{"~notexpanded", "~notexpanded"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := expandHome(tc.input)
			if got != tc.want {
				t.Errorf("expandHome(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestUserHomeDir_Error(t *testing.T) {
	orig := userHomeDirFunc
	t.Cleanup(func() { userHomeDirFunc = orig })
	userHomeDirFunc = func() (string, error) { return "", errors.New("no home") }

	got := userHomeDir()
	if got != "~" {
		t.Errorf("userHomeDir() with error = %q, want %q", got, "~")
	}
}

func TestExpandHome_UserHomeDirError(t *testing.T) {
	orig := userHomeDirFunc
	t.Cleanup(func() { userHomeDirFunc = orig })
	userHomeDirFunc = func() (string, error) { return "", errors.New("no home") }

	got := expandHome("~/foo")
	// Should fall back to "~/foo" because userHomeDir returns "~".
	if !strings.HasPrefix(got, "~/") && got != filepath.Join("~", "foo") {
		t.Errorf("expandHome with error = %q", got)
	}
}

func TestCoalesce(t *testing.T) {
	if coalesce("a", "b") != "a" {
		t.Error("coalesce should return first when non-empty")
	}
	if coalesce("", "b") != "b" {
		t.Error("coalesce should return second when first is empty")
	}
	if coalesce("", "") != "" {
		t.Error("coalesce of two empty strings should return empty")
	}
}
