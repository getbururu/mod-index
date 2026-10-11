package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// The poll runs a published Bururu build for Linux, pinned in
// tools/bururu.json by its version, address and SHA-256. The indexer
// downloads it, checks the hash and takes the bururu program out of it:
// an AppImage it unpacks by running it with --appimage-extract, which
// needs no FUSE; a .tar.gz it reads itself. Nothing else about Bururu
// comes into this repo.

// BururuPinFile is where the pin lives in the repo.
const BururuPinFile = "tools/bururu.json"

// BururuPinFormat is the format of tools/bururu.json.
const BururuPinFormat = 1

// Limits of the pin and the build it names.
const (
	MaxPinBytes     = 4 << 10
	MaxBuildBytes   = 1 << 30   // the downloaded file
	MaxProgramBytes = 256 << 20 // the bururu program taken out of a .tar.gz
)

// BururuPin is tools/bururu.json: the published Bururu build whose mod
// check and mod listing the poll runs. With every field but the format
// empty, no build is pinned yet.
type BururuPin struct {
	Bururu  int    `json:"bururu"`  // BururuPinFormat
	Version string `json:"version"` // Bururu's version, such as "1.0.0"; the reports name it
	URL     string `json:"url"`     // the https address of the build: an .AppImage or a .tar.gz
	SHA256  string `json:"sha256"`  // of the file at URL, lower-case hex
	Path    string `json:"path"`    // the bururu program inside the build: usr/bin/bururu in the AppImage
}

// Pinned reports whether p names a build.
func (p BururuPin) Pinned() bool { return p != BururuPin{Bururu: p.Bururu} }

// The kinds of build a pin may name, by the end of its address.
const (
	BuildAppImage = ".AppImage"
	BuildTarGz    = ".tar.gz"
)

// buildKind is BuildAppImage or BuildTarGz for the file at the address
// path p; "" for anything else.
func buildKind(p string) string {
	for _, k := range []string{BuildAppImage, BuildTarGz} {
		if base := path.Base(p); strings.HasSuffix(base, k) && len(base) > len(k) {
			return k
		}
	}
	return ""
}

// programPathRE is the program's path inside the build: plain names
// joined by "/", none starting with a dot.
var programPathRE = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]*(/[A-Za-z0-9_-][A-Za-z0-9._-]*)*$`)

// readPin reads tools/bururu.json under root strictly.
func readPin(root string) (BururuPin, error) {
	var p BururuPin
	file := filepath.Join(root, filepath.FromSlash(BururuPinFile))
	if _, err := os.Stat(file); errors.Is(err, fs.ErrNotExist) {
		return p, fmt.Errorf("%s is missing", BururuPinFile)
	}
	if err := readJSON(file, MaxPinBytes, &p); err != nil {
		return p, err
	}
	if err := checkPin(p); err != nil {
		return p, fmt.Errorf("%s: %w", BururuPinFile, err)
	}
	return p, nil
}

// checkPin is what a pin must hold: nothing but its format, or every
// field well formed.
func checkPin(p BururuPin) error {
	c := &checker{}
	if p.Bururu != BururuPinFormat {
		c.fail("format %d; this indexer reads %d", p.Bururu, BururuPinFormat)
	}
	if !p.Pinned() {
		return c.err
	}
	if _, ok := parseSemver(p.Version); !ok || len(p.Version) > 64 {
		c.fail("version %q is not a version such as 1.0.0", p.Version)
	}
	u, err := url.Parse(p.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		len(p.URL) > 512 || buildKind(u.Path) == "" {
		c.fail("url %q is not the https address of an %s or a %s", p.URL, BuildAppImage, BuildTarGz)
	}
	c.match("sha256", sha256RE, p.SHA256)
	if !programPathRE.MatchString(p.Path) || len(p.Path) > 200 {
		c.fail("path %q is not a path inside the build, such as usr/bin/bururu", p.Path)
	}
	return c.err
}

// FetchBururu downloads the build pin names into dir, an empty folder,
// checks its SHA-256 and takes the bururu program out of it. It returns
// the program's absolute path. hc makes the download; max is the most
// bytes the file may have.
func FetchBururu(ctx context.Context, hc *http.Client, pin BururuPin, dir string, max int64) (string, error) {
	if !pin.Pinned() {
		return "", errors.New("no Bururu build is pinned")
	}
	if err := checkPin(pin); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	u, _ := url.Parse(pin.URL)
	file := filepath.Join(dir, "download", path.Base(u.Path))
	if err := download(ctx, hc, pin.URL, pin.SHA256, file, max); err != nil {
		return "", err
	}
	out := filepath.Join(dir, "unpacked")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}
	var prog string
	switch buildKind(u.Path) {
	case BuildAppImage:
		prog, err = unpackAppImage(ctx, file, pin.Path, out)
	case BuildTarGz:
		prog, err = unpackTarGz(file, pin.Path, out)
	}
	if err != nil {
		return "", err
	}
	st, err := os.Lstat(prog)
	if err != nil || !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s has no program at %s", path.Base(u.Path), pin.Path)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o100 == 0 {
		return "", fmt.Errorf("%s in %s may not be run", pin.Path, path.Base(u.Path))
	}
	return prog, nil
}

// buildRunsOn is nil when the pinned build runs on goos/goarch. The
// build is Bururu's Linux release, an x86_64 AppImage.
func buildRunsOn(goos, goarch string) error {
	if goos != "linux" || goarch != "amd64" {
		return fmt.Errorf("the pinned Bururu is a Linux x86_64 build; on %s/%s, poll with -bururu <program> -ref <ref>", goos, goarch)
	}
	return nil
}

// CheckBururuDownload downloads the build pin names into dir, checks its
// SHA-256 and removes the file again. It neither unpacks nor runs the
// build: check-pr uses it on a pull request that changes the pin.
func CheckBururuDownload(ctx context.Context, hc *http.Client, pin BururuPin, dir string, max int64) error {
	if !pin.Pinned() {
		return errors.New("no Bururu build is pinned")
	}
	if err := checkPin(pin); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	u, _ := url.Parse(pin.URL)
	file := filepath.Join(dir, path.Base(u.Path))
	defer os.Remove(file)
	return download(ctx, hc, pin.URL, pin.SHA256, file, max)
}

// pinText says what pin names, for a report.
func pinText(p BururuPin) string {
	if !p.Pinned() {
		return "no build pinned"
	}
	return fmt.Sprintf("Bururu %s from %s, SHA-256 %s, program %s", p.Version, p.URL, p.SHA256, p.Path)
}

// CheckBururuVersion runs `<prog> -version` and wants it to print
// "Bururu <version>": the build runs here, and it is the version its pin
// names.
func CheckBururuVersion(ctx context.Context, prog, version string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, prog, "-version")
	cmd.Env = childEnv()
	var out bytes.Buffer
	cmd.Stdout = &limited{w: &out, n: 4 << 10}
	cmd.Stderr = cmd.Stdout
	err := cmd.Run()
	got := strings.TrimSpace(out.String())
	if err != nil {
		return fmt.Errorf("bururu -version: %v: %s", err, got)
	}
	if want := "Bururu " + version; got != want {
		return fmt.Errorf("bururu -version says %q; %s names %q", got, BururuPinFile, want)
	}
	return nil
}

// download writes the file at rawURL to file: over https only, sent on
// to https only, with no token, at most max bytes, and only when its
// SHA-256 is sum.
func download(ctx context.Context, hc *http.Client, rawURL, sum, file string, max int64) error {
	if u, err := url.Parse(rawURL); err != nil || u.Scheme != "https" || u.User != nil {
		return fmt.Errorf("%s is not an https address", rawURL)
	}
	c := *hc
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" || req.URL.User != nil {
			return fmt.Errorf("a redirect to %s, which is not https", req.URL.Redacted())
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "mod-index-indexer")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", rawURL, resp.Status)
	}
	if resp.ContentLength > max {
		return fmt.Errorf("download %s: %d bytes, more than %d", rawURL, resp.ContentLength, max)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, max+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > max {
		err = fmt.Errorf("download %s: more than %d bytes", rawURL, max)
	}
	if got := hex.EncodeToString(h.Sum(nil)); err == nil && got != sum {
		err = fmt.Errorf("download %s: its SHA-256 is %s; the pin says %s", rawURL, got, sum)
	}
	if err != nil {
		os.Remove(file)
		return err
	}
	return nil
}

// unpackAppImage unpacks the AppImage file into dir by running it with
// --appimage-extract, and returns where the program inner is. The
// AppImage's runtime reads its own squashfs, so this needs no FUSE.
func unpackAppImage(ctx context.Context, file, inner, dir string) (string, error) {
	if err := os.Chmod(file, 0o755); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, file, "--appimage-extract")
	cmd.Dir = dir
	cmd.Env = childEnv()
	var errOut bytes.Buffer
	cmd.Stderr = &limited{w: &errOut, n: 64 << 10}
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s --appimage-extract: %v: %s", filepath.Base(file), err, bytes.TrimSpace(errOut.Bytes()))
	}
	return filepath.Join(dir, "squashfs-root", filepath.FromSlash(inner)), nil
}

// unpackTarGz takes the plain file inner out of the .tar.gz file into
// dir, and returns its path. A second file of that name refuses the
// archive.
func unpackTarGz(file, inner, dir string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("%s: %v", filepath.Base(file), err)
	}
	tr := tar.NewReader(zr)
	prog := ""
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("%s: %v", filepath.Base(file), err)
		}
		if strings.TrimPrefix(h.Name, "./") != inner {
			continue
		}
		switch {
		case prog != "":
			return "", fmt.Errorf("%s holds %s twice", filepath.Base(file), inner)
		case h.Typeflag != tar.TypeReg:
			return "", fmt.Errorf("%s: %s is not a plain file", filepath.Base(file), inner)
		case h.Size > MaxProgramBytes:
			return "", fmt.Errorf("%s: %s has %d bytes, more than %d", filepath.Base(file), inner, h.Size, MaxProgramBytes)
		}
		prog = filepath.Join(dir, path.Base(inner))
		out, err := os.OpenFile(prog, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
		if err != nil {
			return "", err
		}
		_, err = io.Copy(out, tr)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return "", fmt.Errorf("%s: %s: %v", filepath.Base(file), inner, err)
		}
	}
	if prog == "" {
		return "", fmt.Errorf("%s has no %s", filepath.Base(file), inner)
	}
	return prog, nil
}
