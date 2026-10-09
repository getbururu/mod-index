package main

import (
	"bytes"
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
	a, err := BuildIndex(load(t, root), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildIndex(load(t, root), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.File, b.File) || a.Same {
		t.Fatal("two builds differ")
	}
	if !bytes.HasPrefix(a.File, []byte(`{"advisories":[],"games":{"elite-dangerous":{"exe":`)) || bytes.Count(a.File, []byte("\n")) != 1 {
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
	prev, err := BuildIndex(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	same, err := BuildIndex(r, prev.Index)
	if err != nil || !same.Same || same.Index.Serial != 1 || !bytes.Equal(same.File, prev.File) {
		t.Fatalf("nothing changed: %v %+v", err, same)
	}
	r.Client.MinClient = "0.9.0"
	next, err := BuildIndex(r, prev.Index)
	if err != nil || next.Same || next.Index.Serial != 2 {
		t.Fatalf("a change: %v %+v", err, next)
	}
	st := r.States["gravel-rally"]
	v := st.Versions["1.0.0"]
	v.Size++
	st.Versions["1.0.0"] = v
	if _, err := BuildIndex(r, prev.Index); err == nil || !strings.Contains(err.Error(), "another hash, size or link") {
		t.Fatal(err)
	}
	delete(st.Versions, "1.0.0")
	if _, err := BuildIndex(r, prev.Index); err == nil || !strings.Contains(err.Error(), "is gone") {
		t.Fatal(err)
	}
}

// TestSite: the site takes the built files and the stats, reads back,
// and keeps nothing else at the top of v1/.
func TestSite(t *testing.T) {
	root := listedRepo(t)
	v1 := filepath.Join(t.TempDir(), "v1")
	if err := os.MkdirAll(v1, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, old := range []string{"index-3.json", "index.minisig", "keys.json"} {
		os.WriteFile(filepath.Join(v1, old), []byte("{}"), 0o644)
	}
	if ix, err := ReadSite(v1); err != nil || ix != nil {
		t.Fatalf("no index yet: %v %v", ix, err)
	}
	b, err := BuildIndex(load(t, root), nil)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{IndexFile: b.File}
	for k, v := range b.Files {
		files[k] = v
	}
	if err := WriteSite(v1, files); err != nil {
		t.Fatal(err)
	}
	if err := WriteSite(v1, map[string][]byte{StatsFile: []byte(`{"stats":1,"generated":"2026-11-02T12:00:00Z","mods":{}}`)}); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(v1)
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if strings.Join(names, " ") != "index.json stats.json text" {
		t.Fatal(names)
	}
	ix, err := ReadSite(v1)
	if err != nil || ix == nil || ix.Serial != 1 {
		t.Fatal(ix, err)
	}
	if err := WriteSite(v1, map[string][]byte{"../x.json": []byte("{}")}); err == nil {
		t.Fatal("the site took ../x.json")
	}
}
