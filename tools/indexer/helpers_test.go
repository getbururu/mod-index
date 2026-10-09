package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// t0 is the tests' now.
var t0 = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

// fakeGitHub answers the API calls and release downloads the indexer
// makes, from recorded answers the test sets up.
type fakeGitHub struct {
	mu       sync.Mutex
	srv      *httptest.Server
	repos    map[string]*RepoInfo
	releases map[string][]Release
	commits  map[string]string // "<repo>@<tag>"
	files    map[string][]byte // "/<repo>/releases/download/<tag>/<name>"
	hits     map[string]int
	notMod   int // 304 answers
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{repos: map[string]*RepoInfo{}, releases: map[string][]Release{}, commits: map[string]string{},
		files: map[string][]byte{}, hits: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits[r.URL.Path]++
	if strings.Contains(r.URL.Path, "/releases/download/") {
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "a download carries no token", http.StatusBadRequest)
			return
		}
		b, ok := f.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
		return
	}
	var v any
	p := strings.TrimPrefix(r.URL.Path, "/repos/")
	switch {
	case strings.HasSuffix(p, "/releases"):
		v = f.releases[strings.TrimSuffix(p, "/releases")]
		if v == nil {
			v = []Release{}
		}
	case strings.Contains(p, "/commits/"):
		repo, tag, _ := strings.Cut(p, "/commits/")
		sha, ok := f.commits[repo+"@"+tag]
		if !ok {
			http.NotFound(w, r)
			return
		}
		v = map[string]string{"sha": sha}
	default:
		info, ok := f.repos[p]
		if !ok {
			http.NotFound(w, r)
			return
		}
		v = info
	}
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		f.notMod++
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(b)
}

// client is a GitHub that talks to the fake.
func (f *fakeGitHub) client(cache string) *GitHub {
	return &GitHub{API: f.srv.URL, Token: "test-token", Cache: cache, Hosts: []string{strings.TrimPrefix(f.srv.URL, "http://")},
		Loopback: true, ReleaseBase: f.srv.URL}
}

// repo adds a repo.
func (f *fakeGitHub) repo(full string, id, ownerID int64) *RepoInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	info := &RepoInfo{ID: id, FullName: full}
	info.Owner.ID = ownerID
	info.Owner.Login, _, _ = strings.Cut(full, "/")
	f.repos[full] = info
	return info
}

// release adds a release of repo with one pack.
func (f *fakeGitHub) release(repo, tag string, pack string, b []byte, mut func(*Release)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := "/" + repo + "/releases/download/" + tag + "/" + pack
	f.files[path] = b
	sum := sha256.Sum256(b)
	r := Release{ID: int64(len(f.releases[repo]) + 1), TagName: tag, PublishedAt: t0.Add(-72 * time.Hour),
		Prerelease: strings.Contains(tag, "-"),
		Assets: []Asset{{Name: pack, Size: int64(len(b)), State: "uploaded", Digest: "sha256:" + hex.EncodeToString(sum[:]),
			DownloadCount: 7, URL: "https://github.com" + path}}}
	if mut != nil {
		mut(&r)
	}
	f.releases[repo] = append([]Release{r}, f.releases[repo]...)
	f.commits[repo+"@"+tag] = strings.Repeat("a", 39) + fmt.Sprint(len(f.releases[repo])%10)
}

// makePack is a zip of files, as a pack.
func makePack(t testing.TB, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range sortedKeys(files) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(files[name]))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fakeRunner stands for the bururu program: its listing of a pack comes
// from the test, with the pack's own hash and size.
type fakeRunner struct {
	listing func(id, version string) *Listing
	texts   map[string]string // published texts by sha256
	check   func() *CheckResult
	with    [][]string // the --with lists it was given
}

func (r *fakeRunner) Listing(_ context.Context, pack, iconOut, textOut string) (*Listing, error) {
	base := strings.TrimSuffix(filepath.Base(pack), PackExt)
	// <id>-<version>: the version starts at the first "-<digit>"
	id, ver := base, ""
	for j := 0; j < len(base)-1; j++ {
		if base[j] == '-' && base[j+1] >= '0' && base[j+1] <= '9' {
			id, ver = base[:j], base[j+1:]
			break
		}
	}
	l := r.listing(id, ver)
	b, err := os.ReadFile(pack)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	l.SHA256, l.Size = hex.EncodeToString(sum[:]), int64(len(b))
	for _, ref := range []*FileRef{l.Readme, l.Changelog} {
		if ref != nil {
			if err := writeFile(filepath.Join(textOut, ref.SHA256+".txt"), []byte(r.texts[ref.SHA256])); err != nil {
				return nil, err
			}
		}
	}
	return l, nil
}

func (r *fakeRunner) Check(_ context.Context, pack string, with []string) (*CheckResult, error) {
	r.with = append(r.with, with)
	if r.check != nil {
		return r.check(), nil
	}
	return &CheckResult{Check: 1, Result: CheckPass, CheckedBy: "test"}, nil
}

// textRef is a published text and its pin.
func textRef(r *fakeRunner, s string) *FileRef {
	sum := sha256.Sum256([]byte(s))
	h := hex.EncodeToString(sum[:])
	if r.texts == nil {
		r.texts = map[string]string{}
	}
	r.texts[h] = s
	return &FileRef{SHA256: h, Size: int64(len(s))}
}

// basicListing is a community game mod's listing.
func basicListing(r *fakeRunner, id, version string) *Listing {
	return &Listing{Listing: 1, ID: id, Version: version, Name: "Gravel Rally", Summary: "Haptics for a rally game.",
		Authors: []string{"Rally Modder"}, Kind: KindGame, Games: []string{"rally-game"}, For: []string{}, Provides: []string{},
		License: "MIT", API: "0.9", OS: []string{}, Icon: Icon{Builtin: "car"},
		Readme:       textRef(r, "# Gravel Rally\n\nFeels for a rally game.\n"),
		Changelog:    textRef(r, "## "+version+"\n\n- Changes of "+version+".\n"),
		Notes:        "- Changes of " + version + ".",
		Dependencies: map[string]Dependency{},
		Permissions: Permissions{Files: map[string]PathDecl{"telemetry": {Path: "{SavedGames}/Rally/telemetry"}},
			Paths: []string{}, UDP: []int{20777}, UDPForward: []string{}},
		GameRecords: map[string]GameRecord{}, Problems: []string{}, Reserved: []string{}, Warnings: []string{}}
}

// testRepo writes a repo with the real policy and games files and the
// entries given, and loads it.
func testRepo(t *testing.T, entries ...*Entry) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range []string{"policy/reserved.json", "policy/revoked.json", "policy/advisories.json", "policy/client.json",
		"games/elite-dangerous.json"} {
		b, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		if err := writeFile(filepath.Join(root, filepath.FromSlash(f)), b); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range entries {
		if err := writeJSON(filepath.Join(root, "entries", e.ID+".json"), e, "  "); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// load reads a repo and fails the test on any problem.
func load(t *testing.T, root string) *Repo {
	t.Helper()
	r, probs := LoadRepo(root)
	if len(probs) > 0 {
		t.Fatalf("the repo has problems:\n%s", strings.Join(probs, "\n"))
	}
	return r
}

// testPoller is a Poller on the fake GitHub and runner.
func testPoller(t *testing.T, root string, gh *fakeGitHub, run Runner) *Poller {
	return &Poller{Repo: load(t, root), GH: gh.client(""), Run: run, Ref: "0123456789abcdef0123456789abcdef01234567",
		Work: t.TempDir(), Now: t0}
}

// accept writes a poll's changes into the repo as a merged bot pull
// request would, through the strict readers.
func accept(t *testing.T, root string, changes []*Change) {
	t.Helper()
	out := t.TempDir()
	if err := WriteChanges(out, changes, nil); err != nil {
		t.Fatal(err)
	}
	cf, err := ReadChanges(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range cf.List {
		p, err := ReadProposal(out, h, load(t, root))
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Apply(root); err != nil {
			t.Fatal(err)
		}
	}
	load(t, root)
}

// rallyEntry is a community entry.
func rallyEntry() *Entry {
	return &Entry{Entry: 1, ID: "gravel-rally", Repo: "rally-modder/gravel-rally"}
}
