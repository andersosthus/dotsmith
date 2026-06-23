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

// TestMain isolates the user config environment so the developer's real
// ~/.config/dotsmith/config.yml or ~/.dotsmith.yml cannot leak into tests.
// Individual tests override XDG_CONFIG_HOME/HOME with t.Setenv as needed.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "dotsmith-config-test-home")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("HOME", dir)
	_ = os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// writeConfig writes YAML to path, creating parent directories.
func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

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
	if c.AgeIdentity != filepath.Join(home, ".dotsmith-age-key") {
		t.Errorf("AgeIdentity = %q, want ~/.dotsmith-age-key", c.AgeIdentity)
	}
}

func TestLoad_ConfigFileOverridesDefaults(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	writeConfig(t, UserConfigPath(), "compile_dir: /tmp/mycompile\ntarget_dir: /tmp/mytarget\n")

	c, err := Load(ctx, Flags{DotfilesDir: t.TempDir()})
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
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir:", err)
	}

	writeConfig(t, UserConfigPath(), "compile_dir: ~/mycompile\ntarget_dir: ~/mytarget\n")

	c, err := Load(ctx, Flags{DotfilesDir: t.TempDir()})
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
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	writeConfig(t, UserConfigPath(), "identity:\n  hostname: myhost\n  username: myuser\n  os: myos\n")

	c, err := Load(ctx, Flags{DotfilesDir: t.TempDir()})
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

func TestLoad_RepoConfigIgnored(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir:", err)
	}
	dir := t.TempDir()

	// A repo-local .dotsmith.yml must be ignored entirely: an untrusted dotfiles
	// repo cannot redirect the age identity, compile/target dirs, or identity.
	repoCfg := `compile_dir: /evil/compile
target_dir: /evil/target
age:
  identity_file: /evil/key
identity:
  hostname: evilhost
`
	if err = os.WriteFile(filepath.Join(dir, ".dotsmith.yml"), []byte(repoCfg), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	c, err := Load(ctx, Flags{DotfilesDir: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.CompileDir != filepath.Join(home, ".dotcompiled") {
		t.Errorf("CompileDir = %q, want default (repo config must be ignored)", c.CompileDir)
	}
	if c.TargetDir != home {
		t.Errorf("TargetDir = %q, want default (repo config must be ignored)", c.TargetDir)
	}
	if c.AgeIdentity != filepath.Join(home, ".dotsmith-age-key") {
		t.Errorf("AgeIdentity = %q, want default (repo config must be ignored)", c.AgeIdentity)
	}
	if c.Identity.Hostname == "evilhost" {
		t.Error("Identity.Hostname picked up repo config — must be ignored")
	}
}

func TestLoad_InvalidYAML_InDotfilesDirIgnored(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()

	// Bad YAML in the repo config must be ignored (never read), not an error.
	if err := os.WriteFile(filepath.Join(dir, ".dotsmith.yml"), []byte("not: valid: yaml: {"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := Load(ctx, Flags{DotfilesDir: dir}); err != nil {
		t.Fatalf("Load should ignore repo config, got error: %v", err)
	}
}

func TestLoad_InvalidYAML_InUserConfig(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	writeConfig(t, UserConfigPath(), "not: valid: yaml: {")

	if _, err := Load(ctx, Flags{DotfilesDir: t.TempDir()}); err == nil {
		t.Fatal("expected error for invalid YAML in user config, got nil")
	}
}

func TestLoad_XDGWinsOverHome(t *testing.T) {
	ctx := context.Background()
	xdg := t.TempDir()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("HOME", home)

	writeConfig(t, filepath.Join(xdg, "dotsmith", "config.yml"), "compile_dir: /from/xdg\n")
	writeConfig(t, filepath.Join(home, ".dotsmith.yml"), "compile_dir: /from/home\n")

	c, err := Load(ctx, Flags{DotfilesDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.CompileDir != "/from/xdg" {
		t.Errorf("CompileDir = %q, want /from/xdg (XDG must win over ~)", c.CompileDir)
	}
}

func TestLoad_HomeFallback(t *testing.T) {
	ctx := context.Background()
	xdg := t.TempDir() // exists but contains no dotsmith config
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("HOME", home)

	writeConfig(t, filepath.Join(home, ".dotsmith.yml"), "compile_dir: /from/home\n")

	c, err := Load(ctx, Flags{DotfilesDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.CompileDir != "/from/home" {
		t.Errorf("CompileDir = %q, want /from/home (~ fallback)", c.CompileDir)
	}
}

func TestLoad_XDGConfigHomeDefaultsToDotConfig(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "") // force the ~/.config fallback

	writeConfig(t, filepath.Join(home, ".config", "dotsmith", "config.yml"), "compile_dir: /from/default-xdg\n")

	c, err := Load(ctx, Flags{DotfilesDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.CompileDir != "/from/default-xdg" {
		t.Errorf("CompileDir = %q, want /from/default-xdg (XDG_CONFIG_HOME unset → ~/.config)", c.CompileDir)
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
