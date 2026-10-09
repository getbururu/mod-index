package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// The poll: for each entry, read the repo and its releases, take every
// new version through every check of a listing, and propose what
// changed. It runs in a job that can write nothing: it downloads and
// parses packs nobody has reviewed yet, and hands its result on as files
// that a second job reads with strict readers.

// Poller is what a poll or an entry check needs.
type Poller struct {
	Repo *Repo
	GH   *GitHub
	Run  Runner
	Ref  string // the pinned Bururu ref whose checks ran: checks.checked_by
	Work string // a scratch folder
	Now  time.Time
}

// Change is one proposed change of the repo: one bot pull request.
type Change struct {
	Key     string // "<id>-<version>", "<id>-hold", "<id>-gone", "<id>-removed", "<id>-refresh"
	ID      string
	Kind    string // ChangeVersion, ChangeHold, ChangeGone, ChangeRemoved, ChangeRefresh
	Version string // ChangeVersion only
	Title   string
	State   *State
	Report  string
	Files   map[string][]byte // "icons/<sha>.png", "text/<sha>.txt"
}

// The kinds of changes.
const (
	ChangeVersion = "version" // a new accepted version
	ChangeHold    = "hold"    // drift or a problem: a maintainer looks
	ChangeGone    = "gone"    // the repo is missing: held, removed after GoneDays
	ChangeRemoved = "removed" // archived, or gone for GoneDays
	ChangeRefresh = "refresh" // the owner's login changed
)

// GoneDays is how long a missing repo is held before it is removed.
const GoneDays = 30

// Branch is the bot branch of a change.
func (c *Change) Branch() string { return "bot/" + c.Key }

// Candidate is a version that passed every check.
type Candidate struct {
	Version  string
	Tag      string
	State    StateVersion
	Meta     Meta
	Listing  *Listing
	Check    *CheckResult
	Prev     string // the version it was compared with; "" the first
	Perm     PermDiff
	Deps     []string
	Files    FileDiff
	Lua      string
	Flags    []string
	Notes    []string // what the report says besides
	Repo     *RepoInfo
	Release  *Release
	Pack     string // the asset's name
	Publish  map[string][]byte
	Official bool
}

// verdict is what became of one release: a candidate, a refusal (not
// listed) or a hold (drift: a maintainer looks).
type verdict struct {
	cand   *Candidate
	reject string
	hold   string
}

// tagVersion is the version of a release tag: "v1.2.0" or "1.2.0".
func tagVersion(tag string) (string, bool) {
	v := strings.TrimPrefix(tag, "v")
	if _, ok := parseSemver(v); !ok || strings.Contains(v, "+") {
		return "", false
	}
	return v, true
}

// isPrerelease reports a test version: a version with a pre-release
// part, such as 1.3.0-beta.1. GitHub's own pre-release mark is not read.
func isPrerelease(ver string) bool { return strings.Contains(ver, "-") }

// newest is the highest version of vs; stable only takes releases.
func newest(vs []string, stable bool) string {
	best := ""
	for _, v := range vs {
		sv, ok := parseSemver(v)
		if !ok || (stable && sv.pre != "") {
			continue
		}
		if best == "" || cmpVersions(v, best) > 0 {
			best = v
		}
	}
	return best
}

// examine takes one release of e's repo through the checks.
func (p *Poller) examine(ctx context.Context, e *Entry, st *State, info *RepoInfo, rel *Release, ver string) verdict {
	listed := st != nil && len(st.Versions) > 0
	drift := func(format string, args ...any) verdict {
		msg := fmt.Sprintf(format, args...)
		if listed {
			return verdict{hold: msg}
		}
		return verdict{reject: msg}
	}
	pack := e.ID + "-" + ver + PackExt
	a := rel.Asset(pack)
	if a == nil {
		if rel.Asset(e.ID+"-"+ver+".zip") != nil {
			return verdict{reject: fmt.Sprintf("the release %s has %s-%s.zip; the list takes the asset %s", rel.TagName, e.ID, ver, pack)}
		}
		return verdict{reject: fmt.Sprintf("the release %s has no asset %s", rel.TagName, pack)}
	}
	if a.State != "" && a.State != "uploaded" {
		return verdict{reject: fmt.Sprintf("%s is not uploaded yet", pack)}
	}
	if a.Size <= 0 || a.Size > MaxPackBytes {
		return verdict{reject: fmt.Sprintf("%s has %d bytes; a pack has at most %d", pack, a.Size, MaxPackBytes)}
	}
	b, err := p.GH.Download(ctx, a.URL, MaxPackBytes)
	if err != nil {
		return verdict{reject: "download: " + err.Error()}
	}
	sum := sha256.Sum256(b)
	sha := hex.EncodeToString(sum[:])
	if int64(len(b)) != a.Size {
		return drift("%s: GitHub says %d bytes, the download has %d", pack, a.Size, len(b))
	}
	if a.Digest != "" && a.Digest != "sha256:"+sha {
		return drift("%s: the download's SHA-256 %s is not GitHub's digest %s", pack, sha, a.Digest)
	}
	if pin, ok := e.Pins[ver]; ok && (pin.SHA256 != sha || pin.Size != a.Size) {
		return drift("%s: the download (sha256 %s, %d bytes) is not the pinned pack (sha256 %s, %d bytes)", pack, sha, a.Size, pin.SHA256, pin.Size)
	}
	dir := filepath.Join(p.Work, e.ID, ver)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return verdict{reject: err.Error()}
	}
	packPath := filepath.Join(dir, pack)
	if err := os.WriteFile(packPath, b, 0o644); err != nil {
		return verdict{reject: err.Error()}
	}
	iconDir, textDir := filepath.Join(dir, IconsDir), filepath.Join(dir, TextDir)
	l, err := p.Run.Listing(ctx, packPath, iconDir, textDir)
	if err != nil {
		return verdict{reject: err.Error()}
	}
	switch {
	case l.ID != e.ID:
		return verdict{reject: fmt.Sprintf("the manifest's id is %q; the entry's is %q", l.ID, e.ID)}
	case l.Version != ver:
		return verdict{reject: fmt.Sprintf("the manifest's version is %q; the tag %s says %s", l.Version, rel.TagName, ver)}
	case l.SHA256 != sha || l.Size != a.Size:
		return verdict{reject: "mod listing read another file than the download"}
	case len(l.Problems) > 0:
		return verdict{reject: "mod listing: " + strings.Join(l.Problems, "; ")}
	case !NameOK(l.Name) || len(l.Authors) == 0 || slices.ContainsFunc(l.Authors, func(a string) bool { return !NameOK(a) }):
		return verdict{reject: "the name and each author are printable ASCII (0x20-0x7E), 1 to 64 characters, with no space at either end"}
	case len(l.Reserved) > 0 && !e.Official:
		return verdict{reject: "names only an official mod may carry: " + strings.Join(l.Reserved, ", ")}
	}
	with, notes := p.dependencies(ctx, l)
	c, err := p.Run.Check(ctx, packPath, with)
	if err != nil {
		return verdict{reject: err.Error()}
	}
	if c.Result != CheckPass || c.Errors > 0 {
		var errs []string
		for _, pr := range c.Problems {
			if !pr.Warn {
				errs = append(errs, fmt.Sprintf("%s:%d: %s", pr.File, pr.Line, pr.Text))
			}
		}
		return verdict{reject: "mod check found errors: " + strings.Join(errs, "; ")}
	}
	if probs := nameProblems(p.Repo, e.ID, e.Official, info.Owner.ID, l.Name, l.Authors); len(probs) > 0 {
		return verdict{reject: strings.Join(probs, "; ")}
	}
	commit, err := p.GH.Commit(ctx, e.Repo, rel.TagName)
	if err != nil {
		return verdict{reject: err.Error()}
	}
	pub := utc(rel.PublishedAt)
	if pub.IsZero() {
		return verdict{reject: "the release has no publication time"}
	}
	cand := &Candidate{Version: ver, Tag: rel.TagName, Listing: l, Check: c, Repo: info, Release: rel, Pack: pack, Official: e.Official,
		Notes: notes, Publish: map[string][]byte{}}
	cand.State = StateVersion{URL: ReleaseLink(e.Repo, rel.TagName, pack), Size: a.Size, SHA256: sha, API: l.API, OS: orEmpty(l.OS),
		Prerelease: isPrerelease(ver), Published: pub, Commit: commit,
		Dependencies: orMap(l.Dependencies), Permissions: cleanPermissions(l.Permissions), Changelog: l.Changelog,
		Notes:  cutText(Strip(l.Notes), MaxNotesBytes),
		Checks: Checks{Result: CheckPass, Warnings: c.Warnings, Taste: c.Taste, CheckedBy: p.Ref},
		Review: StateReview{By: ReviewBy}}
	cand.Meta = Meta{Name: l.Name, Summary: StripLine(l.Summary), Authors: orEmpty(l.Authors), Kind: l.Kind, Games: orEmpty(l.Games),
		For: orEmpty(l.For), Provides: orEmpty(l.Provides), License: StripLine(l.License), Icon: l.Icon, Readme: l.Readme}
	if len(cand.Meta.Summary) > MaxSummaryLen {
		cand.Meta.Summary = cutText(cand.Meta.Summary, MaxSummaryLen)
	}
	// the published files mod listing wrote
	for _, ref := range []struct {
		dir, ext string
		f        *FileRef
	}{{IconsDir, ".png", l.Icon.PNG}, {TextDir, ".txt", l.Readme}, {TextDir, ".txt", l.Changelog}} {
		if ref.f == nil {
			continue
		}
		name := ref.f.SHA256 + ref.ext
		fb, err := os.ReadFile(filepath.Join(dir, ref.dir, name))
		if err == nil {
			err = checkPublished(ref.dir, name, fb)
		}
		if err == nil && int64(len(fb)) != ref.f.Size {
			err = errors.New("its size is not the listing's")
		}
		if err != nil {
			return verdict{reject: fmt.Sprintf("%s/%s from mod listing: %v", ref.dir, name, err)}
		}
		cand.Publish[ref.dir+"/"+name] = fb
	}
	// the review: against the previous accepted version
	var prev *StateVersion
	prevKind := ""
	if listed {
		var below []string
		for v := range st.Versions {
			if cmpVersions(v, ver) < 0 {
				below = append(below, v)
			}
		}
		if cand.Prev = newest(below, false); cand.Prev == "" {
			cand.Prev = newest(slices.Collect(maps.Keys(st.Versions)), false)
		}
		pv := st.Versions[cand.Prev]
		prev, prevKind = &pv, st.Mod.Kind
	}
	cand.Perm = diffPermissions(prev, prevKind, Version{Dependencies: cand.State.Dependencies, Permissions: cand.State.Permissions}, l.Kind)
	cand.State.Review.Diff = cand.Perm.Diff
	var prevDeps map[string]Dependency
	if prev != nil {
		prevDeps = prev.Dependencies
	}
	cand.Deps = diffDependencies(prevDeps, cand.State.Dependencies)
	files, err := readPack(b)
	if err != nil {
		return verdict{reject: "the pack: " + err.Error()}
	}
	var prevFiles []PackFile
	if prev != nil {
		pb, err := p.GH.Download(ctx, prev.URL, MaxPackBytes)
		if err == nil && sha256Hex(pb) != prev.SHA256 {
			err = errors.New("its SHA-256 is not the accepted one")
		}
		if err == nil {
			prevFiles, err = readPack(pb)
		}
		if err != nil {
			cand.Notes = append(cand.Notes, fmt.Sprintf("%s could not be read for the file and Lua diff: %v", cand.Prev, err))
			prevFiles = nil
		}
	}
	cand.Files = diffFiles(prevFiles, files)
	cand.Lua = diffLua(prevFiles, files)
	changed := map[string]bool{}
	for _, f := range slices.Concat(cand.Files.Added, cand.Files.Changed) {
		changed[f] = true
	}
	for _, f := range files {
		if isLua(f.Path) && (prev == nil || changed[f.Path]) {
			cand.Flags = append(cand.Flags, luaFlags(f.Path, f.Data)...)
		}
	}
	return verdict{cand: cand}
}

// cleanPermissions is p with empty lists for none, so the JSON is the
// same whatever mod listing wrote.
func cleanPermissions(p Permissions) Permissions {
	return Permissions{Files: orMap(p.Files), Paths: orEmpty(p.Paths), Screen: p.Screen, Keyboard: p.Keyboard,
		UDP: orEmpty(p.UDP), UDPForward: orEmpty(p.UDPForward)}
}

// dependencies are the packs of the listed mods a version requires, for
// `mod check --with`; built-in mods need none.
func (p *Poller) dependencies(ctx context.Context, l *Listing) (with, notes []string) {
	for _, id := range sortedKeys(l.Dependencies) {
		d := l.Dependencies[id]
		st := p.Repo.States[id]
		if d.Kind != DepRequired || st == nil || len(st.Versions) == 0 {
			continue
		}
		v := newest(slices.Collect(maps.Keys(st.Versions)), true)
		if v == "" {
			v = newest(slices.Collect(maps.Keys(st.Versions)), false)
		}
		sv := st.Versions[v]
		b, err := p.GH.Download(ctx, sv.URL, MaxPackBytes)
		if err == nil && sha256Hex(b) != sv.SHA256 {
			err = errors.New("its SHA-256 is not the accepted one")
		}
		if err != nil {
			notes = append(notes, fmt.Sprintf("the dependency %s %s could not be read: %v", id, v, err))
			continue
		}
		path := filepath.Join(p.Work, "deps", id, id+"-"+v+PackExt)
		if err := writeFile(path, b); err != nil {
			notes = append(notes, err.Error())
			continue
		}
		with = append(with, path)
		notes = append(notes, fmt.Sprintf("checked with the listed %s %s", id, v))
	}
	return with, notes
}

// Poll polls one entry and returns its changes, and the releases it did
// not take with why.
func (p *Poller) Poll(ctx context.Context, e *Entry) (changes []*Change, skipped []string, err error) {
	st := p.Repo.States[e.ID]
	if st != nil && st.Status == StatusRemoved {
		return nil, nil, nil
	}
	today := DateOf(p.Now)
	info, err := p.GH.Repo(ctx, e.Repo)
	if errors.Is(err, ErrGone) {
		if st == nil {
			return nil, []string{e.ID + ": the repo " + e.Repo + " is not on GitHub"}, nil
		}
		return p.gone(st, today), nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if st == nil {
		switch {
		case info.Private:
			return nil, []string{e.ID + ": the repo is private"}, nil
		case info.Archived || info.Disabled:
			return nil, []string{e.ID + ": the repo is archived or disabled"}, nil
		}
	} else {
		if st.Status == StatusHold {
			return nil, nil, nil // a maintainer looks first
		}
		switch {
		case info.Archived:
			return []*Change{p.statusChange(st, ChangeRemoved, StatusRemoved, "No longer maintained.")}, nil, nil
		case info.Private || info.Disabled:
			return []*Change{p.statusChange(st, ChangeHold, StatusHold, "The repository is private or disabled.")}, nil, nil
		case info.ID != st.RepoID:
			return []*Change{p.statusChange(st, ChangeHold, StatusHold,
				fmt.Sprintf("Drift: the repository's id is %d; it was %d.", info.ID, st.RepoID))}, nil, nil
		case info.Owner.ID != st.OwnerID:
			return []*Change{p.statusChange(st, ChangeHold, StatusHold,
				fmt.Sprintf("Drift: the repository's owner id is %d; it was %d.", info.Owner.ID, st.OwnerID))}, nil, nil
		}
	}
	rels, err := p.GH.Releases(ctx, e.Repo)
	if err != nil {
		return nil, nil, err
	}
	if st != nil {
		if why := changedPack(st, rels); why != "" {
			return []*Change{p.statusChange(st, ChangeHold, StatusHold, "Drift: "+why)}, nil, nil
		}
	}
	// the versions to look at, oldest first
	var have []string
	if st != nil {
		have = slices.Collect(maps.Keys(st.Versions))
	}
	var stable []string
	for _, v := range have {
		if !st.Versions[v].Prerelease {
			stable = append(stable, v)
		}
	}
	topStable, topAll := newest(stable, true), newest(have, false)
	type cand struct {
		ver string
		rel *Release
	}
	var todo []cand
	for i := range rels {
		r := &rels[i]
		ver, ok := tagVersion(r.TagName)
		pre := isPrerelease(ver)
		if r.Draft || !ok || (pre && !e.Prerelease) || slices.Contains(have, ver) {
			continue
		}
		if (!pre && topStable != "" && cmpVersions(ver, topStable) <= 0) || (pre && topAll != "" && cmpVersions(ver, topAll) <= 0) {
			continue
		}
		todo = append(todo, cand{ver, r})
	}
	slices.SortFunc(todo, func(a, b cand) int { return cmpVersions(a.ver, b.ver) })
	if len(todo) == 0 {
		if st != nil && info.Owner.Login != st.OwnerLogin {
			ns := cloneState(st)
			ns.OwnerLogin = info.Owner.Login
			return []*Change{{Key: e.ID + "-refresh", ID: e.ID, Kind: ChangeRefresh, State: ns,
				Title:  fmt.Sprintf("Refresh %s: owner login %s", e.ID, info.Owner.Login),
				Report: fmt.Sprintf("The owner of %s (id %d) is now called %s; it was %s.\n", e.Repo, st.OwnerID, info.Owner.Login, st.OwnerLogin)}}, skipped, nil
		}
		return nil, skipped, nil
	}
	if st == nil {
		todo = todo[len(todo)-1:] // a first listing takes the newest version only
	}
	for _, t := range todo {
		v := p.examine(ctx, e, st, info, t.rel, t.ver)
		switch {
		case v.hold != "":
			return []*Change{p.statusChange(st, ChangeHold, StatusHold, "Drift: "+v.hold)}, skipped, nil
		case v.reject != "":
			skipped = append(skipped, fmt.Sprintf("%s %s: %s", e.ID, t.ver, v.reject))
			continue
		}
		changes = append(changes, p.versionChange(e, st, v.cand))
	}
	return changes, skipped, nil
}

// changedPack says which accepted version's pack changed on GitHub after
// it was listed: its asset is gone from its release, or has another size
// or digest. "" when none did. Bururu refuses such a download anyway,
// since the list pins each pack's SHA-256; this tells a maintainer.
func changedPack(st *State, rels []Release) string {
	for _, v := range sortedKeys(st.Versions) {
		sv := st.Versions[v]
		_, tag, file, ok := releaseURL(sv.URL)
		if !ok {
			continue
		}
		for i := range rels {
			if rels[i].TagName != tag {
				continue
			}
			a := rels[i].Asset(file)
			switch {
			case a == nil:
				return fmt.Sprintf("the release %s no longer has %s, the pack of %s", tag, file, v)
			case a.Size != sv.Size || (a.Digest != "" && a.Digest != "sha256:"+sv.SHA256):
				return fmt.Sprintf("%s of %s changed on GitHub after it was listed", file, v)
			}
		}
	}
	return ""
}

// versionChange is the change that adds c to st (nil: a first listing).
func (p *Poller) versionChange(e *Entry, st *State, c *Candidate) *Change {
	ns := &State{State: StateFormat, ID: e.ID, Repo: e.Repo, Status: StatusListed, Versions: map[string]StateVersion{}}
	if st != nil {
		ns = cloneState(st)
	}
	ns.RepoID, ns.OwnerID, ns.OwnerLogin = c.Repo.ID, c.Repo.Owner.ID, c.Repo.Owner.Login
	ns.Versions[c.Version] = c.State
	// the mod's fields come from its newest release, or its newest test
	// version while it has no release
	var all, stable []string
	for v, sv := range ns.Versions {
		all = append(all, v)
		if !sv.Prerelease {
			stable = append(stable, v)
		}
	}
	if top := newest(stable, true); top == c.Version || (top == "" && newest(all, false) == c.Version) {
		ns.Mod = c.Meta
	}
	verb := "Add"
	if st == nil || len(st.Versions) == 0 {
		verb = "List"
	}
	return &Change{Key: e.ID + "-" + c.Version, ID: e.ID, Kind: ChangeVersion, Version: c.Version, State: ns,
		Title: fmt.Sprintf("%s %s %s", verb, e.ID, c.Version), Report: report(e, st, c), Files: c.Publish}
}

// statusChange holds or removes st with a note.
func (p *Poller) statusChange(st *State, kind, status, note string) *Change {
	ns := cloneState(st)
	ns.Status, ns.Note = status, note
	title := "Hold " + st.ID
	if status == StatusRemoved {
		title = "Remove " + st.ID
	}
	return &Change{Key: st.ID + "-" + kind, ID: st.ID, Kind: kind, State: ns, Title: title,
		Report: fmt.Sprintf("%s (%s): %s\n\nThe listed versions stay as they are. A maintainer looks at the repository before anything else changes.\n", title, st.Repo, note)}
}

// gone records a missing repo, and removes it after GoneDays.
func (p *Poller) gone(st *State, today Date) []*Change {
	if st.GoneSince == "" {
		c := p.statusChange(st, ChangeGone, StatusHold, "The repository is gone.")
		c.State.GoneSince = today
		return []*Change{c}
	}
	since, ok := st.GoneSince.Time()
	now, _ := today.Time()
	if ok && now.Sub(since) >= GoneDays*24*time.Hour {
		return []*Change{p.statusChange(st, ChangeRemoved, StatusRemoved, "Repository gone.")}
	}
	return nil
}

// cloneState is a deep copy of s.
func cloneState(s *State) *State {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	var c State
	if err := json.Unmarshal(b, &c); err != nil {
		panic(err)
	}
	if c.Versions == nil {
		c.Versions = map[string]StateVersion{}
	}
	return &c
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// cutText is s cut to at most limit bytes at a character's edge.
func cutText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	s = s[:limit]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
