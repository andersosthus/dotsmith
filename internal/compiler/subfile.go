package compiler

import (
	"regexp"
	"unicode"
)

// subfilePattern matches the naming convention:
//
//	<stem>.subfile-NNN[.<ext>][.age]
//
// Examples:
//
//	config.subfile-001.fish  → stem="config", ext=".fish",   target="config.fish"
//	.subfile-010.bashrc      → stem="",       ext=".bashrc", target=".bashrc"
//	my.subfile-001           → stem="my",     ext="",        target="my"
//
// Groups: (1) stem (may be empty), (2) number string, (3) .ext (may be empty), (4) .age or "".
// Group 3 uses a lazy optional (`??`) so that a trailing .age is captured by group 4, not group 3.
var subfilePattern = regexp.MustCompile(`^(.*?)\.subfile-(\d+)(\.[^.]+)??(\.age)?$`)

// SubfileInfo holds the parsed components of a subfile name.
type SubfileInfo struct {
	// Target is the stem portion of the filename (e.g. "config" for "config.subfile-001.fish",
	// "" for ".subfile-010.bashrc"). The compiled target path is Target + Ext.
	Target string
	// Number is the raw digit string from the subfile name (e.g. "020").
	Number string
	// Ext is the target extension (e.g. ".fish" for "config.subfile-001.fish",
	// ".bashrc" for ".subfile-010.bashrc", "" for "my.subfile-001").
	Ext string
	// Encrypted is true if the subfile has an .age suffix.
	Encrypted bool
	// FullName is the original filename as parsed.
	FullName string
}

// ParseSubfileName parses a filename into SubfileInfo. Returns nil if the name
// does not match the subfile naming convention or would produce an empty target.
func ParseSubfileName(name string) *SubfileInfo {
	m := subfilePattern.FindStringSubmatch(name)
	if m == nil {
		return nil
	}
	// Reject .subfile-NNN with no stem and no ext — would compile to an empty target.
	if m[1] == "" && m[3] == "" {
		return nil
	}
	return &SubfileInfo{
		Target:    m[1],
		Number:    m[2],
		Ext:       m[3],
		Encrypted: m[4] == ".age",
		FullName:  name,
	}
}

// NaturalLess reports whether a < b using natural (numeric-aware) sort order.
// Numeric runs within strings are compared as integers so that "subfile-2"
// sorts before "subfile-10".
func NaturalLess(a, b string) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		ra, rb := rune(a[i]), rune(b[j])
		if unicode.IsDigit(ra) && unicode.IsDigit(rb) {
			// Compare numeric runs as integers (ignoring leading zeros).
			ai, alen := parseDigitRun(a, i)
			bi, blen := parseDigitRun(b, j)
			if ai != bi {
				return ai < bi
			}
			i += alen
			j += blen
			continue
		}
		if ra != rb {
			return ra < rb
		}
		i++
		j++
	}
	return len(a) < len(b)
}

// parseDigitRun returns the integer value and byte length of the digit run
// starting at position pos in s.
func parseDigitRun(s string, pos int) (int, int) {
	n := 0
	start := pos
	for pos < len(s) && unicode.IsDigit(rune(s[pos])) {
		n = n*10 + int(s[pos]-'0')
		pos++
	}
	return n, pos - start
}
