package compiler

import (
	"sort"
	"testing"
)

func TestParseSubfileName(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantNil   bool
		wantTarget string
		wantNum   string
		wantExt   string
		wantEncrypted bool
	}{
		{
			name:      "simple sh subfile",
			input:     ".bashrc.subfile-010.sh",
			wantTarget: ".bashrc",
			wantNum:   "010",
			wantExt:   ".sh",
		},
		{
			name:      "encrypted subfile",
			input:     ".bashrc.subfile-030.sh.age",
			wantTarget: ".bashrc",
			wantNum:   "030",
			wantExt:   ".sh",
			wantEncrypted: true,
		},
		{
			name:      "zero-padded single digit",
			input:     ".vimrc.subfile-001.vim",
			wantTarget: ".vimrc",
			wantNum:   "001",
			wantExt:   ".vim",
		},
		{
			name:      "large number",
			input:     "config.subfile-100.toml",
			wantTarget: "config",
			wantNum:   "100",
			wantExt:   ".toml",
		},
		{
			name:      "nested path subfile",
			input:     "config.subfile-02.yaml",
			wantTarget: "config",
			wantNum:   "02",
			wantExt:   ".yaml",
		},
		{
			name:    "regular file",
			input:   ".vimrc",
			wantNil: true,
		},
		{
			name:    "no number",
			input:   ".bashrc.subfile-.sh",
			wantNil: true,
		},
		{
			name:    "missing ext after number",
			input:   ".bashrc.subfile-010",
			wantNil: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseSubfileName(tc.input)
			if tc.wantNil {
				if got != nil {
					t.Errorf("ParseSubfileName(%q) = %v, want nil", tc.input, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("ParseSubfileName(%q) = nil, want non-nil", tc.input)
			}
			assertSubfileFields(t, got, tc.input, tc.wantTarget, tc.wantNum, tc.wantExt, tc.wantEncrypted)
		})
	}
}

func assertSubfileFields(
	t *testing.T,
	got *SubfileInfo,
	input, wantTarget, wantNum, wantExt string,
	wantEncrypted bool,
) {
	t.Helper()
	if got.Target != wantTarget {
		t.Errorf("Target = %q, want %q", got.Target, wantTarget)
	}
	if got.Number != wantNum {
		t.Errorf("Number = %q, want %q", got.Number, wantNum)
	}
	if got.Ext != wantExt {
		t.Errorf("Ext = %q, want %q", got.Ext, wantExt)
	}
	if got.Encrypted != wantEncrypted {
		t.Errorf("Encrypted = %v, want %v", got.Encrypted, wantEncrypted)
	}
	if got.FullName != input {
		t.Errorf("FullName = %q, want %q", got.FullName, input)
	}
}

func TestNaturalLess(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		// Basic numeric ordering (the key correctness cases from PRD).
		{"subfile-1", "subfile-2", true},
		{"subfile-2", "subfile-10", true},
		{"subfile-10", "subfile-099", true},
		{"subfile-099", "subfile-100", true},
		// Mixed zero-padding.
		{"1", "2", true},
		{"2", "10", true},
		{"10", "099", true},
		{"099", "100", true},
		// Equal strings.
		{"abc", "abc", false},
		{"10", "10", false},
		// Lexicographic for non-numeric parts.
		{"a", "b", true},
		{"b", "a", false},
		// Numbers embedded in strings.
		{"file10.sh", "file20.sh", true},
		{"file20.sh", "file10.sh", false},
		// Length difference (no digits).
		{"abc", "abcd", true},
		{"abcd", "abc", false},
		// Leading zeros don't matter.
		{"01", "2", true},   // 1 < 2
		{"02", "10", true},  // 2 < 10
	}

	for _, tc := range tests {
		t.Run(tc.a+"<"+tc.b, func(t *testing.T) {
			got := NaturalLess(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("NaturalLess(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestNaturalSort_MixedPadding(t *testing.T) {
	// Validate the PRD requirement: 1 < 2 < 10 < 099 < 100.
	input := []string{"100", "099", "1", "10", "2"}
	want := []string{"1", "2", "10", "099", "100"}

	sort.Slice(input, func(i, j int) bool {
		return NaturalLess(input[i], input[j])
	})

	for i, got := range input {
		if got != want[i] {
			t.Errorf("sorted[%d] = %q, want %q", i, got, want[i])
		}
	}
}

func TestNaturalLess_PureDigits(t *testing.T) {
	// 099 as a number is 99, 100 is 100 — so 099 < 100.
	if !NaturalLess("099", "100") {
		t.Error("expected 099 < 100 in natural sort")
	}
	if NaturalLess("100", "099") {
		t.Error("expected NOT 100 < 099 in natural sort")
	}
}
