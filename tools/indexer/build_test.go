package main

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// listedRepo is a repo with Elite and a community mod accepted.
func listedRepo(t *testing.T) string {
	t.Helper()
	root, gh, run := rallySetup(t)
	changes, _ := poll(t, root, gh, run)
	accept(t, root, changes)
	writeEntry(t, root, &Entry{Entry: 1, ID: "elite", Repo: "getbururu/mod-elite", Official: true, Prerelease: true})
	writeState(t, root, eliteState())
	return root
}

// TestBuildDeterministic: the same repo gives the same bytes, with
// sorted keys and no float, and Bururu's reader takes them.
func TestBuildDeterministic(t *testing.T) {
	root := listedRepo(t)
	a, err := BuildIndex(load(t, root), IndexFormat, nil, DateOf(t0))
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildIndex(load(t, root), IndexFormat, nil, DateOf(t0))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.File, b.File) || a.Same {
		t.Fatal("two builds differ")
	}
	if !bytes.HasPrefix(a.File, []byte(`{"advisories":[],"games":{"elite-dangerous":{"controller":{"bluetooth":"dualshock4","cable":"dualshock4"},"exe":`)) || bytes.Count(a.File, []byte("\n")) != 1 {
		t.Fatalf("%.200s", a.File)
	}
	ix := a.Index
	if ix.Serial != 1 || len(ix.Mods) != 2 || !ix.Mods["elite"].Official || ix.Mods["gravel-rally"].Official {
		t.Fatalf("%+v", ix.Mods)
	}
	if v := ix.Mods["elite"].Versions["0.9.0"]; v.Prerelease || v.SHA256 != "0d173d10406c5b8150a65a643013114e20e2dbfee3dcf785006a0c6cd56acfc8" {
		t.Fatalf("%+v", v)
	}
	if len(a.Files) != 2 {
		t.Fatalf("%d published files", len(a.Files))
	}
	if _, err := readIndex(a.File); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"signed_at", "expires", "available_from", "immutable", "review", "checks", "listed"} {
		if bytes.Contains(a.File, []byte(`"`+gone+`":`)) {
			t.Errorf("the index has %s", gone)
		}
	}
}

// TestBuildAgainstPublished: the serial grows only when the list
// changed, and a published version keeps its hash.
func TestBuildAgainstPublished(t *testing.T) {
	root := listedRepo(t)
	r := load(t, root)
	prev, err := BuildIndex(r, IndexFormat, nil, DateOf(t0))
	if err != nil {
		t.Fatal(err)
	}
	same, err := BuildIndex(r, IndexFormat, prev.Index, DateOf(t0))
	if err != nil || !same.Same || same.Index.Serial != 1 || !bytes.Equal(same.File, prev.File) {
		t.Fatalf("nothing changed: %v %+v", err, same)
	}
	r.Client.MinClient = "0.9.0"
	next, err := BuildIndex(r, IndexFormat, prev.Index, DateOf(t0))
	if err != nil || next.Same || next.Index.Serial != 2 {
		t.Fatalf("a change: %v %+v", err, next)
	}
	st := r.States["gravel-rally"]
	v := st.Versions["1.0.0"]
	v.Size++
	st.Versions["1.0.0"] = v
	if _, err := BuildIndex(r, IndexFormat, prev.Index, DateOf(t0)); err == nil || !strings.Contains(err.Error(), "another hash, size or link") {
		t.Fatal(err)
	}
	delete(st.Versions, "1.0.0")
	if _, err := BuildIndex(r, IndexFormat, prev.Index, DateOf(t0)); err == nil || !strings.Contains(err.Error(), "is gone") {
		t.Fatal(err)
	}
}

// TestSite: the site takes the built files and the stats into v1/ and
// v2/, reads back, keeps nothing else at the top of either, and leaves
// the rest of the pages branch alone.
func TestSite(t *testing.T) {
	root := listedRepo(t)
	site := t.TempDir()
	v1 := filepath.Join(site, SiteV1)
	if err := os.MkdirAll(v1, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, old := range []string{"index-3.json", "index.minisig", "keys.json"} {
		os.WriteFile(filepath.Join(v1, old), []byte("{}"), 0o644)
	}
	os.WriteFile(filepath.Join(site, "README.md"), []byte("pages\n"), 0o644)
	if ix, err := ReadSite(v1, IndexFormatV1); err != nil || ix != nil {
		t.Fatalf("no index yet: %v %v", ix, err)
	}
	files := map[string][]byte{}
	for _, f := range []struct {
		dir    string
		format int
	}{{SiteV1, IndexFormatV1}, {SiteV2, IndexFormat}} {
		b, err := BuildIndex(load(t, root), f.format, nil, DateOf(t0))
		if err != nil {
			t.Fatal(err)
		}
		files[f.dir+"/"+IndexFile] = b.File
		for k, v := range b.Files {
			files[f.dir+"/"+k] = v
		}
	}
	if err := WriteSite(site, files); err != nil {
		t.Fatal(err)
	}
	stats := []byte(`{"stats":1,"generated":"2026-11-02T12:00:00Z","mods":{}}`)
	if err := WriteSite(site, map[string][]byte{"v1/" + StatsFile: stats, "v2/" + StatsFile: stats}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"", SiteV1, SiteV2} {
		ents, _ := os.ReadDir(filepath.Join(site, dir))
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		want := "index.json stats.json text"
		if dir == "" {
			want = "README.md v1 v2"
		}
		if strings.Join(names, " ") != want {
			t.Fatalf("%s: %v, want %s", dir, names, want)
		}
	}
	ix, err := ReadSite(v1, IndexFormatV1)
	if err != nil || ix == nil || ix.Serial != 1 || ix.Index != IndexFormatV1 {
		t.Fatal(ix, err)
	}
	if ix, err := ReadSite(filepath.Join(site, SiteV2), IndexFormat); err != nil || ix == nil || ix.Index != IndexFormat {
		t.Fatal(ix, err)
	}
	if _, err := ReadSite(v1, IndexFormat); err == nil || !strings.Contains(err.Error(), "has format 1, not 2") {
		t.Fatal(err)
	}
	for _, bad := range []string{"../x.json", "index.json", "v3/index.json", "v1/../x.json"} {
		if err := WriteSite(site, map[string][]byte{bad: []byte("{}")}); err == nil {
			t.Fatalf("the site took %s", bad)
		}
	}
}

// TestPublishCommands runs build and site as publish.yml does, once a
// day: the first run writes both lists, a day with nothing new writes
// nothing, and the free day of an early-access version writes v1/ only.
func TestPublishCommands(t *testing.T) {
	root := earlyRepo(t, "2026-11-12")
	site := t.TempDir()
	publish := func(now string) string {
		t.Helper()
		out := filepath.Join(t.TempDir(), "index")
		if err := run(context.Background(), "build", []string{"-root", root, "-site", site, "-out", out, "-now", now}); err != nil {
			t.Fatal(err)
		}
		var names []string
		filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				rel, _ := filepath.Rel(out, path)
				names = append(names, filepath.ToSlash(rel))
			}
			return nil
		})
		if len(names) > 0 {
			if err := run(context.Background(), "site", []string{"-in", out, "-site", site}); err != nil {
				t.Fatal(err)
			}
		}
		return strings.Join(names, " ")
	}
	if got := publish("2026-11-02T00:15:00Z"); got != "v1/index.json v2/index.json" {
		t.Fatalf("the first run wrote %q", got)
	}
	if got := publish("2026-11-03T00:15:00Z"); got != "" {
		t.Fatalf("a day with nothing new wrote %q", got)
	}
	if got := publish("2026-11-12T00:15:00Z"); got != "v1/index.json" {
		t.Fatalf("the free day wrote %q", got)
	}
	ix, err := ReadSite(filepath.Join(site, SiteV1), IndexFormatV1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ix.Mods["elite"].Versions["0.9.1"]; !ok || ix.Serial != 2 {
		t.Fatalf("v1 on the free day: serial %d, %+v", ix.Serial, ix.Mods["elite"].Versions)
	}
}
