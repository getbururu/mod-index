package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The repo's own files: what authors and maintainers write (entries,
// games, policy) and what the bot's pull requests write (state, files).
// The indexer reads all of them strictly; `indexer validate` runs on
// every pull request.

// The formats of the repo's files.
const (
	EntryFormat    = 1
	StateFormat    = 1
	GameFormat     = 1
	ReservedFormat = 1
)

// Limits of the repo's files.
const (
	MaxEntryBytes  = 16 << 10
	MaxStateBytes  = 2 << 20
	MaxGameBytes   = 64 << 10
	MaxPolicyBytes = 256 << 10
)

// Entry is entries/<id>.json: one listed mod, added by its author's pull
// request.
type Entry struct {
	Entry int    `json:"entry"` // EntryFormat
	ID    string `json:"id"`    // the manifest's id; the file is entries/<id>.json
	Repo  string `json:"repo"`  // "<owner>/<name>" on github.com
	// Official: a mod of the list's maintainers; an author's pull request
	// that sets it is refused
	Official bool `json:"official"`
	// Prerelease: also list GitHub pre-releases (Bururu shows them only
	// with "Show test versions")
	Prerelease bool `json:"prerelease"`
	// Pins: versions whose pack the maintainers built themselves, by
	// version; a release whose asset differs holds the mod
	Pins map[string]Pin `json:"pins,omitempty"`
}

// Pin is the pack a version must have.
type Pin struct {
	Asset  string `json:"asset"`  // <id>-<version>.brr
	SHA256 string `json:"sha256"` // lower-case hex
	Size   int64  `json:"size"`   // exact bytes
}

// State is state/<id>.json, written by the bot's pull requests only:
// the accepted versions, GitHub's ids and the review records. A version
// once in it never changes (append-only). It holds nothing that depends
// on when the bot ran, so a poll that finds nothing new proposes the
// same bytes again.
type State struct {
	State      int    `json:"state"` // StateFormat
	ID         string `json:"id"`
	Repo       string `json:"repo"`
	RepoID     int64  `json:"repo_id"`
	OwnerID    int64  `json:"owner_id"`
	OwnerLogin string `json:"owner_login"`
	Status     string `json:"status"`     // StatusListed, StatusHold or StatusRemoved
	Note       string `json:"note"`       // why, for hold and removed
	GoneSince  Date   `json:"gone_since"` // the day the repo was first missing; "" while it answers
	Mod        Meta   `json:"mod"`        // from the newest accepted version
	// Versions by version
	Versions map[string]StateVersion `json:"versions"`
}

// Meta are a mod's fields that come from its newest manifest.
type Meta struct {
	Name     string   `json:"name"`
	Summary  string   `json:"summary"`
	Authors  []string `json:"authors"`
	Kind     string   `json:"kind"`
	Games    []string `json:"games"`
	For      []string `json:"for"`
	Provides []string `json:"provides"`
	License  string   `json:"license"`
	Icon     Icon     `json:"icon"`
	Readme   *FileRef `json:"readme"`
}

// StateVersion is an accepted version: the index's Version, plus the
// tag's commit and what the checks and the review said.
type StateVersion struct {
	URL          string                `json:"url"`
	Size         int64                 `json:"size"`
	SHA256       string                `json:"sha256"`
	API          string                `json:"api"`
	OS           []string              `json:"os"`
	Prerelease   bool                  `json:"prerelease"`
	Published    time.Time             `json:"published"`
	Commit       string                `json:"commit"`
	Dependencies map[string]Dependency `json:"dependencies"`
	Permissions  Permissions           `json:"permissions"`
	Changelog    *FileRef              `json:"changelog"`
	Notes        string                `json:"notes"`
	Checks       Checks                `json:"checks"`
	Review       StateReview           `json:"review"`
}

// StateReview is a version's review as the bot records it.
type StateReview struct {
	By   string `json:"by"`   // ReviewBy
	Diff string `json:"diff"` // DiffFirst, DiffSame, DiffNarrower or DiffWider
}

// Version is the index's version of v.
func (v StateVersion) Version() Version {
	return Version{URL: v.URL, Size: v.Size, SHA256: v.SHA256, API: v.API, OS: orEmpty(v.OS), Prerelease: v.Prerelease,
		Published: utc(v.Published), Dependencies: orMap(v.Dependencies), Permissions: v.Permissions, Changelog: v.Changelog,
		Notes: v.Notes}
}

// GameFile is games/<id>.json: a canonical game, curated by hand.
type GameFile struct {
	Game int `json:"game"` // GameFormat
	GameRecord
}

// ReservedFile is policy/reserved.json: what a community entry may not
// take.
type ReservedFile struct {
	Reserved int      `json:"reserved"` // ReservedFormat
	IDs      []string `json:"ids"`      // mod ids
	Names    []string `json:"names"`    // names and authors, any case
}

// RevokedFile is policy/revoked.json.
type RevokedFile struct {
	Revoked []Revocation `json:"revoked"`
}

// AdvisoriesFile is policy/advisories.json.
type AdvisoriesFile struct {
	Advisories []Advisory `json:"advisories"`
}

// ClientFile is policy/client.json: what the index asks of Bururu.
type ClientFile struct {
	MinClient string `json:"min_client"` // the oldest Bururu that may use the list; "" any
}

// Repo is the repo's data as read.
type Repo struct {
	Root       string
	Entries    map[string]*Entry
	States     map[string]*State
	Games      map[string]GameRecord
	Reserved   ReservedFile
	Revoked    []Revocation
	Advisories []Advisory
	Client     ClientFile
}

var (
	jsonNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*\.json$`)
	iconNameRE = regexp.MustCompile(`^[0-9a-f]{64}\.png$`)
	textNameRE = regexp.MustCompile(`^[0-9a-f]{64}\.txt$`)
)

// LoadRepo reads the repo's data under root. It returns the data it
// could read and every problem found.
func LoadRepo(root string) (*Repo, []string) {
	r := &Repo{Root: root, Entries: map[string]*Entry{}, States: map[string]*State{}, Games: map[string]GameRecord{}}
	var probs []string
	add := func(format string, args ...any) { probs = append(probs, fmt.Sprintf(format, args...)) }
	each := func(dir string, read func(path, name string) error) {
		names, err := os.ReadDir(filepath.Join(root, dir))
		if errors.Is(err, fs.ErrNotExist) {
			return
		}
		if err != nil {
			add("%s: %v", dir, err)
			return
		}
		for _, e := range names {
			name := e.Name()
			if name == ".gitkeep" {
				continue
			}
			if !e.Type().IsRegular() || !jsonNameRE.MatchString(name) {
				add("%s/%s: only files named <id>.json belong here", dir, name)
				continue
			}
			if err := read(filepath.Join(root, dir, name), strings.TrimSuffix(name, ".json")); err != nil {
				add("%v", err)
			}
		}
	}
	each("entries", func(path, id string) error {
		var e Entry
		if err := readJSON(path, MaxEntryBytes, &e); err != nil {
			return err
		}
		for _, p := range checkEntry(&e, id) {
			add("entries/%s.json: %s", id, p)
		}
		r.Entries[id] = &e
		return nil
	})
	each("state", func(path, id string) error {
		var s State
		if err := readJSON(path, MaxStateBytes, &s); err != nil {
			return err
		}
		if err := checkState(&s, id, r.Entries[id] != nil && r.Entries[id].Official); err != nil {
			return fmt.Errorf("state/%s.json: %w", id, err)
		}
		r.States[id] = &s
		return nil
	})
	each("games", func(path, id string) error {
		var g GameFile
		if err := readJSON(path, MaxGameBytes, &g); err != nil {
			return err
		}
		c := &checker{}
		if g.Game != GameFormat {
			c.fail("game format %d", g.Game)
		}
		checkGame(c, id, g.GameRecord)
		if c.err != nil {
			return fmt.Errorf("games/%s.json: %w", id, c.err)
		}
		r.Games[id] = g.GameRecord
		return nil
	})
	policy := func(name string, v any) {
		path := filepath.Join(root, "policy", name)
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			add("policy/%s is missing", name)
			return
		}
		if err := readJSON(path, MaxPolicyBytes, v); err != nil {
			add("%v", err)
		}
	}
	policy("reserved.json", &r.Reserved)
	var rv RevokedFile
	policy("revoked.json", &rv)
	r.Revoked = rv.Revoked
	var ad AdvisoriesFile
	policy("advisories.json", &ad)
	r.Advisories = ad.Advisories
	policy("client.json", &r.Client)
	probs = append(probs, r.checkPolicy()...)
	probs = append(probs, r.checkFiles()...)
	for _, id := range sortedKeys(r.States) {
		if r.Entries[id] == nil && r.States[id].Status != StatusRemoved {
			add("state/%s.json: no entry for it, and it is not removed", id)
		}
	}
	return r, probs
}

// checkEntry is what an entry file must hold.
func checkEntry(e *Entry, id string) []string {
	var p []string
	if e.Entry != EntryFormat {
		p = append(p, fmt.Sprintf("entry format %d; this indexer reads %d", e.Entry, EntryFormat))
	}
	if e.ID != id {
		p = append(p, fmt.Sprintf("the id %q is not the file's name %q", e.ID, id))
	}
	if !modIDRE.MatchString(e.ID) {
		p = append(p, "the id takes lower-case letters, digits and '-', 2 to 40, starting with a letter")
	}
	if !repoRE.MatchString(e.Repo) || strings.HasSuffix(e.Repo, ".git") {
		p = append(p, fmt.Sprintf("repo %q is not <owner>/<name>", e.Repo))
	}
	for _, v := range sortedKeys(e.Pins) {
		pin := e.Pins[v]
		if _, ok := parseSemver(v); !ok {
			p = append(p, fmt.Sprintf("pins: %q is not a version", v))
		}
		if pin.Asset != e.ID+"-"+v+PackExt || !sha256RE.MatchString(pin.SHA256) || pin.Size <= 0 || pin.Size > MaxPackBytes {
			p = append(p, fmt.Sprintf("pins.%s: asset %s-%s%s, a SHA-256 in lower-case hex and the size", v, e.ID, v, PackExt))
		}
	}
	return p
}

// checkState applies the index's rules to a state file, as the mod it
// becomes; official comes from its entry.
func checkState(s *State, id string, official bool) error {
	c := &checker{}
	if s.State != StateFormat {
		c.fail("state format %d", s.State)
	}
	if s.ID != id {
		c.fail("the id %q is not the file's name", s.ID)
	}
	if s.GoneSince != "" {
		c.date("gone_since", s.GoneSince)
	}
	if c.err != nil {
		return c.err
	}
	checkMod(c, id, s.mod(official))
	for _, v := range sortedKeys(s.Versions) {
		sv := s.Versions[v]
		c.match("versions."+v+".commit", commitRE, sv.Commit)
		switch sv.Review.Diff {
		case DiffFirst, DiffSame, DiffNarrower, DiffWider:
		default:
			c.fail("versions.%s review diff %q", v, sv.Review.Diff)
		}
	}
	return c.err
}

// mod is the index's Mod of s.
func (s *State) mod(official bool) Mod {
	m := Mod{Name: s.Mod.Name, Summary: s.Mod.Summary, Authors: orEmpty(s.Mod.Authors), Official: official, Kind: s.Mod.Kind,
		Games: orEmpty(s.Mod.Games), For: orEmpty(s.Mod.For), Provides: orEmpty(s.Mod.Provides), License: s.Mod.License,
		Repo: s.Repo, RepoID: s.RepoID, OwnerID: s.OwnerID, OwnerLogin: s.OwnerLogin, Icon: s.Mod.Icon, Readme: s.Mod.Readme,
		Status: s.Status, Note: s.Note, Versions: map[string]Version{}}
	for v, sv := range s.Versions {
		m.Versions[v] = sv.Version()
	}
	return m
}

// checkPolicy checks the policy files' contents.
func (r *Repo) checkPolicy() []string {
	var probs []string
	if r.Reserved.Reserved != ReservedFormat {
		probs = append(probs, fmt.Sprintf("policy/reserved.json: format %d", r.Reserved.Reserved))
	}
	for _, id := range r.Reserved.IDs {
		if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`).MatchString(id) {
			probs = append(probs, fmt.Sprintf("policy/reserved.json: id %q", id))
		}
	}
	for _, n := range r.Reserved.Names {
		if !NameOK(n) {
			probs = append(probs, fmt.Sprintf("policy/reserved.json: name %q", n))
		}
	}
	c := &checker{}
	for i, rv := range r.Revoked {
		checkRevocation(c, fmt.Sprintf("policy/revoked.json: revoked[%d]", i), rv)
	}
	for i, a := range r.Advisories {
		checkAdvisory(c, fmt.Sprintf("policy/advisories.json: advisories[%d]", i), a)
	}
	if _, ok := parseSemver(r.Client.MinClient); r.Client.MinClient != "" && !ok {
		c.fail("policy/client.json: min_client %q", r.Client.MinClient)
	}
	if c.err != nil {
		probs = append(probs, c.err.Error())
	}
	return probs
}

// checkFiles checks files/icons and files/text: names, hashes, sizes,
// PNG and text rules, and that every file a state names is there.
func (r *Repo) checkFiles() []string {
	var probs []string
	have := map[string]int64{}
	for _, dir := range []string{IconsDir, TextDir} {
		names, err := os.ReadDir(filepath.Join(r.Root, "files", dir))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			probs = append(probs, err.Error())
			continue
		}
		for _, e := range names {
			name := e.Name()
			if name == ".gitkeep" {
				continue
			}
			b, err := os.ReadFile(filepath.Join(r.Root, "files", dir, name))
			if err == nil {
				err = checkPublished(dir, name, b)
			}
			if err != nil {
				probs = append(probs, fmt.Sprintf("files/%s/%s: %v", dir, name, err))
				continue
			}
			have[dir+"/"+name] = int64(len(b))
		}
	}
	for _, id := range sortedKeys(r.States) {
		for _, ref := range r.States[id].refs() {
			if size, ok := have[ref.path]; !ok || size != ref.Size {
				probs = append(probs, fmt.Sprintf("state/%s.json names files/%s, which is not there with %d bytes", id, ref.path, ref.Size))
			}
		}
	}
	return probs
}

// pubRef is a published file a state names.
type pubRef struct {
	FileRef
	path string // icons/<sha>.png or text/<sha>.txt
}

// refs are the published files s names.
func (s *State) refs() []pubRef {
	var out []pubRef
	addRef := func(dir, ext string, f *FileRef) {
		if f == nil {
			return
		}
		p := dir + "/" + f.SHA256 + ext
		if !slices.ContainsFunc(out, func(r pubRef) bool { return r.path == p }) {
			out = append(out, pubRef{FileRef: *f, path: p})
		}
	}
	addRef(IconsDir, ".png", s.Mod.Icon.PNG)
	addRef(TextDir, ".txt", s.Mod.Readme)
	for _, v := range sortedKeys(s.Versions) {
		addRef(TextDir, ".txt", s.Versions[v].Changelog)
	}
	return out
}

// checkPublished checks one icon or text file: the name is its SHA-256,
// an icon is a PNG of at most MaxIconBytes, a text plain UTF-8 of at most
// MaxTextBytes.
func checkPublished(dir, name string, b []byte) error {
	sum := sha256.Sum256(b)
	switch dir {
	case IconsDir:
		if !iconNameRE.MatchString(name) {
			return errors.New("an icon is named <sha256>.png")
		}
		if len(b) == 0 || len(b) > MaxIconBytes {
			return fmt.Errorf("%d bytes; an icon has at most %d", len(b), MaxIconBytes)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(b))
		if err != nil || cfg.Width != cfg.Height || cfg.Width < 64 || cfg.Width > 256 {
			return errors.New("not a square PNG of 64 to 256 pixels")
		}
		if _, err := png.Decode(bytes.NewReader(b)); err != nil {
			return errors.New("the PNG does not decode")
		}
	case TextDir:
		if !textNameRE.MatchString(name) {
			return errors.New("a text is named <sha256>.txt")
		}
		if len(b) == 0 || len(b) > MaxTextBytes || !TextOK(string(b)) {
			return fmt.Errorf("not plain text of 1 to %d bytes", MaxTextBytes)
		}
	default:
		return errors.New("unknown folder")
	}
	if hex.EncodeToString(sum[:]) != name[:64] {
		return errors.New("its SHA-256 is not its name")
	}
	return nil
}

// orMap is m, or an empty map for none, so the JSON says {}.
func orMap[V any](m map[string]V) map[string]V {
	if m == nil {
		return map[string]V{}
	}
	return m
}

// orEmpty is l, or an empty list for none, so the JSON says [].
func orEmpty[T any](l []T) []T {
	if l == nil {
		return []T{}
	}
	return slices.Clone(l)
}

// utc is t in UTC to the second, as every file of the list writes times.
func utc(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }
