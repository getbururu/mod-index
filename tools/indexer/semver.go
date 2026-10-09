package main

import (
	"strconv"
	"strings"
)

// semver is a semantic version as manifests and the list write them:
// major.minor.patch with an optional pre-release after "-"; build
// metadata after "+" is ignored. A copy of the client's rules (and of
// the manifest rules), so the two compare versions alike.
type semver struct {
	n   [3]int
	pre string
}

// parseSemver reads "1.0.0" or "2.1.0-beta.1"; ok is false otherwise.
func parseSemver(s string) (v semver, ok bool) {
	core, _, _ := strings.Cut(s, "+")
	core, v.pre, ok = strings.Cut(core, "-")
	if ok && v.pre == "" {
		return v, false
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p[0] == '+' || (len(p) > 1 && p[0] == '0') {
			return v, false
		}
		v.n[i] = n
	}
	return v, true
}

// cmp is -1, 0 or 1 as v is below, equal to or above w. A pre-release
// sorts below its release; pre-releases compare by their dot parts.
func (v semver) cmp(w semver) int {
	for i := range v.n {
		if c := cmpInt(v.n[i], w.n[i]); c != 0 {
			return c
		}
	}
	switch {
	case v.pre == w.pre:
		return 0
	case v.pre == "":
		return 1
	case w.pre == "":
		return -1
	}
	a, b := strings.Split(v.pre, "."), strings.Split(w.pre, ".")
	for i := 0; i < len(a) && i < len(b); i++ {
		na, ea := strconv.Atoi(a[i])
		nb, eb := strconv.Atoi(b[i])
		switch {
		case ea == nil && eb == nil:
			if c := cmpInt(na, nb); c != 0 {
				return c
			}
		case ea == nil:
			return -1
		case eb == nil:
			return 1
		default:
			if c := strings.Compare(a[i], b[i]); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(a), len(b))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// cmpVersions compares two version strings; one that does not parse
// sorts below every one that does.
func cmpVersions(a, b string) int {
	va, oka := parseSemver(a)
	vb, okb := parseSemver(b)
	switch {
	case oka && okb:
		return va.cmp(vb)
	case oka:
		return 1
	case okb:
		return -1
	}
	return strings.Compare(a, b)
}

// semRange is a version range: space-separated comparators (>= <= > < =)
// that all hold. The empty range takes any version.
type semRange []struct {
	op string
	v  semver
}

// parseRange reads ">=1.0.0 <2.0.0"; ok is false when it does not parse.
func parseRange(s string) (r semRange, ok bool) {
	for _, f := range strings.Fields(s) {
		op := ""
		for _, o := range []string{">=", "<=", ">", "<", "="} {
			if strings.HasPrefix(f, o) {
				op = o
				break
			}
		}
		v, ok := parseSemver(strings.TrimPrefix(f, op))
		if op == "" || !ok {
			return nil, false
		}
		r = append(r, struct {
			op string
			v  semver
		}{op, v})
	}
	return r, true
}

// has reports whether v is in the range.
func (r semRange) has(v semver) bool {
	for _, c := range r {
		d := v.cmp(c.v)
		var ok bool
		switch c.op {
		case ">=":
			ok = d >= 0
		case "<=":
			ok = d <= 0
		case ">":
			ok = d > 0
		case "<":
			ok = d < 0
		case "=":
			ok = d == 0
		}
		if !ok {
			return false
		}
	}
	return true
}

// apiOf reads a mod API version "major.minor"; ok is false otherwise.
func apiOf(s string) (major, minor int, ok bool) {
	a, b, found := strings.Cut(s, ".")
	x, e1 := strconv.Atoi(a)
	y, e2 := strconv.Atoi(b)
	if !found || e1 != nil || e2 != nil || x < 0 || y < 0 || a[0] == '+' || b[0] == '+' {
		return 0, 0, false
	}
	return x, y, true
}
