package main

import (
	"fmt"
	"strings"
	"time"
)

// MaxReportBytes is the most a report may hold: a pull request's body or
// comment has room for it inside a code block.
const MaxReportBytes = 60 << 10

// report is the reviewer's text for a new version of e: what it is,
// where it comes from, what it may reach, and what changed.
func report(e *Entry, st *State, c *Candidate) string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	kind := "update"
	if c.Prev == "" {
		kind = "first listing"
	}
	line("%s %s from %s (%s)", e.ID, c.Version, e.Repo, kind)
	line("")
	r := c.Release
	flags := "release"
	if c.State.Prerelease {
		flags = "pre-release (shown with \"Show test versions\")"
	}
	line("Release:   tag %s, %s, published %s", r.TagName, flags, utc(r.PublishedAt).Format(time.RFC3339))
	line("Pack:      %s, %d bytes, sha256 %s", c.Pack, c.State.Size, c.State.SHA256)
	if a := r.Asset(c.Pack); a != nil && a.Digest != "" {
		line("           matches GitHub's digest")
	} else {
		line("           GitHub gives no digest for it")
	}
	if pin, ok := e.Pins[c.Version]; ok && pin.SHA256 == c.State.SHA256 {
		line("           matches the entry's pin")
	}
	line("Commit:    %s", c.State.Commit)
	line("Repo:      id %d, owner %s (id %d)", c.Repo.ID, c.Repo.Owner.Login, c.Repo.Owner.ID)
	if st != nil && st.OwnerLogin != "" && st.OwnerLogin != c.Repo.Owner.Login {
		line("           the owner's login was %s", st.OwnerLogin)
	}
	line("Checks:    mod check pass, %d warnings, %d taste notes (Bururu %s)", c.Check.Warnings, c.Check.Taste, orNone(c.State.Checks.CheckedBy))
	line("Mod API:   %s; os: %s", c.State.API, orAll(c.State.OS))
	if e.Official {
		line("Official:  yes (the entry says so)")
	}
	line("")
	line("Name:      %s", c.Meta.Name)
	line("Authors:   %s", strings.Join(c.Meta.Authors, ", "))
	line("Summary:   %s", c.Meta.Summary)
	line("Kind:      %s; games: %s; for: %s; provides: %s", c.Meta.Kind, orNone(strings.Join(c.Meta.Games, ", ")),
		orNone(strings.Join(c.Meta.For, ", ")), orNone(strings.Join(c.Meta.Provides, ", ")))
	line("Licence:   %s", orNone(c.Meta.License))
	icon := c.Meta.Icon.Builtin
	if c.Meta.Icon.PNG != nil {
		icon += fmt.Sprintf(", icon.png (files/icons/%s.png, %d bytes)", c.Meta.Icon.PNG.SHA256, c.Meta.Icon.PNG.Size)
	}
	line("Icon:      %s", orNone(strings.TrimPrefix(icon, ", ")))
	line("")
	if c.Prev == "" {
		line("Permissions (every item; the install dialog shows them):")
	} else {
		line("Permissions against %s: %s", c.Prev, c.Perm.Diff)
	}
	for _, it := range c.Perm.Added {
		line("  + %s", it)
	}
	for _, it := range c.Perm.Removed {
		line("  - %s", it)
	}
	for _, w := range c.Perm.Why {
		line("  ! %s", w)
	}
	if len(c.Perm.Added)+len(c.Perm.Removed)+len(c.Perm.Why) == 0 {
		line("  (none)")
	}
	line("")
	line("Dependencies:")
	if len(c.Deps) == 0 {
		line("  (no change)")
	}
	for _, d := range c.Deps {
		line("  %s", d)
	}
	line("")
	f := c.Files
	line("Files: %d added, %d removed, %d changed, %d the same", len(f.Added), len(f.Removed), len(f.Changed), f.Same)
	if c.Prev != "" {
		for _, x := range f.Added {
			line("  + %s", x)
		}
		for _, x := range f.Removed {
			line("  - %s", x)
		}
		for _, x := range f.Changed {
			line("  ~ %s", x)
		}
	}
	line("")
	line("Lua flags (for the reviewer; read these lines):")
	if len(c.Flags) == 0 {
		line("  (none)")
	}
	for _, x := range c.Flags {
		line("  %s", x)
	}
	for _, n := range c.Notes {
		line("Note: %s", n)
	}
	for _, w := range c.Listing.Warnings {
		line("mod listing: %s", w)
	}
	for _, t := range c.Check.TasteNotes {
		line("taste: %s", t)
	}
	line("")
	line("Notes of %s (from the CHANGELOG):", c.Version)
	if c.State.Notes == "" {
		line("  (none)")
	}
	for _, x := range splitText(c.State.Notes) {
		line("  %s", x)
	}
	if c.Lua != "" {
		line("")
		if c.Prev == "" {
			line("Lua (every file; read it in full):")
		} else {
			line("Lua diff against %s:", c.Prev)
		}
		b.WriteString(c.Lua)
	}
	return cutReport(b.String())
}

// cutReport is a report as it may be posted: plain text with no control
// or bidi character, at most MaxReportBytes.
func cutReport(s string) string {
	s = Strip(s)
	if len(s) <= MaxReportBytes {
		return s
	}
	const tail = "\n[cut: the report is longer than 60 KiB; read the rest in the repository]\n"
	s = cutText(s, MaxReportBytes-len(tail))
	if i := strings.LastIndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return s + tail
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func orAll(os []string) string {
	if len(os) == 0 {
		return "every system"
	}
	return strings.Join(os, ", ")
}
