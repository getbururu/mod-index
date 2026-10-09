package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestLuaFlags: each Lua flag fires on its code and stays quiet on
// plain code, comments, field names and the file's own locals.
func TestLuaFlags(t *testing.T) {
	src := `-- load() in a comment is fine
--[[ and loadstring in a long comment ]]
local m = {}
function m.tick(self)
  self.load = 1
  local f = load("return 1")
  local d = string.dump(f)
  rawset(string, "x", 1)
  rawset(m, "y", 2)
  local s = "\104\101\108\108\111\032\119\111\114\108\100"
  local b = "QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVpBQkNERUZHSElKS0xNTk9QUVJTVFVWV1hZWkFCQ0RFRkdISUpLTE1OT1BRUlNUVVZXWFla"
  local _0x1f2e = 3
  local IlIlI = 4
  return debug
end
local function getfenv(x) return x end
return getfenv(m)
`
	got := luaFlags("lua/main.lua", []byte(src))
	want := []string{
		"lua/main.lua:6: names load, a global Bururu's sandbox removes",
		"lua/main.lua:7: names string.dump",
		"lua/main.lua:8: rawset on the library table string",
		"lua/main.lua:10: a long run of \\ddd escapes",
		"lua/main.lua:11: text that looks like base64",
		"lua/main.lua:12: the machine-looking name _0x1f2e",
		"lua/main.lua:13: the machine-looking name IlIlI",
		"lua/main.lua:14: names debug, a global Bururu's sandbox removes",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	long := "local s = [[" + strings.Repeat("x", 1100) + "]]\n"
	if f := luaFlags("a.lua", []byte(long)); len(f) != 1 || !strings.Contains(f[0], "a string literal of 1100 bytes") {
		t.Fatal(f)
	}
	if f := luaFlags("b.lua", []byte(strings.Repeat("-- x\n", 20000))); len(f) != 1 || !strings.Contains(f[0], "more than 65536") {
		t.Fatal(f)
	}
}

// TestLineDiff: changed lines with two lines of context, hunks apart.
func TestLineDiff(t *testing.T) {
	a := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n"
	b := "1\n2\nthree\n4\n5\n6\n7\n8\n9\n10\n11\neleven-and-a-half\n12\n"
	got := lineDiff("x.lua", a, b)
	want := "--- x.lua (before)\n+++ x.lua (after)\n@@ line 1 before, line 1 after @@\n 1\n 2\n-3\n+three\n 4\n 5\n" +
		"@@ line 10 before, line 10 after @@\n 10\n 11\n+eleven-and-a-half\n 12\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if d := lineDiff("new.lua", "", "a\nb\n"); !strings.Contains(d, "+a\n+b\n") {
		t.Fatal(d)
	}
}

// TestConfusable: the folds and the distances by length.
func TestConfusable(t *testing.T) {
	yes := [][2]string{{"Bururu", "8ururu"}, {"rnodder", "modder"}, {"vvheels", "wheels"}, {"Rally Modder", "rally-modder"},
		{"Frontier Developments", "Frontier Deve1opments"}, {"lua", "LUA"}, {"elite", "e1ite"}, {"gravel-rally", "gravel-ralley"}}
	no := [][2]string{{"lua", "lub"}, {"rally", "relax"}, {"Elite Dangerous", "Elite Tweaks"}, {"Bururu", "Burgers"}}
	for _, p := range yes {
		if !confusable(p[0], p[1]) {
			t.Errorf("%q and %q should look alike", p[0], p[1])
		}
	}
	for _, p := range no {
		if confusable(p[0], p[1]) {
			t.Errorf("%q and %q should not look alike", p[0], p[1])
		}
	}
	if editDistance("kitten", "sitting") != 3 {
		t.Fatal(editDistance("kitten", "sitting"))
	}
}

// TestStrip: what TextOK refuses goes, tabs and newlines stay.
func TestStrip(t *testing.T) {
	// two bidi controls (U+202E, U+2069), a C1 control (U+0085), a bad byte
	in := "a\tb\r\nc\x00d" + "\xe2\x80\xae" + "e" + "\xe2\x81\xa9" + "f" + "\xc2\x85" + "g\xffh"
	if got := Strip(in); got != "a\tb\ncdefgh" || !TextOK(got) {
		t.Fatalf("%q", got)
	}
	if got := StripLine(" one\ttwo\nthree "); got != "one two three" {
		t.Fatalf("%q", got)
	}
	if !NameOK("Rally Modder") || NameOK(" Rally") || NameOK("R"+"\xc3\xa4"+"lly") || NameOK(strings.Repeat("x", 65)) {
		t.Fatal("NameOK")
	}
}

// TestRepoData: the repo's own data files read.
func TestRepoData(t *testing.T) {
	r, probs := LoadRepo(filepath.Join("..", ".."))
	if len(probs) > 0 {
		t.Fatalf("problems:\n%s", strings.Join(probs, "\n"))
	}
	if g := r.Games["elite-dangerous"]; g.Name != "Elite Dangerous" || len(g.Stores.Steam) != 1 {
		t.Fatalf("games/elite-dangerous.json: %+v", g)
	}
	e := r.Entries["elite"]
	if e == nil || !e.Official || e.Repo != "getbururu/mod-elite" {
		t.Fatalf("%+v", e)
	}
	ref, err := os.ReadFile(filepath.Join("..", "bururu.ref"))
	if err != nil {
		t.Fatal(err)
	}
	if s := strings.TrimSpace(string(ref)); !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,99}$`).MatchString(s) {
		t.Fatalf("tools/bururu.ref holds %q: a branch, a tag or a commit of Bururu's repo", s)
	}
}

// TestReadPack: one top folder is taken off, Lua kept, bad paths refused.
func TestReadPack(t *testing.T) {
	files, err := readPack(makePack(t, map[string]string{"mod/manifest.json": "{}", "mod/lua/a.lua": "x"}))
	if err != nil || len(files) != 2 || files[0].Path != "lua/a.lua" || string(files[0].Data) != "x" || files[1].Data != nil {
		t.Fatalf("%+v %v", files, err)
	}
	if _, err := readPack(makePack(t, map[string]string{"../x": "1"})); err == nil {
		t.Fatal("../x was taken")
	}
}

// TestStats: downloads of the listed packs summed, stars copied.
func TestStats(t *testing.T) {
	root, gh, run := rallySetup(t)
	changes, _ := poll(t, root, gh, run)
	accept(t, root, changes)
	gh.repos["rally-modder/gravel-rally"].Stars = 41
	st, notes, err := CountStats(context.Background(), gh.client(""), load(t, root), t0)
	if err != nil || len(notes) != 0 {
		t.Fatal(err, notes)
	}
	m := st.Mods["gravel-rally"]
	if !st.Generated.Equal(t0) || m.Stars != 41 || m.Downloads != 7 || m.Versions["1.0.0"] != 7 {
		t.Fatalf("%+v", st)
	}
	b, _ := encode(st, "")
	if _, err := readStats(b); err != nil {
		t.Fatal(err)
	}
}

// TestCheckPullRequest: a new entry passes with its release's report; a
// non-maintainer may not set official or remove an entry; broken data
// fails.
func TestCheckPullRequest(t *testing.T) {
	base := testRepo(t)
	gh := newFakeGitHub(t)
	gh.repo("rally-modder/gravel-rally", 501, 601)
	run := &fakeRunner{}
	run.listing = func(id, v string) *Listing { return basicListing(run, id, v) }
	gh.release("rally-modder/gravel-rally", "v1.0.0", "gravel-rally-1.0.0.brr", makePack(t, map[string]string{"m.json": "{}"}), nil)
	check := func(e *Entry, assoc string, mut func(pr string)) *EntryCheck {
		pr := testRepo(t, e)
		if mut != nil {
			mut(pr)
		}
		p := &Poller{GH: gh.client(""), Run: run, Ref: "test", Work: t.TempDir(), Now: t0}
		return CheckPullRequest(context.Background(), p, base, pr, "rally-modder", assoc)
	}
	res := check(rallyEntry(), "NONE", nil)
	if !res.Pass || !strings.Contains(res.Report, "gravel-rally 1.0.0 passes every check") || !strings.Contains(res.Report, "+ udp:20777") {
		t.Fatalf("%s", res.Report)
	}
	e := rallyEntry()
	e.Official = true
	if res := check(e, "CONTRIBUTOR", nil); res.Pass || !strings.Contains(res.Report, "only the list's maintainers set official") {
		t.Fatalf("%s", res.Report)
	}
	if res := check(e, "OWNER", nil); !res.Pass {
		t.Fatalf("%s", res.Report)
	}
	if res := check(rallyEntry(), "NONE", func(pr string) { os.WriteFile(filepath.Join(pr, "policy", "client.json"), []byte("{"), 0o644) }); res.Pass {
		t.Fatalf("%s", res.Report)
	}
	gone := rallyEntry()
	gone.Repo = "rally-modder/nothing-here"
	if res := check(gone, "NONE", nil); res.Pass || !strings.Contains(res.Report, "is not on GitHub") {
		t.Fatalf("%s", res.Report)
	}
	// without Bururu the pull request's check looks for the pack only;
	// the poll checks it after the merge
	p0 := &Poller{GH: gh.client(""), Ref: "test", Work: t.TempDir(), Now: t0}
	if res := CheckPullRequest(context.Background(), p0, base, testRepo(t, rallyEntry()), "rally-modder", "NONE"); !res.Pass ||
		!strings.Contains(res.Report, "the poll checks the pack after the merge") {
		t.Fatalf("%s", res.Report)
	}
	// removing an entry
	base2 := testRepo(t, rallyEntry())
	p := &Poller{GH: gh.client(""), Run: run, Ref: "test", Work: t.TempDir(), Now: t0}
	if res := CheckPullRequest(context.Background(), p, base2, testRepo(t), "rally-modder", "NONE"); res.Pass {
		t.Fatalf("%s", res.Report)
	}
}

// TestEliteEntry polls the real entries/elite.json against a fake
// GitHub that serves a real elite-<version>.brr and the real `bururu mod
// listing` of it, and builds the index. It runs when INDEXER_ELITE is a
// folder holding one elite-<version>.brr, listing.json and
// text/<sha>.txt; with INDEXER_ELITE_OUT it also writes the site there
// for a check with Bururu's own reader.
func TestEliteEntry(t *testing.T) {
	dir := os.Getenv("INDEXER_ELITE")
	if dir == "" {
		t.Skip("INDEXER_ELITE names no folder with the Elite pack and its listing")
	}
	found, _ := filepath.Glob(filepath.Join(dir, "elite-*"+PackExt))
	if len(found) != 1 {
		t.Fatalf("INDEXER_ELITE holds %d elite packs, want 1", len(found))
	}
	name := filepath.Base(found[0])
	ver := strings.TrimSuffix(strings.TrimPrefix(name, "elite-"), PackExt)
	pack, err := os.ReadFile(found[0])
	if err != nil {
		t.Fatal(err)
	}
	lb, err := os.ReadFile(filepath.Join(dir, "listing.json"))
	if err != nil {
		t.Fatal(err)
	}
	real, _ := LoadRepo(filepath.Join("..", ".."))
	root := testRepo(t, real.Entries["elite"])
	gh := newFakeGitHub(t)
	gh.repo("getbururu/mod-elite", 1000001, 2000002)
	// published as a GitHub pre-release, as the mod's release steps do
	gh.release("getbururu/mod-elite", "v"+ver, name, pack, nil)
	run := &fakeRunner{texts: map[string]string{}}
	run.listing = func(id, v string) *Listing {
		var l Listing
		if err := json.Unmarshal(lb, &l); err != nil {
			t.Fatal(err)
		}
		return &l
	}
	var refs struct{ Readme, Changelog *FileRef }
	if err := json.Unmarshal(lb, &refs); err != nil {
		t.Fatal(err)
	}
	for _, f := range []*FileRef{refs.Readme, refs.Changelog} {
		b, err := os.ReadFile(filepath.Join(dir, "text", f.SHA256+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		run.texts[f.SHA256] = string(b)
	}
	changes, skipped := poll(t, root, gh, run)
	if len(changes) != 1 || len(skipped) != 0 {
		t.Fatalf("%v %v", changes, skipped)
	}
	c := changes[0]
	t.Logf("%s\n%s", c.Title, c.Report)
	if c.Title != "List elite "+ver || c.State.Versions[ver].Prerelease {
		t.Fatal(c.Title)
	}
	accept(t, root, changes)
	b, err := BuildIndex(load(t, root), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readIndex(b.File); err != nil {
		t.Fatal(err)
	}
	v := b.Index.Mods["elite"].Versions[ver]
	if v.SHA256 != sha256Hex(pack) || v.Size != int64(len(pack)) || !b.Index.Mods["elite"].Official {
		t.Fatalf("%+v", v)
	}
	if out := os.Getenv("INDEXER_ELITE_OUT"); out != "" {
		files := map[string][]byte{IndexFile: b.File}
		for name, fb := range b.Files {
			files[name] = fb
		}
		st, _, err := CountStats(context.Background(), gh.client(""), load(t, root), t0.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		files[StatsFile], _ = encode(st, "")
		if err := os.MkdirAll(out, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := WriteSite(out, files); err != nil {
			t.Fatal(err)
		}
	}
}
