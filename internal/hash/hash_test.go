package hash

import (
	"regexp"
	"testing"
)

// hexDigest matches the expected 32-character lowercase hex form of an
// XXH3-128 digest.
var hexDigest = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestSumIsDeterministic(t *testing.T) {
	t.Parallel()

	inputs := [][]byte{
		nil,
		{},
		[]byte("hello"),
		[]byte("a much longer piece of content used to exercise the hasher\n"),
		make([]byte, 4096),
	}

	for _, in := range inputs {
		first := Sum(in)
		second := Sum(in)
		if first != second {
			t.Errorf("Sum is not deterministic for %q: %q != %q", in, first, second)
		}
		if !hexDigest.MatchString(first) {
			t.Errorf("Sum(%q) = %q, want 32-char lowercase hex", in, first)
		}
	}
}

func TestSumDistinctInputsDistinctDigests(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"empty":    {},
		"hello":    []byte("hello"),
		"world":    []byte("world"),
		"hello2":   []byte("hello\n"),
		"trailing": []byte("world "),
	}

	seen := make(map[string]string, len(cases))
	for name, content := range cases {
		digest := Sum(content)
		if prev, ok := seen[digest]; ok {
			t.Errorf("distinct inputs %q and %q produced the same digest %q", prev, name, digest)
		}
		seen[digest] = name
	}
}

func TestSumEmptyAndNilAgree(t *testing.T) {
	t.Parallel()

	if Sum(nil) != Sum([]byte{}) {
		t.Errorf("Sum(nil) and Sum(empty) disagree: %q != %q", Sum(nil), Sum([]byte{}))
	}
}
