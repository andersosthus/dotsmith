package comment

import (
	"testing"
)

func TestForExtension(t *testing.T) {
	tests := []struct {
		ext        string
		wantNil    bool
		wantPrefix string
		wantSuffix string
	}{
		// Hash-style
		{"sh", false, "#", ""},
		{"bash", false, "#", ""},
		{"zsh", false, "#", ""},
		{"fish", false, "#", ""},
		{"py", false, "#", ""},
		{"rb", false, "#", ""},
		{"pl", false, "#", ""},
		{"yml", false, "#", ""},
		{"yaml", false, "#", ""},
		{"toml", false, "#", ""},
		{"conf", false, "#", ""},
		{"cfg", false, "#", ""},
		{"ini", false, "#", ""},

		// Double-slash
		{"js", false, "//", ""},
		{"ts", false, "//", ""},
		{"go", false, "//", ""},
		{"c", false, "//", ""},
		{"cpp", false, "//", ""},
		{"java", false, "//", ""},
		{"rs", false, "//", ""},
		{"css", false, "//", ""},
		{"scss", false, "//", ""},

		// Double-dash
		{"lua", false, "--", ""},
		{"sql", false, "--", ""},

		// Vim
		{"vim", false, `"`, ""},

		// Lisp
		{"el", false, ";;", ""},
		{"lisp", false, ";;", ""},

		// XML-style
		{"html", false, "<!--", "-->"},
		{"xml", false, "<!--", "-->"},
		{"svg", false, "<!--", "-->"},

		// Unknown
		{"md", true, "", ""},
		{"txt", true, "", ""},
		{"", true, "", ""},
		{"json", true, "", ""},
		{"age", true, "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.ext, func(t *testing.T) {
			got := ForExtension(tc.ext)
			if tc.wantNil {
				if got != nil {
					t.Errorf("ForExtension(%q) = %v, want nil", tc.ext, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("ForExtension(%q) = nil, want non-nil", tc.ext)
			}
			if got.Prefix != tc.wantPrefix {
				t.Errorf("Prefix = %q, want %q", got.Prefix, tc.wantPrefix)
			}
			if got.Suffix != tc.wantSuffix {
				t.Errorf("Suffix = %q, want %q", got.Suffix, tc.wantSuffix)
			}
		})
	}
}

func TestHeader(t *testing.T) {
	tests := []struct {
		name       string
		style      *Style
		sourceName string
		layer      string
		want       string
	}{
		{
			name:       "nil style returns empty",
			style:      nil,
			sourceName: ".bashrc.subfile-010.sh",
			layer:      "base",
			want:       "",
		},
		{
			name:       "hash style",
			style:      &Style{Prefix: "#"},
			sourceName: ".bashrc.subfile-020.sh",
			layer:      "hostname/workstation",
			want:       "# --- dotsmith: .bashrc.subfile-020.sh (hostname/workstation) ---\n",
		},
		{
			name:       "double-slash style",
			style:      &Style{Prefix: "//"},
			sourceName: "init.go",
			layer:      "base",
			want:       "// --- dotsmith: init.go (base) ---\n",
		},
		{
			name:       "double-dash style",
			style:      &Style{Prefix: "--"},
			sourceName: "schema.sql",
			layer:      "os/linux",
			want:       "-- --- dotsmith: schema.sql (os/linux) ---\n",
		},
		{
			name:       "vim style",
			style:      &Style{Prefix: `"`},
			sourceName: ".vimrc.subfile-001.vim",
			layer:      "base",
			want:       `"` + " --- dotsmith: .vimrc.subfile-001.vim (base) ---\n",
		},
		{
			name:       "lisp style",
			style:      &Style{Prefix: ";;"},
			sourceName: "init.el",
			layer:      "username/anders",
			want:       ";; --- dotsmith: init.el (username/anders) ---\n",
		},
		{
			name:       "xml style with suffix",
			style:      &Style{Prefix: "<!--", Suffix: "-->"},
			sourceName: "index.html",
			layer:      "base",
			want:       "<!-- --- dotsmith: index.html (base) --- -->\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Header(tc.style, tc.sourceName, tc.layer)
			if got != tc.want {
				t.Errorf("Header() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHeader_PRDExample(t *testing.T) {
	// Exact examples from the PRD.
	style := ForExtension("sh")
	got := Header(style, ".bashrc.subfile-020.sh", "hostname/workstation")
	want := "# --- dotsmith: .bashrc.subfile-020.sh (hostname/workstation) ---\n"
	if got != want {
		t.Errorf("Header() = %q, want %q", got, want)
	}

	got2 := Header(style, ".bashrc.subfile-010.sh", "base")
	want2 := "# --- dotsmith: .bashrc.subfile-010.sh (base) ---\n"
	if got2 != want2 {
		t.Errorf("Header() = %q, want %q", got2, want2)
	}
}
