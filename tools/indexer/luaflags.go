package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Lua flags point the reviewer at code worth a close look. They are not
// a gate: Bururu's sandbox removes these globals anyway, and a flag
// often has a plain reason. A mod with flags is never merged without a
// human reading the lines.

// Limits of the flags.
const (
	luaFileFlagBytes = 64 << 10 // a .lua file larger than this
	luaStringFlag    = 1 << 10  // a string literal longer than this
	luaFlagsPerFile  = 20
)

var (
	// the globals Bururu's sandbox removes, which a mod has no use for
	removedGlobals = map[string]bool{"load": true, "loadstring": true, "getfenv": true, "setfenv": true,
		"debug": true, "dofile": true, "loadfile": true, "collectgarbage": true, "newproxy": true}
	// the standard tables a mod must not change with rawset
	libraryTables = map[string]bool{"string": true, "table": true, "math": true, "os": true, "io": true,
		"coroutine": true, "utf8": true, "bururu": true, "_G": true, "_ENV": true}
	escapeRunRE = regexp.MustCompile(`(\\[0-9]{1,3}){8,}`)
	base64RE    = regexp.MustCompile(`[A-Za-z0-9+/]{100,}={0,2}`)
	machineRE   = regexp.MustCompile(`^(_0x[0-9A-Fa-f]+|[Il1_]{5,}|.{48,})$`)
)

// looksBase64 reports a run of 100 or more base64 characters that mixes
// upper case, lower case and digits, as encoded data does.
func looksBase64(s string) bool {
	for _, run := range base64RE.FindAllString(s, -1) {
		if strings.ContainsAny(run, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") && strings.ContainsAny(run, "abcdefghijklmnopqrstuvwxyz") &&
			strings.ContainsAny(run, "0123456789") {
			return true
		}
	}
	return false
}

// luaFlags are the flags of one Lua file, as "<file>:<line>: <what>".
func luaFlags(file string, src []byte) []string {
	var out []string
	flag := func(line int, format string, args ...any) {
		if len(out) < luaFlagsPerFile {
			out = append(out, fmt.Sprintf("%s:%d: %s", file, line, fmt.Sprintf(format, args...)))
		}
	}
	if len(src) > luaFileFlagBytes {
		flag(1, "the file has %d bytes, more than %d", len(src), luaFileFlagBytes)
	}
	toks := luaTokens(string(src))
	// a name the file declares local is its own and is not flagged
	local := map[string]bool{}
	for i := 1; i < len(toks); i++ {
		if toks[i].kind == tokName && (toks[i-1].text == "local" || (toks[i-1].text == "function" && i > 1 && toks[i-2].text == "local")) {
			local[toks[i].text] = true
		}
	}
	for i, t := range toks {
		switch t.kind {
		case tokString:
			if len(t.text) > luaStringFlag {
				flag(t.line, "a string literal of %d bytes", len(t.text))
			}
			if escapeRunRE.MatchString(t.text) {
				flag(t.line, "a long run of \\ddd escapes")
			}
			if looksBase64(t.text) {
				flag(t.line, "text that looks like base64")
			}
		case tokName:
			prev := ""
			if i > 0 {
				prev = toks[i-1].text
			}
			field := prev == "." || prev == ":"
			switch {
			case !field && removedGlobals[t.text] && !local[t.text]:
				flag(t.line, "names %s, a global Bururu's sandbox removes", t.text)
			case field && t.text == "dump" && i > 1 && toks[i-2].text == "string":
				flag(t.line, "names string.dump")
			case !field && t.text == "rawset" && i+2 < len(toks) && toks[i+1].text == "(" && libraryTables[toks[i+2].text]:
				flag(t.line, "rawset on the library table %s", toks[i+2].text)
			case machineRE.MatchString(t.text):
				flag(t.line, "the machine-looking name %s", t.text)
			}
		}
	}
	return out
}

type tokKind int

const (
	tokName tokKind = iota
	tokString
	tokOther
)

type luaTok struct {
	kind tokKind
	text string // a string's contents as written, without quotes
	line int
}

// luaTokens splits Lua source into names, string literals and single
// characters; comments and numbers are dropped. It is a reviewer's aid
// and tolerates broken code.
func luaTokens(s string) []luaTok {
	var out []luaTok
	line := 1
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case strings.HasPrefix(s[i:], "--"):
			if lvl, ok := longBracket(s[i+2:]); ok {
				end := closeBracket(s, i+2+lvl+2, lvl)
				line += strings.Count(s[i:end], "\n")
				i = end
				continue
			}
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '[':
			if lvl, ok := longBracket(s[i:]); ok {
				start := i + lvl + 2
				end := closeBracket(s, start, lvl)
				out = append(out, luaTok{tokString, s[start:max(start, end-lvl-2)], line})
				line += strings.Count(s[i:end], "\n")
				i = end
				continue
			}
			out = append(out, luaTok{tokOther, "[", line})
			i++
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(s) && s[j] != c && s[j] != '\n' {
				if s[j] == '\\' && j+1 < len(s) {
					j++
				}
				j++
			}
			out = append(out, luaTok{tokString, s[i+1 : min(j, len(s))], line})
			i = min(j+1, len(s))
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < len(s) && (s[j] == '_' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || s[j] >= '0' && s[j] <= '9') {
				j++
			}
			out = append(out, luaTok{tokName, s[i:j], line})
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < len(s) && (s[j] == '.' || s[j] == '_' || s[j] >= '0' && s[j] <= '9' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z') {
				j++
			}
			i = j
		default:
			out = append(out, luaTok{tokOther, string(c), line})
			i++
		}
	}
	return out
}

// longBracket reports a long bracket "[[", "[=[", ... at the start of s
// and its level.
func longBracket(s string) (int, bool) {
	if !strings.HasPrefix(s, "[") {
		return 0, false
	}
	n := 1
	for n < len(s) && s[n] == '=' {
		n++
	}
	if n < len(s) && s[n] == '[' {
		return n - 1, true
	}
	return 0, false
}

// closeBracket is the index after the "]=*]" of level lvl that closes a
// long bracket whose contents start at start; the end of s when none.
func closeBracket(s string, start, lvl int) int {
	end := "]" + strings.Repeat("=", lvl) + "]"
	if start > len(s) {
		return len(s)
	}
	if k := strings.Index(s[start:], end); k >= 0 {
		return start + k + len(end)
	}
	return len(s)
}
