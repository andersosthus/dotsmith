package compiler

import (
	"regexp"
	"unicode"
)

// subfilePattern matches the naming convention:
//
//	<target>.<subfile-NNN>.<ext>[.age]
//
// Groups: (1) target filename, (2) number string, (3) .ext, (4) .age or "".
var subfilePattern = regexp.MustCompile(`^(.+)\.subfile-(\d+)(\.[^.]+)(\.age)?$`)

// SubfileInfo holds the parsed components of a subfile name.
type SubfileInfo struct {
	// Target is the output filename (e.g. ".bashrc").
	Target string
	// Number is the raw digit string from the subfile name (e.g. "020").
	Number string
	// Ext is the file extension after the number (e.g. ".sh").
	Ext string
	// Encrypted is true if the subfile has an .age suffix.
	Encrypted bool
	// FullName is the original filename as parsed.
	FullName string
}

// ParseSubfileName parses a filename into SubfileInfo. Returns nil if the name
// does not match the subfile naming convention.
func ParseSubfileName(name string) *SubfileInfo {
	m := subfilePattern.FindStringSubmatch(name)
	if m == nil {
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
