package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestBururuPin: tools/bururu.json is empty or a whole pin of an https
// AppImage or tarball, and the strict reader refuses anything else.
func TestBururuPin(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	pinned := `{
  "bururu": 1,
  "version": "1.0.0",
  "url": "https://github.com/getbururu/bururu/releases/download/v1.0.0/Bururu-x86_64.AppImage",
  "sha256": "` + sum + `",
  "path": "usr/bin/bururu"
}
`
	for _, tc := range []struct {
		name, from, to, want string
		pinned               bool
	}{
		{name: "as written"},
		{name: "an AppImage", to: pinned, pinned: true},
		{name: "a tarball", to: strings.Replace(strings.Replace(pinned, "Bururu-x86_64.AppImage", "bururu-1.0.0-linux-amd64.tar.gz", 1),
			"usr/bin/bururu", "bururu/bururu", 1), pinned: true},
		{name: "half a pin", from: `"version": ""`, to: `"version": "1.0.0"`, want: `url "" is not the https address`},
		{name: "plain http", from: "https://", to: "http://", want: `is not the https address of an .AppImage or a .tar.gz`},
		{name: "a zip", from: "Bururu-x86_64.AppImage", to: "Bururu-1.0.0.zip", want: `is not the https address of an .AppImage`},
		{name: "a query", from: "AppImage\"", to: "AppImage?x=1\"", want: `is not the https address`},
		{name: "upper-case hash", from: sum, to: strings.ToUpper(sum), want: `sha256 "` + strings.ToUpper(sum) + `" is not well formed`},
		{name: "a tag", from: `"version": "1.0.0"`, to: `"version": "v1.0.0"`, want: `version "v1.0.0" is not a version`},
		{name: "out of the build", from: `"usr/bin/bururu"`, to: `"../bururu"`, want: `path "../bururu" is not a path inside the build`},
		{name: "absolute", from: `"usr/bin/bururu"`, to: `"/usr/bin/bururu"`, want: `path "/usr/bin/bururu" is not a path`},
		{name: "unknown key", from: `"path": "usr/bin/bururu"`, to: `"path": "usr/bin/bururu", "size": 1`, want: `unknown field "size"`},
		{name: "format 2", from: `"bururu": 1`, to: `"bururu": 2`, want: `format 2; this indexer reads 1`},
		{name: "missing", want: "tools/bururu.json is missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := testRepo(t)
			file := filepath.Join(root, filepath.FromSlash(BururuPinFile))
			b, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			src := string(b)
			switch {
			case tc.name == "missing":
				os.Remove(file)
			case tc.from == "" && tc.to != "":
				src = tc.to
			case tc.from != "":
				if !strings.Contains(src, tc.from) {
					src = pinned
				}
				if !strings.Contains(src, tc.from) {
					t.Fatalf("the pin has no %q", tc.from)
				}
				src = strings.Replace(src, tc.from, tc.to, 1)
			}
			if tc.name != "missing" {
				if err := writeFile(file, []byte(src)); err != nil {
					t.Fatal(err)
				}
			}
			r, probs := LoadRepo(root)
			got := strings.Join(probs, "\n")
			switch {
			case tc.want == "" && got != "":
				t.Fatalf("problems:\n%s", got)
			case tc.want != "" && !strings.Contains(got, tc.want):
				t.Fatalf("problems %q, want %q", got, tc.want)
			case tc.want == "" && r.Bururu.Pinned() != tc.pinned:
				t.Fatalf("pinned %v: %+v", r.Bururu.Pinned(), r.Bururu)
			}
		})
	}
}

// buildServer serves files over https by path; its client trusts it.
func buildServer(t *testing.T, files map[string][]byte) *httptest.Server {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "a download carries no token", http.StatusBadRequest)
			return
		}
		if to, ok := strings.CutPrefix(r.URL.Path, "/redirect/"); ok {
			http.Redirect(w, r, to, http.StatusFound)
			return
		}
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// pinOf is a pin of the file b at the server's path p.
func pinOf(srv *httptest.Server, p string, b []byte, inner string) BururuPin {
	sum := sha256.Sum256(b)
	return BururuPin{Bururu: BururuPinFormat, Version: "1.0.0", URL: srv.URL + p, SHA256: hex.EncodeToString(sum[:]), Path: inner}
}

// tarEntry is one file of a test tarball.
type tarEntry struct {
	name, body string
	link       bool // a symlink to body
}

// makeTarGz is a .tar.gz of the entries.
func makeTarGz(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if e.link {
			h = &tar.Header{Name: e.name, Mode: 0o777, Linkname: e.body, Typeflag: tar.TypeSymlink}
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if !e.link {
			tw.Write([]byte(e.body))
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestFetchBururuTarball: the program comes out of a tarball whose
// SHA-256 is the pin's, and nothing else is taken.
func TestFetchBururuTarball(t *testing.T) {
	good := makeTarGz(t, tarEntry{name: "bururu/README.md", body: "# Bururu\n"},
		tarEntry{name: "./bururu/bururu", body: "the bururu program\n"})
	twice := makeTarGz(t, tarEntry{name: "bururu/bururu", body: "one\n"}, tarEntry{name: "bururu/bururu", body: "two\n"})
	link := makeTarGz(t, tarEntry{name: "bururu/bururu", body: "/bin/sh", link: true})
	files := map[string][]byte{"/v1.0.0/bururu.tar.gz": good, "/twice.tar.gz": twice, "/link.tar.gz": link,
		"/broken.tar.gz": []byte("not a gzip")}
	srv := buildServer(t, files)
	fetch := func(pin BururuPin, max int64) (string, error) {
		return FetchBururu(context.Background(), srv.Client(), pin, t.TempDir(), max)
	}
	prog, err := fetch(pinOf(srv, "/v1.0.0/bururu.tar.gz", good, "bururu/bururu"), MaxBuildBytes)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(prog); err != nil || string(b) != "the bururu program\n" || !filepath.IsAbs(prog) {
		t.Fatalf("%s: %q, %v", prog, b, err)
	}
	if st, _ := os.Stat(prog); runtime.GOOS != "windows" && st.Mode().Perm()&0o100 == 0 {
		t.Fatalf("%s may not be run: %v", prog, st.Mode())
	}
	wrong := pinOf(srv, "/v1.0.0/bururu.tar.gz", good, "bururu/bururu")
	wrong.SHA256 = strings.Repeat("0", 64)
	for _, tc := range []struct {
		name string
		pin  BururuPin
		max  int64
		want string
	}{
		{"another hash", wrong, MaxBuildBytes, "its SHA-256 is " + pinOf(srv, "", good, "").SHA256 + "; the pin says 0000"},
		{"too big", pinOf(srv, "/v1.0.0/bururu.tar.gz", good, "bururu/bururu"), 10, "more than 10"},
		{"not there", pinOf(srv, "/v9.9.9/bururu.tar.gz", good, "bururu/bururu"), MaxBuildBytes, "404 Not Found"},
		{"no program", pinOf(srv, "/v1.0.0/bururu.tar.gz", good, "bin/bururu"), MaxBuildBytes, "bururu.tar.gz has no bin/bururu"},
		{"twice", pinOf(srv, "/twice.tar.gz", twice, "bururu/bururu"), MaxBuildBytes, "holds bururu/bururu twice"},
		{"a symlink", pinOf(srv, "/link.tar.gz", link, "bururu/bururu"), MaxBuildBytes, "bururu/bururu is not a plain file"},
		{"not a tarball", pinOf(srv, "/broken.tar.gz", files["/broken.tar.gz"], "bururu"), MaxBuildBytes, "broken.tar.gz:"},
		{"sent to http", pinOf(srv, "/redirect/http://127.0.0.1:1/bururu.tar.gz", good, "bururu"), MaxBuildBytes,
			"a redirect to http://127.0.0.1:1/bururu.tar.gz, which is not https"},
		{"nothing pinned", BururuPin{Bururu: BururuPinFormat}, MaxBuildBytes, "no Bururu build is pinned"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fetch(tc.pin, tc.max)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%v, want %q", err, tc.want)
			}
		})
	}
}

// fakeAppImage stands for Bururu's AppImage: --appimage-extract writes
// squashfs-root with the core at usr/bin/bururu and AppRun pointing at
// it, as .github/scripts/appimage.sh in Bururu's repo lays it out. The
// core answers -version as Bururu's does.
const fakeAppImage = `#!/bin/sh
[ "$#" -eq 1 ] && [ "$1" = --appimage-extract ] || { echo "want --appimage-extract, got $*" >&2; exit 2; }
mkdir -p squashfs-root/usr/bin
printf '#!/bin/sh\n[ "$1" = -version ] && echo "Bururu 1.0.0"\n' > squashfs-root/usr/bin/bururu
chmod 755 squashfs-root/usr/bin/bururu
ln -s usr/bin/bururu squashfs-root/AppRun
`

// TestFetchBururuAppImage: an AppImage whose SHA-256 is the pin's is
// unpacked by running it with --appimage-extract, the program is the
// plain file at the pin's path, and it says the pin's version. The
// stand-in is a shell script, so this runs where the poll does, not on
// Windows.
func TestFetchBururuAppImage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the AppImage stand-in is a shell script")
	}
	good := []byte(fakeAppImage)
	failing := []byte("#!/bin/sh\necho 'cannot read the squashfs' >&2\nexit 1\n")
	srv := buildServer(t, map[string][]byte{"/v1.0.0/Bururu-x86_64.AppImage": good, "/failing/Bururu-x86_64.AppImage": failing})
	fetch := func(pin BururuPin) (string, error) {
		return FetchBururu(context.Background(), srv.Client(), pin, t.TempDir(), MaxBuildBytes)
	}
	prog, err := fetch(pinOf(srv, "/v1.0.0/Bururu-x86_64.AppImage", good, "usr/bin/bururu"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(filepath.ToSlash(prog), "/unpacked/squashfs-root/usr/bin/bururu") {
		t.Fatalf("the program is at %s", prog)
	}
	if err := CheckBururuVersion(context.Background(), prog, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := CheckBururuVersion(context.Background(), prog, "1.0.1"); err == nil ||
		!strings.Contains(err.Error(), `bururu -version says "Bururu 1.0.0"; tools/bururu.json names "Bururu 1.0.1"`) {
		t.Fatalf("another version: %v", err)
	}
	broken := filepath.Join(t.TempDir(), "bururu")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\necho 'cannot start' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CheckBururuVersion(context.Background(), broken, "1.0.0"); err == nil ||
		!strings.Contains(err.Error(), "bururu -version: exit status 1: cannot start") {
		t.Fatalf("a program that does not start: %v", err)
	}
	wrong := pinOf(srv, "/v1.0.0/Bururu-x86_64.AppImage", good, "usr/bin/bururu")
	wrong.SHA256 = strings.Repeat("0", 64)
	for _, tc := range []struct {
		name string
		pin  BururuPin
		want string
	}{
		{"another hash", wrong, "the pin says 0000"},
		{"AppRun is a link", pinOf(srv, "/v1.0.0/Bururu-x86_64.AppImage", good, "AppRun"), "Bururu-x86_64.AppImage has no program at AppRun"},
		{"no program", pinOf(srv, "/v1.0.0/Bururu-x86_64.AppImage", good, "usr/bin/brr"), "has no program at usr/bin/brr"},
		{"extract fails", pinOf(srv, "/failing/Bururu-x86_64.AppImage", failing, "usr/bin/bururu"), "cannot read the squashfs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fetch(tc.pin)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%v, want %q", err, tc.want)
			}
		})
	}
	// a file that did not match its pin is never run
	dir := t.TempDir()
	if _, err := FetchBururu(context.Background(), srv.Client(), wrong, dir, MaxBuildBytes); err == nil {
		t.Fatal("a wrong hash fetched")
	}
	if _, err := os.Stat(filepath.Join(dir, "unpacked", "squashfs-root")); err == nil {
		t.Fatal("the AppImage ran although its hash was wrong")
	}
}

// TestPollWithoutPin: with no build pinned and no -bururu, the poll
// says so, polls nothing and hands on an empty result.
func TestPollWithoutPin(t *testing.T) {
	root := testRepo(t, rallyEntry())
	out := filepath.Join(t.TempDir(), "out")
	if err := run(context.Background(), "poll", []string{"-root", root, "-out", out}); err != nil {
		t.Fatal(err)
	}
	cf, err := ReadChanges(out)
	if err != nil || len(cf.List) != 0 {
		t.Fatalf("%+v, %v", cf, err)
	}
	if b, err := os.ReadFile(filepath.Join(out, "skipped.txt")); err != nil || len(b) != 0 {
		t.Fatalf("skipped.txt: %q, %v", b, err)
	}
}
