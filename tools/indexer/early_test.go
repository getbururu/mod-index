package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// eliteVersions is eliteState with more versions, each released at its
// time.
func eliteVersions(released map[string]time.Time) *State {
	st := eliteState()
	base := st.Versions["0.9.0"]
	for v, at := range released {
		sv := base
		sv.URL = ReleaseLink(st.Repo, "v"+v, "elite-"+v+PackExt)
		sv.SHA256 = sha256Hex([]byte(v))
		sv.Published = at
		sv.Review.Diff = DiffSame
		st.Versions[v] = sv
	}
	return st
}

// earlyRepo is a repo with the official Elite at 0.9.0 (released
// 2026-10-29) and 0.9.1 (released 2026-11-01), and 0.9.1 in early access
// until free. Today, t0, is 2026-11-02.
func earlyRepo(t *testing.T, free Date) string {
	t.Helper()
	root := testRepo(t, &Entry{Entry: 1, ID: "elite", Repo: "getbururu/mod-elite", Official: true, Prerelease: true,
		EarlyAccess: map[string]Date{"0.9.1": free}})
	writeState(t, root, eliteVersions(map[string]time.Time{"0.9.1": t0.Add(-24 * time.Hour)}))
	return root
}

// TestEarlyAccessEntry: early_access is for an official mod's release
// versions, each with a real day, and the entry still reads strictly.
func TestEarlyAccessEntry(t *testing.T) {
	official := func(early map[string]Date) *Entry {
		return &Entry{Entry: 1, ID: "elite", Repo: "getbururu/mod-elite", Official: true, EarlyAccess: early}
	}
	community := rallyEntry()
	community.EarlyAccess = map[string]Date{"1.1.0": "2026-12-01"}
	for _, tc := range []struct {
		name string
		e    *Entry
		want string
	}{
		{"an official mod's next version", official(map[string]Date{"0.9.1": "2026-12-01"}), ""},
		{"a community mod", community, "early_access is for official mods only"},
		{"a test version", official(map[string]Date{"0.9.1-beta.1": "2026-12-01"}), `early_access: "0.9.1-beta.1" is not a version like 1.2.0`},
		{"a tag", official(map[string]Date{"v0.9.1": "2026-12-01"}), `early_access: "v0.9.1" is not a version like 1.2.0`},
		{"no such day", official(map[string]Date{"0.9.1": "2026-12-32"}), `early_access.0.9.1: "2026-12-32" is not a day (YYYY-MM-DD)`},
		{"not a day", official(map[string]Date{"0.9.1": "1 December"}), `early_access.0.9.1: "1 December" is not a day`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(checkEntry(tc.e, tc.e.ID), "\n")
			switch {
			case tc.want == "" && got != "":
				t.Fatalf("problems:\n%s", got)
			case tc.want != "" && !strings.Contains(got, tc.want):
				t.Fatalf("problems %q, want %q", got, tc.want)
			}
		})
	}
	root := testRepo(t)
	src := `{"entry": 1, "id": "elite", "repo": "getbururu/mod-elite", "official": true, "prerelease": false,
  "early_access": {"0.9.1": "2026-12-01"}, "early": true}
`
	if err := writeFile(filepath.Join(root, "entries", "elite.json"), []byte(src)); err != nil {
		t.Fatal(err)
	}
	if _, probs := LoadRepo(root); !strings.Contains(strings.Join(probs, "\n"), `unknown field "early"`) {
		t.Fatalf("problems %q", probs)
	}
}

// TestEarlyAccessDay: once a version is accepted, its free day is 1 to
// 90 days after its release day; before that, any day reads.
func TestEarlyAccessDay(t *testing.T) {
	root := testRepo(t, &Entry{Entry: 1, ID: "elite", Repo: "getbururu/mod-elite", Official: true,
		EarlyAccess: map[string]Date{"0.9.1": "2026-10-01"}})
	writeState(t, root, eliteState())
	load(t, root)
	// 0.9.1 came out on 2026-11-01
	for _, tc := range []struct {
		free Date
		want string
	}{
		{"2026-11-02", ""},
		{"2026-12-01", ""},
		{"2027-01-30", ""},
		{"2026-11-01", "entries/elite.json: early_access.0.9.1: 2026-11-01 is not after the release on 2026-11-01"},
		{"2026-10-20", "entries/elite.json: early_access.0.9.1: 2026-10-20 is not after the release on 2026-11-01"},
		{"2027-01-31", "entries/elite.json: early_access.0.9.1: 2027-01-31 is more than 90 days after the release on 2026-11-01"},
	} {
		_, probs := LoadRepo(earlyRepo(t, tc.free))
		got := strings.Join(probs, "\n")
		switch {
		case tc.want == "" && got != "":
			t.Errorf("%s: problems:\n%s", tc.free, got)
		case tc.want != "" && got != tc.want:
			t.Errorf("%s: problems %q, want %q", tc.free, got, tc.want)
		}
	}
}

// TestEarlyAccessBuild: before its free day an early-access version is
// in v2/ with free_from and not in v1/; on that day v1/ gets it with the
// next serial, and v2/ stays the same.
func TestEarlyAccessBuild(t *testing.T) {
	r := load(t, earlyRepo(t, "2026-11-12"))
	today := DateOf(t0)
	v2, err := BuildIndex(r, IndexFormat, nil, today)
	if err != nil {
		t.Fatal(err)
	}
	vs := v2.Index.Mods["elite"].Versions
	if v2.Index.Index != 2 || len(vs) != 2 || vs["0.9.1"].FreeFrom != "2026-11-12" || vs["0.9.0"].FreeFrom != "" {
		t.Fatalf("v2: %+v", vs)
	}
	if bytes.Count(v2.File, []byte(`"free_from":"2026-11-12"`)) != 1 || bytes.Count(v2.File, []byte(`"free_from"`)) != 1 {
		t.Fatalf("v2: %s", v2.File)
	}
	v1, err := BuildIndex(r, IndexFormatV1, nil, today)
	if err != nil {
		t.Fatal(err)
	}
	vs = v1.Index.Mods["elite"].Versions
	if _, ok := vs["0.9.1"]; ok || len(vs) != 1 || v1.Index.Index != 1 || bytes.Contains(v1.File, []byte("free_from")) {
		t.Fatalf("v1 before the day: %s", v1.File)
	}
	if b, err := BuildIndex(r, IndexFormatV1, v1.Index, "2026-11-11"); err != nil || !b.Same || b.Index.Serial != 1 {
		t.Fatalf("the day before: %v %+v", err, b)
	}
	day, err := BuildIndex(r, IndexFormatV1, v1.Index, "2026-11-12")
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := day.Index.Mods["elite"].Versions["0.9.1"]; day.Same || day.Index.Serial != 2 || !ok || v.SHA256 != sha256Hex([]byte("0.9.1")) || v.FreeFrom != "" {
		t.Fatalf("v1 on the day: %+v", day.Index.Mods["elite"])
	}
	if _, err := readIndex(day.File); err != nil {
		t.Fatal(err)
	}
	if b, err := BuildIndex(r, IndexFormatV1, day.Index, "2026-12-01"); err != nil || !b.Same {
		t.Fatalf("a later day: %v %+v", err, b)
	}
	if b, err := BuildIndex(r, IndexFormat, v2.Index, "2026-11-12"); err != nil || !b.Same {
		t.Fatalf("v2 on the day: %v %+v", err, b)
	}

	// a mod whose only version is early access is not in v1/ until then
	root := testRepo(t, &Entry{Entry: 1, ID: "elite", Repo: "getbururu/mod-elite", Official: true,
		EarlyAccess: map[string]Date{"0.9.0": "2026-11-12"}})
	writeState(t, root, eliteState())
	r = load(t, root)
	if b, err := BuildIndex(r, IndexFormatV1, nil, today); err != nil || len(b.Index.Mods) != 0 {
		t.Fatalf("v1: %v %s", err, b.File)
	}
	if b, err := BuildIndex(r, IndexFormat, nil, today); err != nil || b.Index.Mods["elite"].Versions["0.9.0"].FreeFrom != "2026-11-12" {
		t.Fatalf("v2: %v %s", err, b.File)
	}

	// format 1 knows no free_from, and format 2 takes a day only
	withFree := bytes.Replace(v1.File, []byte(`"api":`), []byte(`"free_from":"2026-11-12","api":`), 1)
	if _, err := readIndex(withFree); err == nil || !strings.Contains(err.Error(), "has free_from, which format 1 does not know") {
		t.Fatal(err)
	}
	soon := bytes.Replace(v2.File, []byte(`"free_from":"2026-11-12"`), []byte(`"free_from":"soon"`), 1)
	if _, err := readIndex(soon); err == nil || !strings.Contains(err.Error(), `free_from "soon" is not a day`) {
		t.Fatal(err)
	}
}

// TestEarlyAccessStaysFree: a version published free for everyone never
// becomes early access, and a published free day only moves earlier.
func TestEarlyAccessStaysFree(t *testing.T) {
	root := earlyRepo(t, "2026-11-12")
	today := DateOf(t0)
	free := load(t, root)
	free.Entries["elite"].EarlyAccess = nil
	v1, err := BuildIndex(free, IndexFormatV1, nil, today)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := BuildIndex(free, IndexFormat, nil, today)
	if err != nil {
		t.Fatal(err)
	}
	r := load(t, root)
	if _, err := BuildIndex(r, IndexFormatV1, v1.Index, today); err == nil ||
		!strings.Contains(err.Error(), "elite 0.9.1 is in v1/ already, free for everyone: something free stays free") {
		t.Fatal(err)
	}
	if _, err := BuildIndex(r, IndexFormat, v2.Index, today); err == nil ||
		!strings.Contains(err.Error(), "elite 0.9.1 was published free for everyone") {
		t.Fatal(err)
	}
	pub, err := BuildIndex(r, IndexFormat, nil, today)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		free Date
		want string
	}{
		{"2026-11-12", ""},
		{"2026-11-05", ""},
		{"", ""},
		{"2026-11-13", "elite 0.9.1: free_from 2026-11-13 is later than the published 2026-11-12; it may only move earlier"},
	} {
		r.Entries["elite"].EarlyAccess = map[string]Date{"0.9.1": tc.free}
		if tc.free == "" {
			r.Entries["elite"].EarlyAccess = nil
		}
		_, err := BuildIndex(r, IndexFormat, pub.Index, today)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%q: %v", tc.free, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%q: %v, want %q", tc.free, err, tc.want)
		}
	}
}

// TestEarlyAccessDependency: a version may not require a mod version
// that is still early access on the day the version reaches everyone.
func TestEarlyAccessDependency(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rng      string
		released time.Time
		early    Date // the add-on's own free day
		want     string
	}{
		{"only the early version fits", ">=0.9.1", t0, "",
			"state/elite-sounds.json: elite-sounds 0.1.0 reaches everyone on 2026-11-02, but it requires elite >=0.9.1, and the listed versions of elite in that range are all still early access then"},
		{"a free version fits too", ">=0.9.0", t0, "", ""},
		{"released on the free day", ">=0.9.1", time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC), "", ""},
		{"early access itself until then", ">=0.9.1", t0, "2026-11-12", ""},
		{"early access itself, free sooner", ">=0.9.1", t0, "2026-11-11", "elite-sounds 0.1.0 reaches everyone on 2026-11-11, but it requires elite >=0.9.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := earlyRepo(t, "2026-11-12")
			e := &Entry{Entry: 1, ID: "elite-sounds", Repo: "getbururu/mod-elite-sounds", Official: true}
			if tc.early != "" {
				e.EarlyAccess = map[string]Date{"0.1.0": tc.early}
			}
			writeEntry(t, root, e)
			st := eliteState()
			st.ID, st.Repo, st.RepoID = "elite-sounds", "getbururu/mod-elite-sounds", 102
			st.Mod.Name, st.Mod.Kind, st.Mod.Games, st.Mod.For = "Elite Sounds", KindAddon, []string{}, []string{"elite"}
			sv := st.Versions["0.9.0"]
			sv.URL = ReleaseLink(st.Repo, "v0.1.0", "elite-sounds-0.1.0"+PackExt)
			sv.Published = tc.released
			sv.Dependencies = map[string]Dependency{"elite": {Kind: DepRequired, Version: tc.rng}}
			st.Versions = map[string]StateVersion{"0.1.0": sv}
			writeState(t, root, st)
			_, probs := LoadRepo(root)
			got := strings.Join(probs, "\n")
			switch {
			case tc.want == "" && got != "":
				t.Fatalf("problems:\n%s", got)
			case tc.want != "" && !strings.Contains(got, tc.want):
				t.Fatalf("problems %q, want %q", got, tc.want)
			}
		})
	}
}

// TestEarlyAccessPoll: the poll takes an early-access version only when
// its day fits the release, and the report names the day.
func TestEarlyAccessPoll(t *testing.T) {
	root, gh, run := rallySetup(t)
	e := rallyEntry()
	e.Official = true
	writeEntry(t, root, e)
	changes, _ := poll(t, root, gh, run)
	accept(t, root, changes)
	// released on 2026-10-30
	gh.release("rally-modder/gravel-rally", "v1.1.0", "gravel-rally-1.1.0.brr", makePack(t, map[string]string{"manifest.json": "{ }"}), nil)
	e.EarlyAccess = map[string]Date{"1.1.0": "2026-10-30"}
	writeEntry(t, root, e)
	changes, skipped := poll(t, root, gh, run)
	if len(changes) != 0 || len(skipped) != 1 || skipped[0] != "gravel-rally 1.1.0: the entry's early_access day 2026-10-30 is not after the release on 2026-10-30; a maintainer sets a day 1 to 90 days after the release" {
		t.Fatalf("%v %q", changes, skipped)
	}
	e.EarlyAccess["1.1.0"] = "2026-11-29"
	writeEntry(t, root, e)
	changes, skipped = poll(t, root, gh, run)
	if len(changes) != 1 || len(skipped) != 0 {
		t.Fatalf("%v %q", changes, skipped)
	}
	if want := "Early:     early access for Bururu Pro until 2026-11-29, 30 days after the release; everyone gets it that day"; !strings.Contains(changes[0].Report, want) {
		t.Fatalf("report:\n%s", changes[0].Report)
	}
	accept(t, root, changes)
	r := load(t, root)
	v1, err := BuildIndex(r, IndexFormatV1, nil, DateOf(t0))
	if err != nil || len(v1.Index.Mods["gravel-rally"].Versions) != 1 {
		t.Fatalf("v1: %v %+v", err, v1)
	}
	v2, err := BuildIndex(r, IndexFormat, nil, DateOf(t0))
	if err != nil || v2.Index.Mods["gravel-rally"].Versions["1.1.0"].FreeFrom != "2026-11-29" {
		t.Fatalf("v2: %v %+v", err, v2)
	}

	// an add-on free from its release may not require that version alone
	gh.repo("rally-modder/rally-sounds", 502, 601)
	writeEntry(t, root, &Entry{Entry: 1, ID: "rally-sounds", Repo: "rally-modder/rally-sounds"})
	need := ">=1.1.0"
	run.listing = func(id, v string) *Listing {
		l := basicListing(run, id, v)
		if id == "rally-sounds" {
			l.Kind, l.Games, l.For, l.Name = KindAddon, []string{}, []string{"gravel-rally"}, "Rally Sounds"
			l.Dependencies = map[string]Dependency{"gravel-rally": {Kind: DepRequired, Version: need}}
		}
		return l
	}
	gh.release("rally-modder/rally-sounds", "v0.1.0", "rally-sounds-0.1.0.brr", makePack(t, map[string]string{"m.json": "{}"}), nil)
	changes, skipped = poll(t, root, gh, run)
	if want := "rally-sounds 0.1.0: free for everyone from 2026-10-30, but it requires gravel-rally >=1.1.0, and the listed versions of gravel-rally in that range are all still early access then"; len(changes) != 0 || len(skipped) != 1 || skipped[0] != want {
		t.Fatalf("%v %q", changes, skipped)
	}
	need = ">=1.0.0"
	if changes, skipped = poll(t, root, gh, run); len(changes) != 1 || len(skipped) != 0 {
		t.Fatalf("with a free version in the range: %v %q", changes, skipped)
	}
}

// TestEarlyAccessMaintainers: only the list's maintainers add, change
// or take away an entry's early_access.
func TestEarlyAccessMaintainers(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.repo("getbururu/mod-elite", 101, 201)
	tree := func(early map[string]Date) string {
		root := testRepo(t, &Entry{Entry: 1, ID: "elite", Repo: "getbururu/mod-elite", Official: true, Prerelease: true, EarlyAccess: early})
		writeState(t, root, eliteState())
		return root
	}
	check := func(before, after map[string]Date, assoc string) *EntryCheck {
		p := &Poller{GH: gh.client(""), Ref: "test", Work: t.TempDir(), Now: t0}
		return CheckPullRequest(context.Background(), p, tree(before), tree(after), "someone", assoc)
	}
	early := map[string]Date{"0.9.1": "2026-12-01"}
	later := map[string]Date{"0.9.1": "2026-12-20"}
	for _, c := range []struct {
		name          string
		before, after map[string]Date
	}{{"add", nil, early}, {"change", early, later}, {"take away", early, nil}} {
		for _, assoc := range []string{"NONE", "CONTRIBUTOR", "FIRST_TIME_CONTRIBUTOR"} {
			if res := check(c.before, c.after, assoc); res.Pass || !strings.Contains(res.Report, "FAIL only the list's maintainers set early_access") {
				t.Errorf("%s by %s:\n%s", c.name, assoc, res.Report)
			}
		}
		for _, assoc := range []string{"OWNER", "MEMBER", "COLLABORATOR"} {
			if res := check(c.before, c.after, assoc); !res.Pass || !strings.Contains(res.Report, "  early access: "+earlyText(c.after)) {
				t.Errorf("%s by %s:\n%s", c.name, assoc, res.Report)
			}
		}
	}
	if earlyText(early) != "0.9.1 until 2026-12-01" || earlyText(nil) != "(none)" {
		t.Fatal(earlyText(early))
	}

	// a listed version (0.9.0, released 2026-10-29) was published with
	// the base's day: free stays free, and the day only moves earlier
	for _, c := range []struct {
		before, after Date
		want          string
	}{
		{"", "2026-11-12", "FAIL elite 0.9.0 is listed free for everyone: something free stays free"},
		{"2026-11-12", "2026-11-20", "FAIL elite 0.9.0 is listed with early access until 2026-11-12: that day may only move earlier"},
		{"2026-11-12", "2026-11-05", ""},
		{"2026-11-12", "", ""},
	} {
		day := func(d Date) map[string]Date {
			if d == "" {
				return nil
			}
			return map[string]Date{"0.9.0": d}
		}
		res := check(day(c.before), day(c.after), "OWNER")
		switch {
		case c.want == "" && !res.Pass:
			t.Errorf("%q to %q:\n%s", c.before, c.after, res.Report)
		case c.want != "" && (res.Pass || !strings.Contains(res.Report, c.want)):
			t.Errorf("%q to %q, want %q:\n%s", c.before, c.after, c.want, res.Report)
		}
	}
}
