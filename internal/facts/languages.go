package facts

import (
	"path"
	"strings"
)

// extensionLanguages maps a lowercase file extension (without the leading
// dot) to a lowercase language name. Unrecognised extensions are keyed
// "other" by languageOf.
var extensionLanguages = map[string]string{
	"go":  "go",
	"rs":  "rust",
	"py":  "python",
	"pyi": "python",

	"js":  "javascript",
	"jsx": "javascript",
	"mjs": "javascript",
	"cjs": "javascript",
	"ts":  "typescript",
	"tsx": "typescript",

	"java":   "java",
	"kt":     "kotlin",
	"kts":    "kotlin",
	"scala":  "scala",
	"groovy": "groovy",
	"gradle": "groovy",

	"swift": "swift",
	"m":     "objective-c",
	"mm":    "objective-c",

	"c":   "c",
	"h":   "c",
	"cc":  "cpp",
	"cpp": "cpp",
	"cxx": "cpp",
	"hpp": "cpp",
	"hxx": "cpp",
	"cs":  "csharp",

	"rb":  "ruby",
	"php": "php",

	"clj":  "clojure",
	"cljs": "clojure",
	"ex":   "elixir",
	"exs":  "elixir",
	"erl":  "erlang",
	"hrl":  "erlang",
	"hs":   "haskell",
	"lhs":  "haskell",
	"lua":  "lua",
	"pl":   "perl",
	"pm":   "perl",
	"r":    "r",
	"jl":   "julia",
	"dart": "dart",

	"sh":   "shell",
	"bash": "shell",
	"zsh":  "shell",
	"fish": "shell",
	"ps1":  "powershell",

	"sql": "sql",

	"proto":   "protobuf",
	"graphql": "graphql",
	"gql":     "graphql",

	"yaml":  "yaml",
	"yml":   "yaml",
	"json":  "json",
	"jsonc": "json",
	"toml":  "toml",
	"xml":   "xml",

	"html": "html",
	"htm":  "html",
	"css":  "css",
	"scss": "scss",
	"sass": "sass",
	"less": "less",

	"md":  "markdown",
	"mdx": "markdown",
	"rst": "restructuredtext",
	"tex": "tex",

	"vue":    "vue",
	"svelte": "svelte",

	"tf":     "terraform",
	"tfvars": "terraform",
	"nix":    "nix",
	"zig":    "zig",
	"nim":    "nim",
	"cr":     "crystal",
	"elm":    "elm",
	"fs":     "fsharp",
	"fsx":    "fsharp",
	"ml":     "ocaml",
	"mli":    "ocaml",
	"vb":     "visualbasic",
	"asm":    "assembly",
	"s":      "assembly",
	"wasm":   "webassembly",
	"sol":    "solidity",
}

// filenameLanguages maps a lowercase bare filename (no extension) to a
// language name, for files conventionally identified by name rather than
// extension.
var filenameLanguages = map[string]string{
	"dockerfile": "dockerfile",
	"makefile":   "makefile",
}

// LanguageOf returns the lowercase language name for a changed path, or
// "other" when neither its bare filename nor its extension is recognised.
func LanguageOf(p string) string {
	base := strings.ToLower(path.Base(p))
	if lang, ok := filenameLanguages[base]; ok {
		return lang
	}
	ext := strings.TrimPrefix(path.Ext(base), ".")
	if lang, ok := extensionLanguages[ext]; ok {
		return lang
	}
	return "other"
}
