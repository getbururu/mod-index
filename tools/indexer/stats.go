package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// CountStats reads GitHub's own counters for every listed mod: the
// download count of each listed version's pack, summed, and the repo's
// stars. A repo that does not answer is left out (its numbers hide).
func CountStats(ctx context.Context, gh *GitHub, r *Repo, now time.Time) (*Stats, []string, error) {
	st := &Stats{Stats: StatsFormat, Generated: utc(now), Mods: map[string]ModStats{}}
	var notes []string
	for _, id := range sortedKeys(r.States) {
		s := r.States[id]
		if s.Status == StatusRemoved || len(s.Versions) == 0 {
			continue
		}
		info, err := gh.Repo(ctx, s.Repo)
		if errors.Is(err, ErrGone) {
			notes = append(notes, id+": the repo is gone")
			continue
		}
		if err != nil {
			return nil, notes, err
		}
		rels, err := gh.Releases(ctx, s.Repo)
		if err != nil {
			return nil, notes, err
		}
		ms := ModStats{Stars: info.Stars, Versions: map[string]int64{}}
		for v, sv := range s.Versions {
			repo, tag, file, ok := releaseURL(sv.URL)
			if !ok || repo != s.Repo {
				continue
			}
			for i := range rels {
				if rels[i].TagName != tag {
					continue
				}
				if a := rels[i].Asset(file); a != nil && a.DownloadCount >= 0 {
					ms.Versions[v] = a.DownloadCount
					ms.Downloads += a.DownloadCount
				}
			}
		}
		if len(ms.Versions) < len(s.Versions) {
			notes = append(notes, fmt.Sprintf("%s: %d of %d versions have a release on GitHub now", id, len(ms.Versions), len(s.Versions)))
		}
		st.Mods[id] = ms
	}
	if err := checkStats(st); err != nil {
		return nil, notes, err
	}
	return st, notes, nil
}
