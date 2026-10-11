package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The check of a pull request (check-entry.yml): every data file of the
// pull request's tree read strictly, and for each entry it adds or
// changes, every check of a first listing on the repo's newest release.
// It runs read-only; check-report.yml posts the report.

// maintainers are the GitHub author associations that may set an
// entry's official flag and pins.
var maintainers = []string{"OWNER", "MEMBER", "COLLABORATOR"}

// EntryCheck is what the check of a pull request found.
type EntryCheck struct {
	Pass   bool
	Report string
}

// CheckPullRequest checks the tree pr against the base tree base. author
// is the pull request's author and assoc GitHub's author association.
func CheckPullRequest(ctx context.Context, p *Poller, base, pr string, author, assoc string) *EntryCheck {
	var b strings.Builder
	pass := true
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	fail := func(format string, args ...any) {
		pass = false
		line("FAIL "+format, args...)
	}
	baseRepo, baseProbs := LoadRepo(base)
	prRepo, prProbs := LoadRepo(pr)
	line("Pull request by %s (%s)", orNone(author), orNone(strings.ToLower(assoc)))
	line("")
	line("Data files:")
	for _, x := range prProbs {
		if !slices.Contains(baseProbs, x) {
			fail("%s", x)
		}
	}
	if pass {
		line("  every data file reads")
	}
	maintainer := slices.Contains(maintainers, assoc)
	checkPinChange(ctx, p, base, pr, maintainer, line, fail)
	p.Repo = baseRepo
	changed := 0
	for _, id := range sortedKeys(prRepo.Entries) {
		e := prRepo.Entries[id]
		old := baseRepo.Entries[id]
		if old != nil {
			a, _ := os.ReadFile(filepath.Join(base, "entries", id+".json"))
			c, _ := os.ReadFile(filepath.Join(pr, "entries", id+".json"))
			if bytes.Equal(a, c) {
				continue
			}
		}
		changed++
		line("")
		line("Entry %s (%s):", id, e.Repo)
		if (e.Official || len(e.Pins) > 0 || (old != nil && (old.Official || len(old.Pins) > 0))) && !maintainer &&
			(old == nil || old.Official != e.Official || !samePins(old.Pins, e.Pins)) {
			fail("only the list's maintainers set official and pins")
		}
		if old != nil && !strings.EqualFold(old.Repo, e.Repo) && !maintainer {
			fail("the repo of a listed mod changes only by a maintainer")
		}
		checkOneEntry(ctx, p, e, author, line, fail)
	}
	for _, id := range sortedKeys(baseRepo.Entries) {
		if prRepo.Entries[id] == nil {
			changed++
			line("")
			if maintainer {
				line("Entry %s is removed (a maintainer's change; state/%s.json then publishes as removed).", id, id)
			} else {
				fail("entry %s is removed: only maintainers remove entries", id)
			}
		}
	}
	if changed == 0 {
		line("")
		line("No entry is added or changed.")
	}
	line("")
	if pass {
		line("Result: pass. A maintainer reviews it against the checklist before merging.")
	} else {
		line("Result: fail.")
	}
	return &EntryCheck{Pass: pass, Report: cutReport(b.String())}
}

// checkPinChange reports a change of tools/bururu.json: the Bururu build
// the poll runs on every pack and whose checks the bot's pull requests
// carry. Only maintainers change it. For a maintainer's pin it downloads
// the file and checks its SHA-256, without unpacking or running it, so
// a wrong address or hash shows here and not in the polls after the
// merge.
func checkPinChange(ctx context.Context, p *Poller, base, pr string, maintainer bool, line func(string, ...any), fail func(string, ...any)) {
	a, _ := os.ReadFile(filepath.Join(base, filepath.FromSlash(BururuPinFile)))
	c, _ := os.ReadFile(filepath.Join(pr, filepath.FromSlash(BururuPinFile)))
	if bytes.Equal(a, c) {
		return
	}
	line("")
	line("Bururu pin (%s):", BururuPinFile)
	old, err := readPin(base)
	if err != nil {
		line("  before: does not read")
	} else {
		line("  before: %s", pinText(old))
	}
	pin, err := readPin(pr)
	if err != nil {
		line("  after: does not read (see Data files)")
	} else {
		line("  after: %s", pinText(pin))
	}
	switch {
	case !maintainer:
		fail("only the list's maintainers change %s", BururuPinFile)
	case err != nil || !pin.Pinned():
	default:
		dir, err := os.MkdirTemp(p.Work, "bururu-")
		if err == nil {
			err = CheckBururuDownload(ctx, p.buildClient(), pin, dir, MaxBuildBytes)
		}
		if err != nil {
			fail("the pinned build: %v", err)
			return
		}
		line("  the file at that address has the pinned SHA-256; the poll unpacks and runs it after the merge")
	}
}

func samePins(a, b map[string]Pin) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// checkOneEntry runs the checks of a first listing on e's newest
// release, or confirms a listed mod's repo.
func checkOneEntry(ctx context.Context, p *Poller, e *Entry, author string, line func(string, ...any), fail func(string, ...any)) {
	info, err := p.GH.Repo(ctx, e.Repo)
	switch {
	case errors.Is(err, ErrGone):
		fail("the repo %s is not on GitHub, or not public", e.Repo)
		return
	case err != nil:
		fail("GitHub: %v", err)
		return
	case info.Private:
		fail("the repo is private")
		return
	case info.Archived || info.Disabled:
		fail("the repo is archived or disabled")
		return
	}
	line("  repo id %d, owner %s (id %d), %d stars", info.ID, info.Owner.Login, info.Owner.ID, info.Stars)
	if author != "" && !strings.EqualFold(author, info.Owner.Login) {
		line("  note: the pull request's author %s is not the repo's owner %s: check that %s administers the repo", author, info.Owner.Login, author)
	}
	st := p.Repo.States[e.ID]
	if st != nil && len(st.Versions) > 0 {
		if st.RepoID != info.ID || st.OwnerID != info.Owner.ID {
			fail("the repo's ids (%d, owner %d) are not the listed ones (%d, owner %d)", info.ID, info.Owner.ID, st.RepoID, st.OwnerID)
			return
		}
		line("  already listed with %d versions; the poll takes new versions", len(st.Versions))
		return
	}
	rels, err := p.GH.Releases(ctx, e.Repo)
	if err != nil {
		fail("GitHub: %v", err)
		return
	}
	var best *Release
	bestVer := ""
	for i := range rels {
		r := &rels[i]
		ver, ok := tagVersion(r.TagName)
		if r.Draft || !ok || (isPrerelease(ver) && !e.Prerelease) {
			continue
		}
		if bestVer == "" || cmpVersions(ver, bestVer) > 0 {
			best, bestVer = r, ver
		}
	}
	if best == nil {
		fail("no release with a tag v<version> to check (drafts and, unless the entry takes them, pre-releases do not count)")
		return
	}
	if p.Run == nil {
		pack := e.ID + "-" + bestVer + PackExt
		if best.Asset(pack) == nil {
			fail("the release %s has no asset %s", best.TagName, pack)
			return
		}
		line("  %s %s has its pack %s; the poll checks the pack after the merge and opens a pull request with its report.", e.ID, bestVer, pack)
		return
	}
	v := p.examine(ctx, e, st, info, best, bestVer)
	switch {
	case v.reject != "":
		fail("%s %s: %s", e.ID, bestVer, v.reject)
	case v.hold != "":
		fail("%s %s: %s", e.ID, bestVer, v.hold)
	default:
		line("  %s %s passes every check; the report follows.", e.ID, bestVer)
		line("")
		for _, l := range splitText(report(e, st, v.cand)) {
			line("  %s", l)
		}
	}
}
