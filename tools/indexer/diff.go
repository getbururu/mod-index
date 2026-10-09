package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"strings"
)

// What a reviewer reads of a new version: how its permissions,
// dependencies and files changed against the previous accepted version,
// and the Lua that changed.

// PermDiff compares the permission items on Windows and Linux.
type PermDiff struct {
	Diff    string   // DiffFirst, DiffSame, DiffNarrower or DiffWider
	Added   []string // items new in this version ("linux: files:x=..." when only on one OS)
	Removed []string
	Why     []string // what else made it wider: a new required dependency, a changed kind
}

// permItems are p's items on both systems; an item on one only is
// prefixed with that system's name.
func permItems(p Permissions) []string {
	w, l := p.Items(OSWindows), p.Items(OSLinux)
	var out []string
	for _, it := range w {
		if slices.Contains(l, it) {
			out = append(out, it)
		} else {
			out = append(out, OSWindows+": "+it)
		}
	}
	for _, it := range l {
		if !slices.Contains(w, it) {
			out = append(out, OSLinux+": "+it)
		}
	}
	return out
}

// diffPermissions is the review diff of a new version against prev (nil:
// the first version). Wider: a new item on either system (a new or
// changed path, the screen, the keyboard, a port, a forward), a new
// required dependency, or another kind.
func diffPermissions(prev *StateVersion, prevKind string, next Version, nextKind string) PermDiff {
	if prev == nil {
		return PermDiff{Diff: DiffFirst, Added: permItems(next.Permissions)}
	}
	a, b := permItems(prev.Permissions), permItems(next.Permissions)
	d := PermDiff{}
	for _, it := range b {
		if !slices.Contains(a, it) {
			d.Added = append(d.Added, it)
		}
	}
	for _, it := range a {
		if !slices.Contains(b, it) {
			d.Removed = append(d.Removed, it)
		}
	}
	for _, id := range sortedKeys(next.Dependencies) {
		dep := next.Dependencies[id]
		old, had := prev.Dependencies[id]
		if dep.Kind == DepRequired && (!had || old.Kind != DepRequired) {
			d.Why = append(d.Why, "a new required dependency: "+id)
		}
	}
	if prevKind != nextKind {
		d.Why = append(d.Why, "the kind changed from "+prevKind+" to "+nextKind)
	}
	switch {
	case len(d.Added) > 0 || len(d.Why) > 0:
		d.Diff = DiffWider
	case len(d.Removed) > 0:
		d.Diff = DiffNarrower
	default:
		d.Diff = DiffSame
	}
	return d
}

// diffDependencies lists added, removed and changed dependencies.
func diffDependencies(prev, next map[string]Dependency) []string {
	var out []string
	keys := slices.Sorted(maps.Keys(next))
	for id := range prev {
		if _, ok := next[id]; !ok {
			keys = append(keys, id)
		}
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
	dep := func(d Dependency) string {
		if d.Version == "" {
			return d.Kind + " (any version)"
		}
		return d.Kind + " " + d.Version
	}
	for _, id := range keys {
		a, had := prev[id]
		b, has := next[id]
		switch {
		case !had:
			out = append(out, "+ "+id+": "+dep(b))
		case !has:
			out = append(out, "- "+id+": "+dep(a))
		case a != b:
			out = append(out, "~ "+id+": "+dep(a)+" -> "+dep(b))
		}
	}
	return out
}

// PackFile is one file of a pack.
type PackFile struct {
	Path   string
	SHA256 string
	Data   []byte // kept for .lua files only
}

// Limits of reading a pack for the review.
const (
	maxPackFiles = 4096
	maxPackBytes = 256 << 20 // uncompressed, all files
	maxLuaFile   = 1 << 20   // a .lua file kept for the diff
)

// readPack lists a pack's files with their hashes, the one top folder
// (if every file sits in it) taken off, and keeps the Lua sources.
func readPack(b []byte) ([]PackFile, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, err
	}
	if len(zr.File) > maxPackFiles {
		return nil, fmt.Errorf("%d files, more than %d", len(zr.File), maxPackFiles)
	}
	var out []PackFile
	total := int64(0)
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		name := f.Name
		if !relPath(name) || strings.ContainsAny(name, "\x00") {
			return nil, fmt.Errorf("the pack has the path %q", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		var keep bytes.Buffer
		w := io.Writer(h)
		if strings.HasSuffix(strings.ToLower(name), ".lua") {
			w = io.MultiWriter(h, &limited{w: &keep, n: maxLuaFile})
		}
		n, err := io.Copy(w, io.LimitReader(rc, maxPackBytes-total+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		total += n
		if total > maxPackBytes {
			return nil, errors.New("the pack unpacks to too many bytes")
		}
		pf := PackFile{Path: name, SHA256: hex.EncodeToString(h.Sum(nil))}
		if keep.Len() > 0 || strings.HasSuffix(strings.ToLower(name), ".lua") {
			pf.Data = keep.Bytes()
		}
		out = append(out, pf)
	}
	// one top folder around everything: compare without it
	if len(out) > 0 {
		top, _, ok := strings.Cut(out[0].Path, "/")
		for _, f := range out {
			if t, _, has := strings.Cut(f.Path, "/"); !has || t != top {
				ok = false
				break
			}
		}
		if ok {
			for i := range out {
				out[i].Path = strings.TrimPrefix(out[i].Path, top+"/")
			}
		}
	}
	slices.SortFunc(out, func(a, b PackFile) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

// FileDiff is how a pack's files changed.
type FileDiff struct {
	Added, Removed, Changed []string
	Same                    int
}

func diffFiles(prev, next []PackFile) FileDiff {
	var d FileDiff
	old := map[string]string{}
	for _, f := range prev {
		old[f.Path] = f.SHA256
	}
	seen := map[string]bool{}
	for _, f := range next {
		seen[f.Path] = true
		switch h, ok := old[f.Path]; {
		case !ok:
			d.Added = append(d.Added, f.Path)
		case h != f.SHA256:
			d.Changed = append(d.Changed, f.Path)
		default:
			d.Same++
		}
	}
	for _, f := range prev {
		if !seen[f.Path] {
			d.Removed = append(d.Removed, f.Path)
		}
	}
	return d
}

// diffLua is a unified diff of every .lua file that changed, was added
// or was removed.
func diffLua(prev, next []PackFile) string {
	old := map[string]PackFile{}
	for _, f := range prev {
		if isLua(f.Path) {
			old[f.Path] = f
		}
	}
	var b strings.Builder
	done := map[string]bool{}
	for _, f := range next {
		if !isLua(f.Path) {
			continue
		}
		done[f.Path] = true
		o, had := old[f.Path]
		if had && o.SHA256 == f.SHA256 {
			continue
		}
		b.WriteString(lineDiff(f.Path, string(o.Data), string(f.Data)))
	}
	for _, f := range prev {
		if isLua(f.Path) && !done[f.Path] {
			b.WriteString(lineDiff(f.Path, string(f.Data), ""))
		}
	}
	return b.String()
}

func isLua(p string) bool { return strings.EqualFold(path.Ext(p), ".lua") }

// Limits of the line diff: past them it names the sizes only.
const maxDiffCells = 4 << 20

// lineDiff is a unified diff of two texts with two lines of context.
func lineDiff(name, a, b string) string {
	x, y := splitText(a), splitText(b)
	head := "--- " + name + " (before)\n+++ " + name + " (after)\n"
	if len(x)*len(y) > maxDiffCells {
		return head + fmt.Sprintf("@@ too long to compare here: %d lines before, %d after @@\n", len(x), len(y))
	}
	// longest common subsequence, from the ends
	n, m := len(x), len(y)
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	type op struct {
		kind byte // ' ', '-', '+'
		text string
		ai   int // line in a (1-based) of ' ' and '-'
		bi   int // line in b of ' ' and '+'
	}
	var ops []op
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && x[i] == y[j]:
			ops = append(ops, op{' ', x[i], i + 1, j + 1})
			i, j = i+1, j+1
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, op{'-', x[i], i + 1, j + 1})
			i++
		default:
			ops = append(ops, op{'+', y[j], i + 1, j + 1})
			j++
		}
	}
	const ctx = 2
	var out strings.Builder
	out.WriteString(head)
	for k := 0; k < len(ops); {
		if ops[k].kind == ' ' {
			k++
			continue
		}
		start := max(0, k-ctx)
		end := k
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			// a run of context longer than twice ctx ends the hunk
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run == len(ops) || run-end > 2*ctx {
				end = min(end+ctx, len(ops))
				break
			}
			end = run
		}
		fmt.Fprintf(&out, "@@ line %d before, line %d after @@\n", ops[start].ai, ops[start].bi)
		for _, o := range ops[start:end] {
			out.WriteByte(o.kind)
			out.WriteString(o.text)
			out.WriteByte('\n')
		}
		k = end
	}
	return out.String()
}

// splitText splits a text into lines without their ends.
func splitText(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
