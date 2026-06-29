package compiler

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"filippo.io/age/armor"

	"github.com/andersosthus/dotsmith/internal/encrypt"
	"github.com/andersosthus/dotsmith/internal/identity"
	"github.com/andersosthus/dotsmith/internal/state"
)

// generateKey creates a new age identity, writes it to a temp file, and resolves
// it into a candidate identity set for the compiler to consume.
func generateKey(t *testing.T) (keyPath string, set encrypt.IdentitySet) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	dir := t.TempDir()
	keyPath = filepath.Join(dir, "key.txt")
	if err = os.WriteFile(keyPath, []byte(id.String()+"\n"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	set, err = encrypt.Resolve(context.Background(), encrypt.KeySource{
		IdentityFile:         keyPath,
		IdentityFileExplicit: true,
	}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return keyPath, set
}

// encryptFile encrypts plaintext to <path>.age for the recipient in keyPath. The
// encrypt code path was removed from the encrypt package, so tests build the
// armored age ciphertext directly.
func encryptFile(t *testing.T, path string, plaintext string, keyPath string) {
	t.Helper()
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("ReadFile key: %v", err)
	}
	ids, err := age.ParseIdentities(bytes.NewReader(keyData))
	if err != nil {
		t.Fatalf("ParseIdentities: %v", err)
	}
	rec := ids[0].(*age.X25519Identity).Recipient()

	var buf bytes.Buffer
	aw := armor.NewWriter(&buf)
	w, err := age.Encrypt(aw, rec)
	if err != nil {
		t.Fatalf("age.Encrypt: %v", err)
	}
	if _, err = io.WriteString(w, plaintext); err != nil {
		t.Fatalf("write plaintext: %v", err)
	}
	if err = w.Close(); err != nil {
		t.Fatalf("close age writer: %v", err)
	}
	if err = aw.Close(); err != nil {
		t.Fatalf("close armor writer: %v", err)
	}

	if err = os.WriteFile(path+".age", buf.Bytes(), 0o600); err != nil {
		t.Fatalf("WriteFile age: %v", err)
	}
}

func TestCompile_SingleSubfile(t *testing.T) {
	root := t.TempDir()
	makeDir(t, root, "base")
	writeFile(t, filepath.Join(root, "base"), ".subfile-010.bashrc", "export PATH=/usr/bin\n")

	ctx := context.Background()
	cfg := CompileConfig{
		DotfilesDir: root,
		Identity:    identity.Identity{},
	}
	result, err := Compile(ctx, cfg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("len(Files) = %d, want 1", len(result.Files))
	}
	cf := result.Files[0]
	if cf.RelPath != ".bashrc" {
		t.Errorf("RelPath = %q, want %q", cf.RelPath, ".bashrc")
	}
}

func TestCompile_WithCommentHeaders(t *testing.T) {
	root := t.TempDir()
	makeDir(t, root, "base")
	// aliases.sh has a .sh extension, so comment headers will be inserted.
	writeFile(t, filepath.Join(root, "base"), "aliases.subfile-010.sh", "alias ll='ls -la'\n")
	writeFile(t, filepath.Join(root, "base"), "aliases.subfile-020.sh", "alias gs='git status'\n")

	ctx := context.Background()
	cfg := CompileConfig{DotfilesDir: root, Identity: identity.Identity{}}
	result, err := Compile(ctx, cfg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	var cf CompiledFile
	for _, f := range result.Files {
		if f.RelPath == "aliases.sh" {
			cf = f
			break
		}
	}
	content := string(cf.Content)
	if !strings.Contains(content, "# --- dotsmith:") {
		t.Errorf("expected comment header in output, not found; got: %q", content)
	}
	if !strings.Contains(content, "alias ll=") {
		t.Error("expected subfile-010 content in output")
	}
	if !strings.Contains(content, "alias gs=") {
		t.Error("expected subfile-020 content in output")
	}
}

func TestCompile_SubfilePreservesTargetExtension(t *testing.T) {
	// Targets with extensions use the full target name in the subfile filename,
	// e.g. config.fish.subfile-001.fish compiles to config.fish.
	root := t.TempDir()
	makeDir(t, root, "base")
	writeFile(t, filepath.Join(root, "base"), "config.subfile-001.fish", "# 001\n")
	writeFile(t, filepath.Join(root, "base"), "config.subfile-050.fish", "# 050\n")

	ctx := context.Background()
	result, err := Compile(ctx, CompileConfig{DotfilesDir: root, Identity: identity.Identity{}})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("len(Files) = %d, want 1", len(result.Files))
	}
	if result.Files[0].RelPath != "config.fish" {
		t.Errorf("RelPath = %q, want %q", result.Files[0].RelPath, "config.fish")
	}
}

func TestCompile_NoHeaderForUnknownExtension(t *testing.T) {
	root := t.TempDir()
	makeDir(t, root, "base")
	// .xyz is an unknown extension.
	writeFile(t, filepath.Join(root, "base"), "config.subfile-001.xyz", "data\n")
	writeFile(t, filepath.Join(root, "base"), "config.subfile-002.xyz", "more data\n")

	ctx := context.Background()
	cfg := CompileConfig{DotfilesDir: root, Identity: identity.Identity{}}
	result, err := Compile(ctx, cfg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	cf := result.Files[0]
	if strings.Contains(string(cf.Content), "dotsmith:") {
		t.Error("expected no comment header for unknown extension")
	}
}

func TestCompile_RegularFile_NoHeader(t *testing.T) {
	root := t.TempDir()
	makeDir(t, root, "base")
	writeFile(t, filepath.Join(root, "base"), ".vimrc", "\" regular vimrc\n")

	ctx := context.Background()
	cfg := CompileConfig{DotfilesDir: root, Identity: identity.Identity{}}
	result, err := Compile(ctx, cfg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	cf := result.Files[0]
	if strings.Contains(string(cf.Content), "dotsmith:") {
		t.Error("expected no comment header for regular file")
	}
}

func TestCompile_EncryptedSubfile(t *testing.T) {
	keyPath, set := generateKey(t)

	root := t.TempDir()
	base := makeDir(t, root, "base")
	// Create the plain file and encrypt it.
	encryptFile(t, filepath.Join(base, ".subfile-040.bashrc"), "export SECRET=hi\n", keyPath)

	ctx := context.Background()
	cfg := CompileConfig{DotfilesDir: root, Identity: identity.Identity{}, Identities: set}
	result, err := Compile(ctx, cfg)
	if err != nil {
		t.Fatalf("Compile with encrypted subfile: %v", err)
	}
	cf := result.Files[0]
	if !strings.Contains(string(cf.Content), "export SECRET=hi") {
		t.Errorf("expected decrypted content in output, got: %q", cf.Content)
	}
	if !cf.FromEncrypted {
		t.Error("FromEncrypted should be true")
	}
}

func TestCompile_DuplicateSubfileError(t *testing.T) {
	// Manually inject a FileEntry with duplicate numbers.
	entry := &FileEntry{
		Target: ".bashrc",
		Subfiles: []SubfileDesc{
			{Number: "010", SourcePath: "/fake1"},
			{Number: "010", SourcePath: "/fake2"},
		},
	}
	_, _, err := compileSubfiles(context.Background(), entry, CompileConfig{Identities: encrypt.IdentitySet{}})
	if err == nil {
		t.Fatal("expected error for duplicate subfile numbers, got nil")
	}
}

func TestCompile_DecryptionFailure(t *testing.T) {
	keyPath1, _ := generateKey(t)
	_, set2 := generateKey(t)

	root := t.TempDir()
	base := makeDir(t, root, "base")
	encryptFile(t, filepath.Join(base, ".subfile-010.bashrc"), "secret\n", keyPath1)

	ctx := context.Background()
	cfg := CompileConfig{DotfilesDir: root, Identity: identity.Identity{}, Identities: set2}
	_, err := Compile(ctx, cfg)
	if err == nil {
		t.Fatal("expected error decrypting with wrong key, got nil")
	}
}

func TestCompile_ReadError(t *testing.T) {
	root := t.TempDir()
	base := makeDir(t, root, "base")
	path := filepath.Join(base, ".subfile-010.bashrc")
	if err := os.WriteFile(path, []byte("data"), 0o000); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ctx := context.Background()
	cfg := CompileConfig{DotfilesDir: root, Identity: identity.Identity{}}
	_, err := Compile(ctx, cfg)
	if err == nil {
		t.Fatal("expected error reading unreadable file, got nil")
	}
}

func TestWriteCompiled_IdempotentSameContent(t *testing.T) {
	root := t.TempDir()
	makeDir(t, root, "base")
	writeFile(t, filepath.Join(root, "base"), ".subfile-010.bashrc", "export A=1\n")

	ctx := context.Background()
	cfg := CompileConfig{DotfilesDir: root, Identity: identity.Identity{}}
	compileDir := t.TempDir()
	wcfg := WriteConfig{CompileDir: compileDir}

	result, err := Compile(ctx, cfg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	stats1, err := WriteCompiled(ctx, result, wcfg)
	if err != nil {
		t.Fatalf("WriteCompiled #1: %v", err)
	}
	if stats1.Written != 1 {
		t.Errorf("first write: Written = %d, want 1", stats1.Written)
	}

	// Second write — content unchanged.
	stats2, err := WriteCompiled(ctx, result, wcfg)
	if err != nil {
		t.Fatalf("WriteCompiled #2: %v", err)
	}
	if stats2.Written != 0 {
		t.Errorf("second write: Written = %d, want 0", stats2.Written)
	}
	if stats2.Unchanged != 1 {
		t.Errorf("second write: Unchanged = %d, want 1", stats2.Unchanged)
	}
}

func TestWriteCompiled_Permissions(t *testing.T) {
	keyPath, set := generateKey(t)

	root := t.TempDir()
	base := makeDir(t, root, "base")
	writeFile(t, filepath.Join(root, "base"), ".subfile-010.bashrc", "export A=1\n")
	encryptFile(t, filepath.Join(base, ".subfile-001.secret"), "secret\n", keyPath)

	ctx := context.Background()
	cfg := CompileConfig{DotfilesDir: root, Identity: identity.Identity{}, Identities: set}
	compileDir := t.TempDir()
	// Set compileDir permissions.
	if err := os.Chmod(compileDir, 0o700); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	result, err := Compile(ctx, cfg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	_, err = WriteCompiled(ctx, result, WriteConfig{CompileDir: compileDir})
	if err != nil {
		t.Fatalf("WriteCompiled: %v", err)
	}

	for _, cf := range result.Files {
		info, statErr := os.Stat(filepath.Join(compileDir, cf.RelPath))
		if statErr != nil {
			t.Fatalf("Stat %s: %v", cf.RelPath, statErr)
		}
		want := os.FileMode(0o644)
		if cf.FromEncrypted {
			want = 0o600
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s permissions = %o, want %o", cf.RelPath, info.Mode().Perm(), want)
		}
	}
}

func TestWriteCompiled_DryRun(t *testing.T) {
	root := t.TempDir()
	makeDir(t, root, "base")
	writeFile(t, filepath.Join(root, "base"), ".subfile-010.bashrc", "export A=1\n")

	ctx := context.Background()
	cfg := CompileConfig{DotfilesDir: root, Identity: identity.Identity{}}
	compileDir := t.TempDir()

	result, err := Compile(ctx, cfg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	stats, err := WriteCompiled(ctx, result, WriteConfig{CompileDir: compileDir, DryRun: true})
	if err != nil {
		t.Fatalf("WriteCompiled dry-run: %v", err)
	}
	_ = stats

	// Nothing should be written in dry-run.
	entries, err := os.ReadDir(compileDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no files in compileDir in dry-run, got %d", len(entries))
	}
}

func TestWriteCompiled_CompileDirPermissions(t *testing.T) {
	root := t.TempDir()
	makeDir(t, root, "base")
	writeFile(t, filepath.Join(root, "base"), ".subfile-010.bashrc", "data\n")

	ctx := context.Background()
	result, err := Compile(ctx, CompileConfig{DotfilesDir: root, Identity: identity.Identity{}})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	compileDir := filepath.Join(t.TempDir(), "compiled")
	if _, err = WriteCompiled(ctx, result, WriteConfig{CompileDir: compileDir}); err != nil {
		t.Fatalf("WriteCompiled: %v", err)
	}

	info, err := os.Stat(compileDir)
	if err != nil {
		t.Fatalf("Stat compileDir: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("compileDir permissions = %o, want 0700", info.Mode().Perm())
	}
}

func TestWriteCompiled_MkdirError(t *testing.T) {
	root := t.TempDir()
	makeDir(t, root, "base")
	writeFile(t, filepath.Join(root, "base"), ".subfile-010.bashrc", "data\n")

	ctx := context.Background()
	result, err := Compile(ctx, CompileConfig{DotfilesDir: root, Identity: identity.Identity{}})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// Use an unwritable parent dir.
	parent := t.TempDir()
	if err = os.Chmod(parent, 0o555); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	compileDir := filepath.Join(parent, "compiled")
	_, err = WriteCompiled(ctx, result, WriteConfig{CompileDir: compileDir})
	if err == nil {
		t.Fatal("expected error creating compile dir in unwritable parent, got nil")
	}
}

func TestCompile_EncryptedRegularFile(t *testing.T) {
	keyPath, set := generateKey(t)

	root := t.TempDir()
	base := makeDir(t, root, "base")
	encryptFile(t, filepath.Join(base, ".secret"), "top secret\n", keyPath)

	ctx := context.Background()
	cfg := CompileConfig{DotfilesDir: root, Identity: identity.Identity{}, Identities: set}
	result, err := Compile(ctx, cfg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("len(Files) = %d, want 1", len(result.Files))
	}
	cf := result.Files[0]
	if cf.RelPath != ".secret" {
		t.Errorf("RelPath = %q, want %q", cf.RelPath, ".secret")
	}
	if !strings.Contains(string(cf.Content), "top secret") {
		t.Errorf("expected decrypted content, got: %q", cf.Content)
	}
}

func TestCompile_RegularReadError(t *testing.T) {
	root := t.TempDir()
	base := makeDir(t, root, "base")
	path := filepath.Join(base, ".vimrc")
	if err := os.WriteFile(path, []byte("data"), 0o000); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ctx := context.Background()
	_, err := Compile(ctx, CompileConfig{DotfilesDir: root, Identity: identity.Identity{}})
	if err == nil {
		t.Fatal("expected error reading unreadable regular file, got nil")
	}
}

func TestCompile_EncryptedRegularDecryptError(t *testing.T) {
	keyPath1, _ := generateKey(t)
	_, set2 := generateKey(t)

	root := t.TempDir()
	base := makeDir(t, root, "base")
	encryptFile(t, filepath.Join(base, ".secret"), "data\n", keyPath1)

	ctx := context.Background()
	_, err := Compile(ctx, CompileConfig{DotfilesDir: root, Identity: identity.Identity{}, Identities: set2})
	if err == nil {
		t.Fatal("expected error decrypting regular file with wrong key, got nil")
	}
}

func TestCompile_EmptyRegularEntry(t *testing.T) {
	// Edge case: a FileEntry with IsRegular=true but no subfiles.
	entry := &FileEntry{Target: "empty", IsRegular: true, Subfiles: nil}
	_, _, err := compileRegular(context.Background(), entry, CompileConfig{Identities: encrypt.IdentitySet{}})
	if err == nil {
		t.Fatal("expected error for empty regular entry, got nil")
	}
}

func TestCompile_DiscoverError(t *testing.T) {
	root := t.TempDir()
	base := makeDir(t, root, "base")
	locked := makeDir(t, base, "locked")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	ctx := context.Background()
	_, err := Compile(ctx, CompileConfig{DotfilesDir: root, Identity: identity.Identity{}})
	if err == nil {
		t.Fatal("expected error walking locked directory, got nil")
	}
}

func TestWriteCompiled_NestedMkdirError(t *testing.T) {
	root := t.TempDir()
	base := makeDir(t, root, "base", ".config", "git")
	writeFile(t, base, "config", "[core]\n\tautocrlf = false\n")

	ctx := context.Background()
	result, err := Compile(ctx, CompileConfig{DotfilesDir: root, Identity: identity.Identity{}})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	compileDir := t.TempDir()
	// Block creation of .config/git/ by placing a regular file at .config.
	if err = os.WriteFile(filepath.Join(compileDir, ".config"), []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err = WriteCompiled(ctx, result, WriteConfig{CompileDir: compileDir})
	if err == nil {
		t.Fatal("expected error creating nested compile dir, got nil")
	}
}

func TestWriteCompiled_WriteError(t *testing.T) {
	root := t.TempDir()
	base := makeDir(t, root, "base", "sub")
	writeFile(t, base, ".subfile-010.bashrc", "export A=1\n")

	ctx := context.Background()
	result, err := Compile(ctx, CompileConfig{DotfilesDir: root, Identity: identity.Identity{}})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// Pre-create the file's parent directory inside the (writable) compile dir at
	// a read-only mode so the per-file write fails. The compile dir itself stays
	// writable so the WriteCompiled chmod of the compile dir succeeds.
	compileDir := t.TempDir()
	subDir := filepath.Join(compileDir, "sub")
	if err = os.Mkdir(subDir, 0o555); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(subDir, 0o755) })

	_, err = WriteCompiled(ctx, result, WriteConfig{CompileDir: compileDir})
	if err == nil {
		t.Fatal("expected error writing into read-only subdir, got nil")
	}
}

// encryptedCompileResult builds a CompileResult with a single FromEncrypted
// file at relPath whose content is the given bytes.
func encryptedCompileResult(relPath string, content []byte) *CompileResult {
	return &CompileResult{
		Files: []CompiledFile{{
			RelPath:       relPath,
			Content:       content,
			ContentHash:   hashContent(content),
			FromEncrypted: true,
		}},
	}
}

// TestWriteCompiled_EncryptedRepairsExistingMode asserts a compiled file derived
// from an encrypted source ends up 0600 across all three write paths even when
// the destination already exists at a looser 0644 mode.
func TestWriteCompiled_EncryptedRepairsExistingMode(t *testing.T) {
	ctx := context.Background()
	const relPath = ".secret"

	t.Run("fresh write over pre-existing 0644", func(t *testing.T) {
		compileDir := t.TempDir()
		destPath := filepath.Join(compileDir, relPath)
		// Pre-create the destination at a loose mode with different content so
		// the write path (not the idempotency path) runs.
		if err := os.WriteFile(destPath, []byte("old plaintext\n"), 0o644); err != nil {
			t.Fatalf("seed dest: %v", err)
		}

		result := encryptedCompileResult(relPath, []byte("decrypted secret\n"))
		stats, err := WriteCompiled(ctx, result, WriteConfig{CompileDir: compileDir})
		if err != nil {
			t.Fatalf("WriteCompiled: %v", err)
		}
		if stats.Written != 1 {
			t.Errorf("Written = %d, want 1", stats.Written)
		}
		assertMode(t, destPath, 0o600)
	})

	t.Run("content-changed re-write over 0644", func(t *testing.T) {
		compileDir := t.TempDir()
		destPath := filepath.Join(compileDir, relPath)
		if err := os.WriteFile(destPath, []byte("v1\n"), 0o644); err != nil {
			t.Fatalf("seed dest: %v", err)
		}

		result := encryptedCompileResult(relPath, []byte("v2\n"))
		if _, err := WriteCompiled(ctx, result, WriteConfig{CompileDir: compileDir}); err != nil {
			t.Fatalf("WriteCompiled: %v", err)
		}
		assertMode(t, destPath, 0o600)
	})

	t.Run("content-unchanged idempotent run over 0644", func(t *testing.T) {
		compileDir := t.TempDir()
		destPath := filepath.Join(compileDir, relPath)
		content := []byte("same secret\n")
		// Pre-create with matching content but a loose mode; this exercises the
		// idempotency early-return, which must still repair the mode.
		if err := os.WriteFile(destPath, content, 0o644); err != nil {
			t.Fatalf("seed dest: %v", err)
		}

		result := encryptedCompileResult(relPath, content)
		stats, err := WriteCompiled(ctx, result, WriteConfig{CompileDir: compileDir})
		if err != nil {
			t.Fatalf("WriteCompiled: %v", err)
		}
		if stats.Unchanged != 1 {
			t.Errorf("Unchanged = %d, want 1", stats.Unchanged)
		}
		assertMode(t, destPath, 0o600)
	})
}

// TestWriteCompiled_TightensLooseCompileDir asserts a pre-existing, loosely
// permissioned compile dir is brought back to 0700.
func TestWriteCompiled_TightensLooseCompileDir(t *testing.T) {
	ctx := context.Background()
	compileDir := t.TempDir()
	if err := os.Chmod(compileDir, 0o755); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	result := encryptedCompileResult(".secret", []byte("secret\n"))
	if _, err := WriteCompiled(ctx, result, WriteConfig{CompileDir: compileDir}); err != nil {
		t.Fatalf("WriteCompiled: %v", err)
	}
	assertMode(t, compileDir, 0o700)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat %s: %v", path, err)
	}
	if info.Mode().Perm() != want {
		t.Errorf("%s mode = %o, want %o", path, info.Mode().Perm(), want)
	}
}

func TestCompile_PopulatesSourceSignature(t *testing.T) {
	root := t.TempDir()
	base := makeDir(t, root, "base")
	writeFile(t, base, "aliases.subfile-010.sh", "alias ll='ls -la'\n")
	writeFile(t, base, "aliases.subfile-020.sh", "alias gs='git status'\n")
	writeFile(t, base, ".vimrc", "\" regular\n")

	ctx := context.Background()
	cfg := CompileConfig{DotfilesDir: root, Identity: identity.Identity{}}
	result, err := Compile(ctx, cfg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(result.Files) == 0 {
		t.Fatal("expected compiled files")
	}
	for _, cf := range result.Files {
		if cf.SourceSignature == "" {
			t.Errorf("file %q has an empty SourceSignature", cf.RelPath)
		}
	}
}

func TestCompile_RecordsSignatureInManifest(t *testing.T) {
	root := t.TempDir()
	base := makeDir(t, root, "base")
	writeFile(t, base, "config.subfile-001.fish", "# 001\n")
	writeFile(t, base, "config.subfile-050.fish", "# 050\n")

	ctx := context.Background()
	cfg := CompileConfig{DotfilesDir: root, Identity: identity.Identity{}}
	compileDir := t.TempDir()

	result, err := Compile(ctx, cfg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, err = WriteCompiled(ctx, result, WriteConfig{CompileDir: compileDir}); err != nil {
		t.Fatalf("WriteCompiled: %v", err)
	}

	s, err := state.Load(ctx, compileDir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	for _, cf := range result.Files {
		entry, ok := s.Compiled[cf.RelPath]
		if !ok {
			t.Errorf("manifest missing entry for %q", cf.RelPath)
			continue
		}
		if entry.SourceSignature == "" {
			t.Errorf("manifest entry %q has an empty SourceSignature", cf.RelPath)
		}
		if entry.SourceSignature != cf.SourceSignature {
			t.Errorf("manifest signature for %q = %q, want %q",
				cf.RelPath, entry.SourceSignature, cf.SourceSignature)
		}
	}
}

func TestCompile_SignatureMovesAcrossRuns(t *testing.T) {
	root := t.TempDir()
	base := makeDir(t, root, "base")
	p := writeFile(t, base, ".subfile-010.bashrc", "export A=1\n")

	ctx := context.Background()
	cfg := CompileConfig{DotfilesDir: root, Identity: identity.Identity{}}

	first, err := Compile(ctx, cfg)
	if err != nil {
		t.Fatalf("Compile #1: %v", err)
	}

	if err = os.WriteFile(p, []byte("export A=2\n"), 0o644); err != nil {
		t.Fatalf("rewrite source: %v", err)
	}
	second, err := Compile(ctx, cfg)
	if err != nil {
		t.Fatalf("Compile #2: %v", err)
	}

	if first.Files[0].SourceSignature == second.Files[0].SourceSignature {
		t.Error("signature should move after a source content change")
	}
}

func TestHashContent(t *testing.T) {
	h1 := hashContent([]byte("hello"))
	h2 := hashContent([]byte("hello"))
	h3 := hashContent([]byte("world"))

	if h1 != h2 {
		t.Error("same content should produce same hash")
	}
	if h1 == h3 {
		t.Error("different content should produce different hash")
	}
	if len(h1) != 32 {
		t.Errorf("hash length = %d, want 32 (hex XXH3-128)", len(h1))
	}
}
