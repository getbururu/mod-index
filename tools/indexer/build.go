package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The index is built from the repo's main branch alone: state/, games/,
// policy/ and the published files under files/. The build is
// deterministic: the same repo and serial give the same bytes.

// Built is a built index with what it publishes beside it.
type Built struct {
	Index *Index
	File  []byte            // index.json
	Files map[string][]byte // "icons/<sha>.png", "text/<sha>.txt"
	Same  bool              // the published index says the same: nothing to publish
}

// BuildIndex builds index.json from r. prev is the published index (nil
// none): the new one takes the next serial, and a version prev lists
// must keep its hash, size and link. When nothing changed, Same is true
// and the serial stays.
func BuildIndex(r *Repo, prev *Index) (*Built, error) {
	serial := int64(1)
	if prev != nil {
		serial = prev.Serial + 1
	}
	ix := &Index{Index: IndexFormat, Serial: serial, MinClient: r.Client.MinClient,
		Games: map[string]GameRecord{}, Mods: map[string]Mod{}, Revoked: orEmpty(r.Revoked), Advisories: orEmpty(r.Advisories)}
	for id, g := range r.Games {
		ix.Games[id] = g
	}
	b := &Built{Index: ix, Files: map[string][]byte{}}
	for _, id := range sortedKeys(r.States) {
		st := r.States[id]
		if len(st.Versions) == 0 {
			continue
		}
		official := false
		if e := r.Entries[id]; e != nil {
			official = e.Official
		}
		m := st.mod(official)
		if r.Entries[id] == nil && m.Status != StatusRemoved {
			m.Status, m.Note = StatusRemoved, "No longer listed."
		}
		ix.Mods[id] = m
		for _, ref := range st.refs() {
			fb, err := os.ReadFile(filepath.Join(r.Root, "files", filepath.FromSlash(ref.path)))
			if err != nil {
				return nil, fmt.Errorf("%s: files/%s: %v", id, ref.path, err)
			}
			if err := checkPublished(filepath.Dir(ref.path), filepath.Base(ref.path), fb); err != nil || int64(len(fb)) != ref.Size {
				return nil, fmt.Errorf("%s: files/%s does not match its pin", id, ref.path)
			}
			b.Files[ref.path] = fb
		}
	}
	if prev != nil {
		for id, pm := range prev.Mods {
			for v, pv := range pm.Versions {
				nv, ok := ix.Mods[id].Versions[v]
				if !ok {
					return nil, fmt.Errorf("%s %s was published and is gone: take a version back with a revocation or a status, never by editing state", id, v)
				}
				if nv.SHA256 != pv.SHA256 || nv.Size != pv.Size || !sameLink(pv.URL, nv.URL, pm, ix.Mods[id]) {
					return nil, fmt.Errorf("%s %s was published with another hash, size or link", id, v)
				}
			}
		}
	}
	if err := checkIndex(ix); err != nil {
		return nil, err
	}
	file, err := encode(ix, "")
	if err != nil {
		return nil, err
	}
	if len(file) > MaxIndexBytes {
		return nil, fmt.Errorf("the index has %d bytes, more than %d", len(file), MaxIndexBytes)
	}
	// read back as Bururu reads it
	if _, err := readIndex(file); err != nil {
		return nil, fmt.Errorf("the built index fails Bururu's reader: %w", err)
	}
	if prev != nil {
		was := *prev
		was.Serial = serial
		if old, err := encode(&was, ""); err == nil && bytes.Equal(old, file) {
			ix.Serial = prev.Serial
			if file, err = encode(ix, ""); err != nil {
				return nil, err
			}
			b.Same = true
		}
	}
	b.File = file
	return b, nil
}

// ReadSite reads the published index in the v1/ folder of the pages
// branch: nil when there is none yet.
func ReadSite(v1 string) (*Index, error) {
	b, err := os.ReadFile(filepath.Join(v1, IndexFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ix Index
	if err := decode(b, MaxIndexBytes, &ix, true); err != nil {
		return nil, fmt.Errorf("the published %s: %v", IndexFile, err)
	}
	return &ix, nil
}

// sameLink: a published version's link is the same, or differs only
// because its repo was renamed: same repo id, and the link with the old
// repo name swapped for the new one (the hash and size are checked as
// well, so the file is the same).
func sameLink(prevURL, newURL string, prev, cur Mod) bool {
	if prevURL == newURL {
		return true
	}
	if prev.RepoID == 0 || prev.RepoID != cur.RepoID || prev.Repo == "" || cur.Repo == "" || prev.Repo == cur.Repo {
		return false
	}
	return strings.Replace(prevURL, "github.com/"+prev.Repo+"/", "github.com/"+cur.Repo+"/", 1) == newURL
}
