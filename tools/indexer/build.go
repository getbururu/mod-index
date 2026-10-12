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

// The index is built from the repo's main branch alone: entries/,
// state/, games/, policy/ and the published files under files/. The
// build is deterministic: the same repo, serial and day give the same
// bytes.

// Built is a built index with what it publishes beside it.
type Built struct {
	Index *Index
	File  []byte            // index.json
	Files map[string][]byte // "icons/<sha>.png", "text/<sha>.txt"
	Same  bool              // the published index says the same: nothing to publish
}

// BuildIndex builds index.json of format from r on the day today.
// Format 2 (v2/) lists every accepted version, an early-access one with
// its free_from. Format 1 (v1/) leaves out each early-access version
// until its free day, and a mod with no free version yet. prev is the
// published index of that format (nil none): the new one takes the next
// serial, and a version prev lists must keep its hash, size and link,
// and stay free once it was. When nothing changed, Same is true and the
// serial stays.
func BuildIndex(r *Repo, format int, prev *Index, today Date) (*Built, error) {
	if format != IndexFormat && format != IndexFormatV1 {
		return nil, fmt.Errorf("index format %d", format)
	}
	if _, ok := today.Time(); !ok {
		return nil, fmt.Errorf("the day %q", today)
	}
	serial := int64(1)
	if prev != nil {
		serial = prev.Serial + 1
	}
	ix := &Index{Index: format, Serial: serial, MinClient: r.Client.MinClient,
		Games: map[string]GameRecord{}, Mods: map[string]Mod{}, Revoked: orEmpty(r.Revoked), Advisories: orEmpty(r.Advisories)}
	for id, g := range r.Games {
		ix.Games[id] = g
	}
	b := &Built{Index: ix, Files: map[string][]byte{}}
	for _, id := range sortedKeys(r.States) {
		st, e := r.States[id], r.Entries[id]
		if format == IndexFormatV1 {
			st = st.freeOn(e.earlyAccess(), today)
		}
		if len(st.Versions) == 0 {
			continue
		}
		m := st.mod(e != nil && e.Official)
		if e == nil && m.Status != StatusRemoved {
			m.Status, m.Note = StatusRemoved, "No longer listed."
		}
		if format == IndexFormat {
			for v, d := range e.earlyAccess() {
				if mv, ok := m.Versions[v]; ok {
					mv.FreeFrom = d
					m.Versions[v] = mv
				}
			}
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
		for _, id := range sortedKeys(prev.Mods) {
			pm := prev.Mods[id]
			for _, v := range sortedKeys(pm.Versions) {
				pv := pm.Versions[v]
				nv, ok := ix.Mods[id].Versions[v]
				if !ok && r.States[id] != nil {
					if _, held := r.States[id].Versions[v]; held {
						return nil, fmt.Errorf("%s %s is in %s/ already, free for everyone: something free stays free, so it cannot become early access", id, v, SiteV1)
					}
				}
				if !ok {
					return nil, fmt.Errorf("%s %s was published and is gone: take a version back with a revocation or a status, never by editing state", id, v)
				}
				if nv.SHA256 != pv.SHA256 || nv.Size != pv.Size || !sameLink(pv.URL, nv.URL, pm, ix.Mods[id]) {
					return nil, fmt.Errorf("%s %s was published with another hash, size or link", id, v)
				}
				switch {
				case nv.FreeFrom == "" || (pv.FreeFrom != "" && nv.FreeFrom <= pv.FreeFrom):
				case pv.FreeFrom == "":
					return nil, fmt.Errorf("%s %s was published free for everyone: something free stays free, so it cannot become early access", id, v)
				default:
					return nil, fmt.Errorf("%s %s: free_from %s is later than the published %s; it may only move earlier", id, v, nv.FreeFrom, pv.FreeFrom)
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

// freeOn is s without the versions that early (an entry's early_access)
// still keeps for Bururu Pro on day.
func (s *State) freeOn(early map[string]Date, day Date) *State {
	if len(early) == 0 {
		return s
	}
	ns := *s
	ns.Versions = map[string]StateVersion{}
	for v, sv := range s.Versions {
		if d, ok := early[v]; ok && d > day {
			continue
		}
		ns.Versions[v] = sv
	}
	return &ns
}

// ReadSite reads the published index of format in its folder of the
// pages branch (v1/ or v2/): nil when there is none yet.
func ReadSite(dir string, format int) (*Index, error) {
	b, err := os.ReadFile(filepath.Join(dir, IndexFile))
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
	if ix.Index != format {
		return nil, fmt.Errorf("the published %s in %s has format %d, not %d", IndexFile, filepath.Base(dir), ix.Index, format)
	}
	return &ix, nil
}

// sameLink: a published version's link is the same, or differs only
// because its repo moved: renamed (same repo id) or deleted and made
// again by the same owner (same owner id). The new link must be the old
// one with the old repo name swapped for the new one (the hash and size
// are checked as well, so the file is the same).
func sameLink(prevURL, newURL string, prev, cur Mod) bool {
	if prevURL == newURL {
		return true
	}
	if prev.Repo == "" || cur.Repo == "" || prev.Repo == cur.Repo {
		return false
	}
	sameRepo := prev.RepoID != 0 && prev.RepoID == cur.RepoID
	sameOwner := prev.OwnerID != 0 && prev.OwnerID == cur.OwnerID
	if !sameRepo && !sameOwner {
		return false
	}
	return strings.Replace(prevURL, "github.com/"+prev.Repo+"/", "github.com/"+cur.Repo+"/", 1) == newURL
}
