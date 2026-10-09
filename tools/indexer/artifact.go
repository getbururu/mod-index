package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// The poll's read-only job hands its result to the job that writes as a
// folder of plain files (a workflow artifact):
//
//	changes.json                 the changes, in order
//	<key>/state.json             the proposed state/<id>.json
//	<key>/report.txt             the reviewer's report: the pull request's body
//	<key>/files/icons/<sha>.png  published files the state names
//	<key>/files/text/<sha>.txt
//	skipped.txt                  releases not taken, and why
//
// The writing job reads it with the strict readers below and runs
// nothing from it.

// ChangesFormat is the format of changes.json.
const ChangesFormat = 1

// ChangesFile is changes.json.
type ChangesFile struct {
	Changes int          `json:"changes"` // ChangesFormat
	List    []ChangeHead `json:"list"`
}

// ChangeHead names one change.
type ChangeHead struct {
	Key     string `json:"key"`
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Version string `json:"version"`
	Title   string `json:"title"`
}

// Limits of what the writing job reads.
const (
	maxChanges      = 200
	maxChangesBytes = 256 << 10
	maxSkippedBytes = 64 << 10
)

var (
	keyRE   = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}-(hold|gone|removed|refresh|[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?)$`)
	titleRE = regexp.MustCompile(`^(List|Add|Hold|Remove|Refresh) [ -~]{1,120}$`)
)

// WriteChanges writes the poll's result into out.
func WriteChanges(out string, changes []*Change, skipped []string) error {
	cf := ChangesFile{Changes: ChangesFormat, List: []ChangeHead{}}
	for _, c := range changes {
		cf.List = append(cf.List, ChangeHead{Key: c.Key, ID: c.ID, Kind: c.Kind, Version: c.Version, Title: c.Title})
		dir := filepath.Join(out, c.Key)
		if err := writeJSON(filepath.Join(dir, "state.json"), c.State, "  "); err != nil {
			return err
		}
		if err := writeFile(filepath.Join(dir, "report.txt"), []byte(cutReport(c.Report))); err != nil {
			return err
		}
		for _, name := range sortedKeys(c.Files) {
			if err := writeFile(filepath.Join(dir, "files", filepath.FromSlash(name)), c.Files[name]); err != nil {
				return err
			}
		}
	}
	if err := writeJSON(filepath.Join(out, "changes.json"), cf, "  "); err != nil {
		return err
	}
	text := strings.Join(skipped, "\n")
	if text != "" {
		text += "\n"
	}
	return writeFile(filepath.Join(out, "skipped.txt"), []byte(cutText(Strip(text), maxSkippedBytes)))
}

// ReadChanges reads changes.json of an artifact strictly.
func ReadChanges(in string) (*ChangesFile, error) {
	var cf ChangesFile
	if err := readJSON(filepath.Join(in, "changes.json"), maxChangesBytes, &cf); err != nil {
		return nil, err
	}
	if cf.Changes != ChangesFormat || len(cf.List) > maxChanges {
		return nil, broken("changes.json: format %d, %d changes", cf.Changes, len(cf.List))
	}
	seen := map[string]bool{}
	for _, h := range cf.List {
		want := h.ID + "-" + h.Kind
		if h.Kind == ChangeVersion {
			want = h.ID + "-" + h.Version
		}
		switch {
		case !keyRE.MatchString(h.Key) || seen[h.Key] || h.Key != want:
			return nil, broken("changes.json: the key %q", h.Key)
		case !modIDRE.MatchString(h.ID):
			return nil, broken("changes.json: the id %q", h.ID)
		case !slices.Contains([]string{ChangeVersion, ChangeHold, ChangeGone, ChangeRemoved, ChangeRefresh}, h.Kind):
			return nil, broken("changes.json: the kind %q", h.Kind)
		case !titleRE.MatchString(h.Title):
			return nil, broken("changes.json: the title %q", h.Title)
		}
		seen[h.Key] = true
	}
	return &cf, nil
}

// Proposal is one change as read back: checked against the repo it goes
// into.
type Proposal struct {
	Head   ChangeHead
	State  *State
	Report string
	Files  map[string][]byte // "icons/<sha>.png", "text/<sha>.txt"
}

// ReadProposal reads one change of an artifact and checks it against
// the repo r (the main branch it is proposed for): strict formats,
// file names, PNGs decoded again, the index's rules, append-only state,
// and only the differences its kind allows.
func ReadProposal(in string, h ChangeHead, r *Repo) (*Proposal, error) {
	dir := filepath.Join(in, h.Key)
	p := &Proposal{Head: h, Files: map[string][]byte{}}
	var st State
	if err := readJSON(filepath.Join(dir, "state.json"), MaxStateBytes, &st); err != nil {
		return nil, err
	}
	if err := checkState(&st, h.ID, r.Entries[h.ID] != nil && r.Entries[h.ID].Official); err != nil {
		return nil, fmt.Errorf("%s: %w", h.Key, err)
	}
	p.State = &st
	rep, err := os.ReadFile(filepath.Join(dir, "report.txt"))
	if err != nil {
		return nil, err
	}
	if len(rep) > MaxReportBytes || !TextOK(string(rep)) {
		return nil, broken("%s: the report is not plain text of at most %d bytes", h.Key, MaxReportBytes)
	}
	p.Report = string(rep)
	// files: only icons/ and text/, named by their hash, checked again
	err = filepath.WalkDir(filepath.Join(dir, "files"), func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == filepath.Join(dir, "files") {
			return filepath.SkipDir
		}
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(filepath.Join(dir, "files"), path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "." || rel == IconsDir || rel == TextDir {
				return nil
			}
			return broken("%s: the folder files/%s", h.Key, rel)
		}
		sub, name, _ := strings.Cut(rel, "/")
		if !d.Type().IsRegular() || strings.Contains(name, "/") {
			return broken("%s: files/%s", h.Key, rel)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := checkPublished(sub, name, b); err != nil {
			return broken("%s: files/%s: %v", h.Key, rel, err)
		}
		p.Files[rel] = b
		return nil
	})
	if err != nil {
		return nil, err
	}
	// every file the state names is in the change or already in the repo
	for _, ref := range st.refs() {
		b, ok := p.Files[ref.path]
		if !ok {
			b, err = os.ReadFile(filepath.Join(r.Root, "files", filepath.FromSlash(ref.path)))
			if err != nil {
				return nil, broken("%s: the state names files/%s, which is neither in the change nor in the repo", h.Key, ref.path)
			}
		}
		if int64(len(b)) != ref.Size {
			return nil, broken("%s: files/%s has %d bytes, the state says %d", h.Key, ref.path, len(b), ref.Size)
		}
	}
	for name := range p.Files {
		if !slices.ContainsFunc(st.refs(), func(x pubRef) bool { return x.path == name }) {
			return nil, broken("%s: files/%s is named by nothing in the state", h.Key, name)
		}
	}
	if err := allowedChange(r, h, &st); err != nil {
		return nil, err
	}
	return p, nil
}

// allowedChange checks a proposed state against the repo's: versions
// are only ever added, and each kind changes only its own fields.
func allowedChange(r *Repo, h ChangeHead, st *State) error {
	e := r.Entries[h.ID]
	if e == nil {
		return broken("%s: there is no entry %s", h.Key, h.ID)
	}
	if st.Repo != e.Repo {
		return broken("%s: the state names the repo %s; the entry %s", h.Key, st.Repo, e.Repo)
	}
	old := r.States[h.ID]
	if old == nil {
		if h.Kind != ChangeVersion || len(st.Versions) != 1 || st.Versions[h.Version].SHA256 == "" ||
			st.Status != StatusListed || st.Note != "" || st.GoneSince != "" {
			return broken("%s: a first listing adds exactly its one version", h.Key)
		}
		return newNames(r, h, e, st)
	}
	// every accepted version stays as it was
	for v, ov := range old.Versions {
		nv, ok := st.Versions[v]
		if !ok {
			return broken("%s: the version %s is gone from the state", h.Key, v)
		}
		a, _ := encode(ov, "")
		b, _ := encode(nv, "")
		if !bytes.Equal(a, b) {
			return broken("%s: the accepted version %s changed", h.Key, v)
		}
	}
	same := func(fields ...string) error {
		o, n := cloneState(old), cloneState(st)
		for _, f := range fields {
			switch f {
			case "versions":
				n.Versions = o.Versions
			case "mod":
				n.Mod = o.Mod
			case "owner":
				n.OwnerLogin = o.OwnerLogin
			case "status":
				n.Status, n.Note = o.Status, o.Note
			case "gone":
				n.GoneSince = o.GoneSince
			}
		}
		a, _ := encode(o, "")
		b, _ := encode(n, "")
		if !bytes.Equal(a, b) {
			return broken("%s: the change touches more of the state than a %s change may", h.Key, h.Kind)
		}
		return nil
	}
	switch h.Kind {
	case ChangeVersion:
		if len(st.Versions) != len(old.Versions)+1 || st.Versions[h.Version].SHA256 == "" || old.Status != StatusListed {
			return broken("%s: a version change adds exactly its one version to a listed mod", h.Key)
		}
		if err := same("versions", "mod", "owner"); err != nil {
			return err
		}
		return newNames(r, h, e, st)
	case ChangeHold:
		if st.Status != StatusHold || st.Note == "" {
			return broken("%s: a hold sets the status hold with a note", h.Key)
		}
		return same("status")
	case ChangeGone:
		if st.Status != StatusHold || st.GoneSince == "" || old.GoneSince != "" {
			return broken("%s: a gone change holds the mod and sets gone_since", h.Key)
		}
		return same("status", "gone")
	case ChangeRemoved:
		if st.Status != StatusRemoved || st.Note == "" {
			return broken("%s: a removal sets the status removed with a note", h.Key)
		}
		return same("status")
	case ChangeRefresh:
		if st.OwnerLogin == old.OwnerLogin {
			return broken("%s: a refresh changes the owner's login", h.Key)
		}
		return same("owner")
	}
	return broken("%s: the kind %q", h.Key, h.Kind)
}

// newNames runs the name rules again in the writing job, on the trusted
// side, so a forged artifact cannot list a reserved or confusable name.
func newNames(r *Repo, h ChangeHead, e *Entry, st *State) error {
	if probs := nameProblems(r, h.ID, e.Official, st.OwnerID, st.Mod.Name, st.Mod.Authors); len(probs) > 0 {
		return broken("%s: %s", h.Key, strings.Join(probs, "; "))
	}
	return nil
}

// Apply writes a proposal into the repo at root: state/<id>.json and the
// files it brings.
func (p *Proposal) Apply(root string) error {
	if err := writeJSON(filepath.Join(root, "state", p.Head.ID+".json"), p.State, "  "); err != nil {
		return err
	}
	for _, name := range sortedKeys(p.Files) {
		if err := writeFile(filepath.Join(root, "files", filepath.FromSlash(name)), p.Files[name]); err != nil {
			return err
		}
	}
	return nil
}

// Body is the pull request's body: a short head and the report inside a
// code block, so nothing in it is read as markup or mentions anyone.
func (p *Proposal) Body() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s, proposed by the poll. Merging it is the review: read the report below against the checklist in docs/listing.md; the list is published right after the merge.\n\n", p.Head.Title)
	b.WriteString("```text\n")
	b.WriteString(strings.ReplaceAll(p.Report, "`", "'"))
	if !strings.HasSuffix(p.Report, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("```\n")
	return b.String()
}
