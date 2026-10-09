package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// rallySetup is a community mod with one release 1.0.0 on the fake
// GitHub.
func rallySetup(t *testing.T) (root string, gh *fakeGitHub, run *fakeRunner) {
	t.Helper()
	root = testRepo(t, rallyEntry())
	gh = newFakeGitHub(t)
	gh.repo("rally-modder/gravel-rally", 501, 601)
	run = &fakeRunner{}
	run.listing = func(id, v string) *Listing { return basicListing(run, id, v) }
	gh.release("rally-modder/gravel-rally", "v1.0.0", "gravel-rally-1.0.0.brr",
		makePack(t, map[string]string{"manifest.json": "{}", "lua/main.lua": "local x = 1\nreturn x\n"}), nil)
	return root, gh, run
}

func poll(t *testing.T, root string, gh *fakeGitHub, run Runner) ([]*Change, []string) {
	t.Helper()
	p := testPoller(t, root, gh, run)
	var all []*Change
	var skipped []string
	for _, id := range sortedKeys(p.Repo.Entries) {
		c, s, err := p.Poll(context.Background(), p.Repo.Entries[id])
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, c...)
		skipped = append(skipped, s...)
	}
	return all, skipped
}

// TestFirstListing: the newest release of a new entry becomes one bot
// change with the whole state, which the strict readers take.
func TestFirstListing(t *testing.T) {
	root, gh, run := rallySetup(t)
	gh.release("rally-modder/gravel-rally", "v1.1.0", "gravel-rally-1.1.0.brr", makePack(t, map[string]string{"manifest.json": "{ }"}), nil)
	changes, skipped := poll(t, root, gh, run)
	if len(changes) != 1 || len(skipped) != 0 {
		t.Fatalf("changes %d, skipped %v", len(changes), skipped)
	}
	c := changes[0]
	if c.Key != "gravel-rally-1.1.0" || c.Title != "List gravel-rally 1.1.0" || c.Kind != ChangeVersion {
		t.Fatalf("%+v", c)
	}
	st := c.State
	v := st.Versions["1.1.0"]
	if st.RepoID != 501 || st.OwnerID != 601 || st.OwnerLogin != "rally-modder" || st.Status != StatusListed || len(st.Versions) != 1 ||
		v.Review.Diff != DiffFirst || v.URL != "https://github.com/rally-modder/gravel-rally/releases/download/v1.1.0/gravel-rally-1.1.0.brr" ||
		!v.Published.Equal(t0.Add(-72*time.Hour)) || v.Checks.CheckedBy != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("%+v", st)
	}
	if !strings.Contains(c.Report, "first listing") || !strings.Contains(c.Report, "+ files:telemetry={SavedGames}/Rally/telemetry") ||
		!strings.Contains(c.Report, "+ udp:20777") {
		t.Fatalf("report:\n%s", c.Report)
	}
	accept(t, root, changes)
	if _, err := os.Stat(filepath.Join(root, "files", "text", v.Changelog.SHA256+".txt")); err != nil {
		t.Fatal(err)
	}
	// a second poll finds nothing new
	again, _ := poll(t, root, gh, run)
	if len(again) != 0 {
		t.Fatalf("%d changes after the first was accepted", len(again))
	}
}

// TestUpdateAndWider: a later version with a new folder is wider and its
// new items are named; one with fewer items is narrower.
func TestUpdateAndWider(t *testing.T) {
	root, gh, run := rallySetup(t)
	changes, _ := poll(t, root, gh, run)
	accept(t, root, changes)
	run.listing = func(id, v string) *Listing {
		l := basicListing(run, id, v)
		l.Permissions.Files["replays"] = PathDecl{Path: "{Documents}/Rally/replays"}
		l.Permissions.Screen = true
		return l
	}
	gh.release("rally-modder/gravel-rally", "v1.1.0", "gravel-rally-1.1.0.brr",
		makePack(t, map[string]string{"manifest.json": "{}", "lua/main.lua": "local x = 2\nreturn load(x)\n"}), nil)
	changes, _ = poll(t, root, gh, run)
	if len(changes) != 1 || changes[0].Title != "Add gravel-rally 1.1.0" {
		t.Fatalf("%v", changes)
	}
	c := changes[0]
	if c.State.Versions["1.1.0"].Review.Diff != DiffWider {
		t.Fatalf("diff %s", c.State.Versions["1.1.0"].Review.Diff)
	}
	for _, want := range []string{"Permissions against 1.0.0: wider", "+ files:replays={Documents}/Rally/replays", "+ screen",
		"~ lua/main.lua", "-local x = 1", "+local x = 2", "names load, a global Bururu's sandbox removes"} {
		if !strings.Contains(c.Report, want) {
			t.Errorf("the report lacks %q:\n%s", want, c.Report)
		}
	}
	accept(t, root, changes)
	run.listing = func(id, v string) *Listing {
		l := basicListing(run, id, v)
		l.Permissions.UDP = nil
		return l
	}
	gh.release("rally-modder/gravel-rally", "v1.2.0", "gravel-rally-1.2.0.brr", makePack(t, map[string]string{"manifest.json": "{}"}), nil)
	changes, _ = poll(t, root, gh, run)
	if len(changes) != 1 || changes[0].State.Versions["1.2.0"].Review.Diff != DiffNarrower {
		t.Fatalf("%v", changes)
	}
}

// TestDigestMismatchHolds: a download that is not GitHub's digest holds
// a listed mod (and refuses a new one).
func TestDigestMismatchHolds(t *testing.T) {
	root, gh, run := rallySetup(t)
	changes, _ := poll(t, root, gh, run)
	accept(t, root, changes)
	gh.release("rally-modder/gravel-rally", "v1.1.0", "gravel-rally-1.1.0.brr", makePack(t, map[string]string{"a.json": "{}"}), func(r *Release) {
		r.Assets[0].Digest = "sha256:" + strings.Repeat("0", 64)
	})
	changes, _ = poll(t, root, gh, run)
	if len(changes) != 1 || changes[0].Kind != ChangeHold || changes[0].State.Status != StatusHold ||
		!strings.Contains(changes[0].State.Note, "is not GitHub's digest") {
		t.Fatalf("%+v", changes)
	}
	accept(t, root, changes)
	// held: nothing more is taken until a maintainer looks
	gh.release("rally-modder/gravel-rally", "v1.2.0", "gravel-rally-1.2.0.brr", makePack(t, map[string]string{"b.json": "{}"}), nil)
	if changes, _ := poll(t, root, gh, run); len(changes) != 0 {
		t.Fatalf("a held mod took %v", changes)
	}

	root2, gh2, run2 := rallySetup(t)
	gh2.releases["rally-modder/gravel-rally"][0].Assets[0].Digest = "sha256:" + strings.Repeat("1", 64)
	changes, skipped := poll(t, root2, gh2, run2)
	if len(changes) != 0 || len(skipped) != 1 || !strings.Contains(skipped[0], "digest") {
		t.Fatalf("%v %v", changes, skipped)
	}
}

// TestChangedPackHolds: a pack that changes on GitHub after it was
// listed holds the mod; one that is gone from its release too.
func TestChangedPackHolds(t *testing.T) {
	for _, gone := range []bool{false, true} {
		root, gh, run := rallySetup(t)
		changes, _ := poll(t, root, gh, run)
		accept(t, root, changes)
		rel := &gh.releases["rally-modder/gravel-rally"][0]
		if gone {
			rel.Assets = nil
		} else {
			rel.Assets[0].Digest = "sha256:" + strings.Repeat("4", 64)
		}
		changes, _ = poll(t, root, gh, run)
		if len(changes) != 1 || changes[0].Kind != ChangeHold || !strings.HasPrefix(changes[0].State.Note, "Drift: ") {
			t.Fatalf("gone %v: %+v", gone, changes)
		}
		t.Log(changes[0].State.Note)
		accept(t, root, changes)
	}
}

// TestRepoIDChangeHolds: another repo id, or another owner id, holds the
// mod.
func TestRepoIDChangeHolds(t *testing.T) {
	for _, owner := range []bool{false, true} {
		root, gh, run := rallySetup(t)
		changes, _ := poll(t, root, gh, run)
		accept(t, root, changes)
		if owner {
			gh.repos["rally-modder/gravel-rally"].Owner.ID = 999
		} else {
			gh.repos["rally-modder/gravel-rally"].ID = 777
		}
		changes, _ = poll(t, root, gh, run)
		if len(changes) != 1 || changes[0].State.Status != StatusHold || !strings.HasPrefix(changes[0].State.Note, "Drift: ") {
			t.Fatalf("owner %v: %+v", owner, changes)
		}
		accept(t, root, changes)
	}
}

// TestNamesRefused: a non-ASCII name or author, a reserved author (also
// Bururu's earlier name) and an author that looks like another owner's
// are refused.
func TestNamesRefused(t *testing.T) {
	cases := map[string]func(l *Listing){
		"non-ASCII name":     func(l *Listing) { l.Name = "Gravel R\u00e4lly" },
		"non-ASCII author":   func(l *Listing) { l.Authors = []string{"R\u0430lly Modder"} },
		"reserved author":    func(l *Listing) { l.Authors = []string{"bururu"} },
		"reserved studio":    func(l *Listing) { l.Authors = []string{"Frontier Developments"} },
		"old app name":       func(l *Listing) { l.Authors = []string{"EDSense"} },
		"old app look-alike": func(l *Listing) { l.Name = "ED Sense" },
		"confusable author":  func(l *Listing) { l.Authors = []string{"Frontier Deve1opments"} },
		"confusable to name": func(l *Listing) { l.Name = "Elite Dangerou5" },
	}
	for name, mut := range cases {
		root, gh, run := rallySetup(t)
		// an official mod is listed already
		writeState(t, root, eliteState())
		writeEntry(t, root, &Entry{Entry: 1, ID: "elite", Repo: "getbururu/mod-elite", Official: true})
		run.listing = func(id, v string) *Listing {
			l := basicListing(run, id, v)
			mut(l)
			return l
		}
		changes, skipped := poll(t, root, gh, run)
		var mine []*Change
		for _, c := range changes {
			if c.ID == "gravel-rally" {
				mine = append(mine, c)
			}
		}
		t.Logf("%s: %v", name, skipped)
		if len(mine) != 0 || !slices.ContainsFunc(skipped, func(s string) bool { return strings.HasPrefix(s, "gravel-rally 1.0.0") }) {
			t.Errorf("%s: %v %v", name, mine, skipped)
		}
	}
}

// TestBidiStripped: control and bidi characters never reach the state.
func TestBidiStripped(t *testing.T) {
	root, gh, run := rallySetup(t)
	run.listing = func(id, v string) *Listing {
		l := basicListing(run, id, v)
		l.Summary = "Haptics \u202efor\u202c a rally game.\x07"
		l.Notes = "- Line one\u2066.\r\n- Line two\x1b[31m."
		return l
	}
	changes, _ := poll(t, root, gh, run)
	if len(changes) != 1 {
		t.Fatal(changes)
	}
	st := changes[0].State
	if st.Mod.Summary != "Haptics for a rally game." || st.Versions["1.0.0"].Notes != "- Line one.\n- Line two[31m." {
		t.Fatalf("%q %q", st.Mod.Summary, st.Versions["1.0.0"].Notes)
	}
	if !TextOK(changes[0].Report) {
		t.Fatal("the report has a control character")
	}
	accept(t, root, changes)
}

// TestOwnerLoginRefreshed: a renamed owner account is only refreshed.
func TestOwnerLoginRefreshed(t *testing.T) {
	root, gh, run := rallySetup(t)
	changes, _ := poll(t, root, gh, run)
	accept(t, root, changes)
	gh.repos["rally-modder/gravel-rally"].Owner.Login = "rally-renamed"
	changes, _ = poll(t, root, gh, run)
	if len(changes) != 1 || changes[0].Kind != ChangeRefresh || changes[0].State.OwnerLogin != "rally-renamed" ||
		changes[0].State.Status != StatusListed {
		t.Fatalf("%+v", changes)
	}
	accept(t, root, changes)
	if st := load(t, root).States["gravel-rally"]; st.OwnerLogin != "rally-renamed" || st.OwnerID != 601 {
		t.Fatal(st)
	}
}

// TestPins: a pinned version must be the pinned pack.
func TestPins(t *testing.T) {
	root, gh, run := rallySetup(t)
	e := rallyEntry()
	e.Pins = map[string]Pin{"1.0.0": {Asset: "gravel-rally-1.0.0.brr", SHA256: strings.Repeat("2", 64), Size: 10}}
	writeEntry(t, root, e)
	_, skipped := poll(t, root, gh, run)
	if len(skipped) != 1 || !strings.Contains(skipped[0], "is not the pinned pack") {
		t.Fatal(skipped)
	}
	b := gh.files["/rally-modder/gravel-rally/releases/download/v1.0.0/gravel-rally-1.0.0.brr"]
	e.Pins["1.0.0"] = Pin{Asset: "gravel-rally-1.0.0.brr", SHA256: sha256Hex(b), Size: int64(len(b))}
	writeEntry(t, root, e)
	changes, _ := poll(t, root, gh, run)
	if len(changes) != 1 || !strings.Contains(changes[0].Report, "matches the entry's pin") {
		t.Fatal(changes)
	}
}

// TestAppendOnly: the writing job refuses a proposal that changes an
// accepted version or drops one, or a version change that also holds.
func TestAppendOnly(t *testing.T) {
	root, gh, run := rallySetup(t)
	changes, _ := poll(t, root, gh, run)
	accept(t, root, changes)
	gh.release("rally-modder/gravel-rally", "v1.1.0", "gravel-rally-1.1.0.brr", makePack(t, map[string]string{"x.json": "{}"}), nil)
	changes, _ = poll(t, root, gh, run)
	if len(changes) != 1 {
		t.Fatal(changes)
	}
	r := load(t, root)
	try := func(what string, mut func(st *State)) {
		c := *changes[0]
		c.State = cloneState(changes[0].State)
		mut(c.State)
		out := t.TempDir()
		if err := WriteChanges(out, []*Change{&c}, nil); err != nil {
			t.Fatal(err)
		}
		cf, err := ReadChanges(out)
		if err == nil {
			_, err = ReadProposal(out, cf.List[0], r)
		}
		if err == nil {
			t.Errorf("%s: taken", what)
		}
	}
	try("a changed hash", func(st *State) {
		v := st.Versions["1.0.0"]
		v.SHA256 = strings.Repeat("3", 64)
		st.Versions["1.0.0"] = v
	})
	try("a dropped version", func(st *State) { delete(st.Versions, "1.0.0") })
	try("a status change", func(st *State) { st.Status, st.Note = StatusHold, "x" })
	try("another repo id", func(st *State) { st.RepoID = 1 })
	try("a reserved name", func(st *State) { st.Mod.Authors = []string{"Bururu"} })
	try("two new versions", func(st *State) { st.Versions["1.2.0"] = st.Versions["1.1.0"] })
}

// TestGone: a missing repo is held with the day, and removed after 30
// days; an archived one is removed at once.
func TestGone(t *testing.T) {
	root, gh, run := rallySetup(t)
	changes, _ := poll(t, root, gh, run)
	accept(t, root, changes)
	delete(gh.repos, "rally-modder/gravel-rally")
	changes, _ = poll(t, root, gh, run)
	if len(changes) != 1 || changes[0].Kind != ChangeGone || changes[0].State.GoneSince != DateOf(t0) {
		t.Fatalf("%+v", changes)
	}
	accept(t, root, changes)
	// held: the poll waits for a maintainer, unless 30 days pass
	p := testPoller(t, root, gh, run)
	p.Now = t0.AddDate(0, 0, 31)
	st := cloneState(p.Repo.States["gravel-rally"])
	got := p.gone(st, DateOf(p.Now))
	if len(got) != 1 || got[0].State.Status != StatusRemoved {
		t.Fatalf("%+v", got)
	}
	root2, gh2, run2 := rallySetup(t)
	changes, _ = poll(t, root2, gh2, run2)
	accept(t, root2, changes)
	gh2.repos["rally-modder/gravel-rally"].Archived = true
	changes, _ = poll(t, root2, gh2, run2)
	if len(changes) != 1 || changes[0].State.Status != StatusRemoved || changes[0].State.Note != "No longer maintained." {
		t.Fatalf("%+v", changes)
	}
	accept(t, root2, changes)
}

// TestPrereleases: test versions only when the entry takes them; a
// release GitHub marks as a pre-release with a plain version is a
// release.
func TestPrereleases(t *testing.T) {
	root, gh, run := rallySetup(t)
	gh.release("rally-modder/gravel-rally", "v1.1.0-beta.1", "gravel-rally-1.1.0-beta.1.brr", makePack(t, map[string]string{"p.json": "{}"}), nil)
	changes, _ := poll(t, root, gh, run)
	if len(changes) != 1 || changes[0].Version != "1.0.0" {
		t.Fatalf("%v", changes)
	}
	e := rallyEntry()
	e.Prerelease = true
	writeEntry(t, root, e)
	changes, _ = poll(t, root, gh, run)
	if len(changes) != 1 || changes[0].Version != "1.1.0-beta.1" || !changes[0].State.Versions["1.1.0-beta.1"].Prerelease {
		t.Fatalf("%v", changes)
	}
	root2, gh2, run2 := rallySetup(t)
	gh2.releases["rally-modder/gravel-rally"][0].Prerelease = true
	changes, _ = poll(t, root2, gh2, run2)
	if len(changes) != 1 || changes[0].State.Versions["1.0.0"].Prerelease {
		t.Fatalf("%v", changes)
	}
}

// TestETags: a second poll with the cache sends the ETags and gets 304s.
func TestETags(t *testing.T) {
	root, gh, run := rallySetup(t)
	cache := t.TempDir()
	p := testPoller(t, root, gh, run)
	p.GH = gh.client(cache)
	e := p.Repo.Entries["gravel-rally"]
	if _, _, err := p.Poll(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if gh.notMod != 0 {
		t.Fatal(gh.notMod)
	}
	if _, _, err := p.Poll(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if gh.notMod < 2 {
		t.Fatalf("%d answers were 304", gh.notMod)
	}
}

// TestDependenciesChecked: a required listed dependency is handed to mod
// check with --with.
func TestDependenciesChecked(t *testing.T) {
	root, gh, run := rallySetup(t)
	changes, _ := poll(t, root, gh, run)
	accept(t, root, changes)
	gh.repo("rally-modder/rally-sounds", 502, 601)
	writeEntry(t, root, &Entry{Entry: 1, ID: "rally-sounds", Repo: "rally-modder/rally-sounds"})
	run.listing = func(id, v string) *Listing {
		l := basicListing(run, id, v)
		if id == "rally-sounds" {
			l.Kind, l.Games, l.For = KindAddon, []string{}, []string{"gravel-rally"}
			l.Name = "Rally Sounds"
			l.Dependencies = map[string]Dependency{"gravel-rally": {Kind: DepRequired, Version: ">=1.0.0 <2.0.0"}}
		}
		return l
	}
	gh.release("rally-modder/rally-sounds", "v0.1.0", "rally-sounds-0.1.0.brr", makePack(t, map[string]string{"m.json": "{}"}), nil)
	run.with = nil
	changes, skipped := poll(t, root, gh, run)
	if len(changes) != 1 || len(skipped) != 0 {
		t.Fatalf("%v %v", changes, skipped)
	}
	if len(run.with) != 1 || len(run.with[0]) != 1 || filepath.Base(run.with[0][0]) != "gravel-rally-1.0.0.brr" {
		t.Fatalf("%v", run.with)
	}
	if !strings.Contains(changes[0].Report, "+ gravel-rally: required >=1.0.0 <2.0.0") {
		t.Fatal(changes[0].Report)
	}
}

func writeEntry(t *testing.T, root string, e *Entry) {
	t.Helper()
	if err := writeJSON(filepath.Join(root, "entries", e.ID+".json"), e, "  "); err != nil {
		t.Fatal(err)
	}
}

func writeState(t *testing.T, root string, s *State) {
	t.Helper()
	if err := writeJSON(filepath.Join(root, "state", s.ID+".json"), s, "  "); err != nil {
		t.Fatal(err)
	}
}

// eliteState is a listed official Elite, as the bot would have written
// it.
func eliteState() *State {
	return &State{State: StateFormat, ID: "elite", Repo: "getbururu/mod-elite", RepoID: 101, OwnerID: 201, OwnerLogin: "getbururu",
		Status: StatusListed, Mod: Meta{Name: "Elite Dangerous", Summary: "Haptics, adaptive triggers and lights for Elite Dangerous.",
			Authors: []string{"Bururu"}, Kind: KindGame, Games: []string{"elite-dangerous"}, For: []string{}, Provides: []string{},
			License: "MIT", Icon: Icon{Builtin: "ship"}},
		Versions: map[string]StateVersion{"0.9.0": {URL: "https://github.com/getbururu/mod-elite/releases/download/v0.9.0/elite-0.9.0.brr",
			Size: 140130, SHA256: "0d173d10406c5b8150a65a643013114e20e2dbfee3dcf785006a0c6cd56acfc8", API: "0.9",
			OS: []string{"windows", "linux"}, Published: t0.Add(-96 * 3600e9),
			Commit: strings.Repeat("c", 40), Dependencies: map[string]Dependency{},
			Permissions: Permissions{Files: map[string]PathDecl{"journal": {Path: "{SavedGames}/Frontier Developments/Elite Dangerous",
				Proton: 359320, Setting: "journal_dir", Label: "Journal"}}, Paths: []string{}, Screen: true, Keyboard: true,
				UDP: []int{}, UDPForward: []string{}},
			Notes: "The first release of the Elite Dangerous mod on its own.", Checks: Checks{Result: CheckPass, CheckedBy: "test"},
			Review: StateReview{By: ReviewBy, Diff: DiffFirst}}}}
}
