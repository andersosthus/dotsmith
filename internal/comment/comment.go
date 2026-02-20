// Package comment maps file extensions to comment styles and formats
// provenance headers inserted during subfile assembly.
package comment

import "fmt"

// Style describes the prefix and optional suffix for a line comment in a
// particular language.
type Style struct {
	Prefix string
	Suffix string
}

// extensionStyles maps file extensions (without leading dot) to their comment
// Style.
var extensionStyles = map[string]*Style{
	// Shell, Python, Ruby, Perl, config formats — hash-style
	"sh":   {Prefix: "#"},
	"bash": {Prefix: "#"},
	"zsh":  {Prefix: "#"},
	"fish": {Prefix: "#"},
	"py":   {Prefix: "#"},
	"rb":   {Prefix: "#"},
	"pl":   {Prefix: "#"},
	"yml":  {Prefix: "#"},
	"yaml": {Prefix: "#"},
	"toml": {Prefix: "#"},
	"conf": {Prefix: "#"},
	"cfg":  {Prefix: "#"},
	"ini":  {Prefix: "#"},

	// C-family, Go, JS/TS, CSS — double-slash
	"js":   {Prefix: "//"},
	"ts":   {Prefix: "//"},
	"go":   {Prefix: "//"},
	"c":    {Prefix: "//"},
	"cpp":  {Prefix: "//"},
	"java": {Prefix: "//"},
	"rs":   {Prefix: "//"},
	"css":  {Prefix: "//"},
	"scss": {Prefix: "//"},

	// Lua, SQL — double-dash
	"lua": {Prefix: "--"},
	"sql": {Prefix: "--"},

	// Vim — double-quote
	"vim": {Prefix: `"`},

	// Emacs Lisp — double-semicolon
	"el":   {Prefix: ";;"},
	"lisp": {Prefix: ";;"},

	// HTML, XML, SVG — XML comment
	"html": {Prefix: "<!--", Suffix: "-->"},
	"xml":  {Prefix: "<!--", Suffix: "-->"},
	"svg":  {Prefix: "<!--", Suffix: "-->"},
}

// ForExtension returns the comment Style for the given file extension (without
// leading dot), or nil if the extension has no known comment syntax.
func ForExtension(ext string) *Style {
	return extensionStyles[ext]
}

// Header formats a provenance comment for a subfile.
//
// The comment uses the given style and includes the source name (e.g.
// ".bashrc.subfile-020.sh") and the layer from which it originated (e.g.
// "hostname/workstation"). Returns an empty string if style is nil.
func Header(style *Style, sourceName, layer string) string {
	if style == nil {
		return ""
	}
	inner := fmt.Sprintf("--- dotsmith: %s (%s) ---", sourceName, layer)
	if style.Suffix != "" {
		return style.Prefix + " " + inner + " " + style.Suffix + "\n"
	}
	return style.Prefix + " " + inner + "\n"
}
