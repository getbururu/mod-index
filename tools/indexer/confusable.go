package main

import (
	"fmt"
	"slices"
	"strings"
)

// The confusable check: a new id, name or author must not look like one
// another owner already listed, or a reserved one. Names are ASCII only,
// so look-alike letters of other scripts cannot pass at all; this check
// covers the ASCII tricks.

// fold is s as it looks: lower case, separators gone, and the letters
// people mistake for each other made one (0/o, 1/l/i, 5/s, rn/m, vv/w).
func fold(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("rn", "m", "vv", "w").Replace(s)
	var b strings.Builder
	for _, r := range s {
		switch r {
		case ' ', '-', '_', '.', '\'', '"':
			continue
		case '0':
			r = 'o'
		case '1', 'i', '|', '!':
			r = 'l'
		case '5', '$':
			r = 's'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// editDistance is the Levenshtein distance of a and b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// confusable reports two strings that look alike: equal after folding,
// or within an edit distance that grows with length (0 up to 3
// characters, 1 up to 7, 2 from 8).
func confusable(a, b string) bool {
	fa, fb := fold(a), fold(b)
	if fa == "" || fb == "" {
		return false
	}
	if fa == fb {
		return true
	}
	n := min(len(fa), len(fb))
	limit := 2
	switch {
	case n <= 3:
		limit = 0
	case n <= 7:
		limit = 1
	}
	return editDistance(fa, fb) <= limit
}

// isID reports a label that is an id.
func isID(what string) bool { return what == "id" || what == "reserved id" }

// Label is an id, name or author of a listed mod.
type Label struct {
	What  string // "id", "name" or "author"
	Value string
	Mod   string // the mod it belongs to; "" a reserved one
	Owner int64  // the GitHub owner id of that mod; 0 unknown
}

// labelsOf are every id, name and author the repo's states and entries
// carry.
func labelsOf(r *Repo) []Label {
	var out []Label
	for _, id := range sortedKeys(r.Entries) {
		out = append(out, Label{"id", id, id, 0})
	}
	for _, id := range sortedKeys(r.States) {
		s := r.States[id]
		for i := range out {
			if out[i].Mod == id {
				out[i].Owner = s.OwnerID
			}
		}
		if s.Mod.Name != "" {
			out = append(out, Label{"name", s.Mod.Name, id, s.OwnerID})
		}
		for _, a := range s.Mod.Authors {
			out = append(out, Label{"author", a, id, s.OwnerID})
		}
	}
	for _, id := range r.Reserved.IDs {
		out = append(out, Label{"reserved id", id, "", 0})
	}
	for _, n := range r.Reserved.Names {
		out = append(out, Label{"reserved name", n, "", 0})
	}
	return out
}

// nameProblems are the reserved and confusable problems of a mod's id,
// name and authors against the repo. An official mod may carry reserved
// names; any mod may share labels with the other mods of its own GitHub
// owner.
func nameProblems(r *Repo, id string, official bool, owner int64, name string, authors []string) []string {
	var probs []string
	reservedName := func(s string) bool {
		return ReservedName(s) || slices.ContainsFunc(r.Reserved.Names, func(n string) bool { return strings.EqualFold(n, s) })
	}
	if !official {
		if slices.Contains(r.Reserved.IDs, id) {
			probs = append(probs, fmt.Sprintf("the id %q is reserved", id))
		}
		if reservedName(name) {
			probs = append(probs, fmt.Sprintf("the name %q is reserved", name))
		}
		for _, a := range authors {
			if reservedName(a) {
				probs = append(probs, fmt.Sprintf("the author %q is reserved", a))
			}
		}
	}
	mine := []Label{{"id", id, id, owner}, {"name", name, id, owner}}
	for _, a := range authors {
		mine = append(mine, Label{"author", a, id, owner})
	}
	others := labelsOf(r)
	for _, m := range mine {
		if m.Value == "" {
			continue
		}
		for _, o := range others {
			switch {
			case o.Mod == id:
				continue // its own
			case o.Mod != "" && owner != 0 && o.Owner == owner:
				continue // the same GitHub owner's other mod
			case o.Mod == "" && official:
				continue // official mods may carry reserved names
			case isID(m.What) != isID(o.What):
				continue // ids against ids, names and authors against names and authors
			}
			if confusable(m.Value, o.Value) {
				where := "of " + o.Mod
				if o.Mod == "" {
					where = "on the reserved list"
				}
				probs = append(probs, fmt.Sprintf("the %s %q looks like the %s %q %s", m.What, m.Value, strings.TrimPrefix(o.What, "reserved "), o.Value, where))
			}
		}
	}
	slices.Sort(probs)
	return slices.Compact(probs)
}
