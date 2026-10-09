package main

import (
	"net"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The rules Bururu applies to every file of the list it reads
// (internal/modindex/read.go in Bururu's repo), so the indexer never
// publishes a file Bururu refuses. Keep the two alike.

// NameOK reports a name or author the list takes: printable ASCII
// (0x20-0x7E), not empty, at most MaxNameLen, no space at either end.
func NameOK(s string) bool {
	if s == "" || len(s) > MaxNameLen || s[0] == ' ' || s[len(s)-1] == ' ' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// TextOK reports a text field the list takes: valid UTF-8 whose only
// controls are tab and newline, with no bidi control.
func TextOK(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n':
		case badRune(r):
			return false
		}
	}
	return true
}

// badRune is a control other than tab and newline, or a bidi control.
func badRune(r rune) bool {
	return r < 0x20 && r != '\t' && r != '\n' || r == 0x7f || (r >= 0x80 && r <= 0x9f) || IsBidi(r)
}

// IsBidi reports a bidi control: U+200E, U+200F, U+202A-U+202E,
// U+2066-U+2069.
func IsBidi(r rune) bool {
	return r == 0x200e || r == 0x200f || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

// Strip removes what TextOK refuses: bytes that are not UTF-8, controls
// other than tab and newline, and bidi controls. A carriage return
// before a newline goes too; others become newlines.
func Strip(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Map(func(r rune) rune {
		if badRune(r) {
			return -1
		}
		return r
	}, s)
}

// StripLine is Strip for a one-line field: tabs and newlines become
// spaces, and the ends are trimmed.
func StripLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' {
			return ' '
		}
		return r
	}, Strip(s))
	return strings.TrimSpace(s)
}

// ReservedAuthor is the author name official entries carry.
const ReservedAuthor = "Bururu"

// ReservedName reports a name or author the client refuses for a
// community entry, in any case.
func ReservedName(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "bururu", "official":
		return true
	}
	return false
}

var (
	modIDRE   = regexp.MustCompile(ModIDPattern)
	gameIDRE  = regexp.MustCompile(GameIDPattern)
	sha256RE  = regexp.MustCompile(SHA256Pattern)
	commitRE  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	targetRE  = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}(@[1-9][0-9]{0,5})?$`)
	iconRE    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	declRE    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	repoRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$`)
	loginRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)
	storeIDRE = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	// GitHub's owner and repo names, a tag or an asset name in a link
	repoPartRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	assetNameRE = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,255}$`)
)

// checker collects the first problem of a file.
type checker struct{ err error }

func (c *checker) fail(format string, args ...any) {
	if c.err == nil {
		c.err = broken(format, args...)
	}
}

func (c *checker) text(where, s string, limit int, line bool) {
	switch {
	case len(s) > limit:
		c.fail("%s is longer than %d bytes", where, limit)
	case !TextOK(s):
		c.fail("%s has a control or bidi character", where)
	case line && strings.ContainsAny(s, "\t\n"):
		c.fail("%s is more than one line", where)
	}
}

func (c *checker) match(where string, re *regexp.Regexp, s string) {
	if !re.MatchString(s) {
		c.fail("%s %q is not well formed", where, s)
	}
}

func (c *checker) file(where string, f *FileRef, limit int64) {
	if f == nil {
		return
	}
	if !sha256RE.MatchString(f.SHA256) || f.Size <= 0 || f.Size > limit {
		c.fail("%s is not a pinned file", where)
	}
}

func (c *checker) date(where string, d Date) {
	if _, ok := d.Time(); !ok {
		c.fail("%s %q is not a day", where, d)
	}
}

// checkIndex applies Bururu's rules to an index.
func checkIndex(ix *Index) error {
	c := &checker{}
	switch {
	case ix.Index != IndexFormat:
		c.fail("index format %d", ix.Index)
	case ix.Serial <= 0:
		c.fail("serial %d", ix.Serial)
	}
	if _, ok := parseSemver(ix.MinClient); ix.MinClient != "" && !ok {
		c.fail("min_client %q", ix.MinClient)
	}
	for _, id := range sortedKeys(ix.Games) {
		checkGame(c, id, ix.Games[id])
	}
	for _, id := range sortedKeys(ix.Mods) {
		checkMod(c, id, ix.Mods[id])
	}
	for i, r := range ix.Revoked {
		checkRevocation(c, "revoked["+strconv.Itoa(i)+"]", r)
	}
	for i, a := range ix.Advisories {
		checkAdvisory(c, "advisories["+strconv.Itoa(i)+"]", a)
	}
	return c.err
}

func checkRevocation(c *checker, where string, r Revocation) {
	c.match(where+".id", modIDRE, r.ID)
	for _, v := range r.Versions {
		if _, ok := parseSemver(v); !ok {
			c.fail("%s names version %q", where, v)
		}
	}
	for _, h := range r.SHA256 {
		c.match(where+".sha256", sha256RE, h)
	}
	if r.Level != LevelWarn && r.Level != LevelBlock {
		c.fail("%s level %q", where, r.Level)
	}
	c.text(where+".reason", r.Reason, MaxNotesBytes, false)
	c.date(where+".date", r.Date)
}

func checkAdvisory(c *checker, where string, a Advisory) {
	if _, ok := parseSemver(a.Below); !ok {
		c.fail("%s below %q", where, a.Below)
	}
	if a.Action != AdviseWarn && a.Action != AdviseCommunityOff {
		c.fail("%s action %q", where, a.Action)
	}
	c.text(where+".text", a.Text, MaxNotesBytes, false)
}

func checkGame(c *checker, id string, g GameRecord) {
	where := "games." + id
	c.match(where, gameIDRE, id)
	if g.ID != id {
		c.fail("%s has the id %q", where, g.ID)
	}
	c.text(where+".name", g.Name, 128, true)
	if g.Name == "" {
		c.fail("%s has no name", where)
	}
	s := g.Stores
	for _, n := range slices.Concat(s.Steam, s.GOG, s.Ubisoft) {
		if n <= 0 {
			c.fail("%s has a store id %d", where, n)
		}
	}
	for _, e := range s.Epic {
		for _, x := range []string{e.App, e.Namespace, e.Item} {
			if x != "" {
				c.match(where+".stores.epic", storeIDRE, x)
			}
		}
	}
	for _, x := range s.Xbox {
		c.match(where+".stores.xbox", storeIDRE, x.Family)
		if x.StoreID != "" {
			c.match(where+".stores.xbox", storeIDRE, x.StoreID)
		}
	}
	for _, x := range slices.Concat(s.BattleNet, s.EA) {
		c.match(where+".stores", storeIDRE, x)
	}
	for _, os := range sortedKeys(g.Exe) {
		e := g.Exe[os]
		if os != OSWindows && os != OSLinux {
			c.fail("%s.exe names the OS %q", where, os)
		}
		for _, x := range slices.Concat(e.Game, e.Launcher) {
			c.text(where+".exe", x, 128, true)
			if x == "" || strings.ContainsAny(x, `/\:`) {
				c.fail("%s.exe has %q", where, x)
			}
		}
		if e.Match != "" && e.Match != "comm" && e.Match != "cmdline" {
			c.fail("%s.exe match %q", where, e.Match)
		}
	}
	for _, m := range g.Markers {
		c.text(where+".markers", m, 260, true)
		if !relPath(m) {
			c.fail("%s has the marker %q", where, m)
		}
	}
	if g.Proton < 0 {
		c.fail("%s proton %d", where, g.Proton)
	}
}

// relPath reports a forward-slash path inside a folder.
func relPath(p string) bool {
	if p == "" || strings.ContainsAny(p, `\:`) || strings.HasPrefix(p, "/") {
		return false
	}
	for _, s := range strings.Split(p, "/") {
		if s == "" || s == "." || s == ".." {
			return false
		}
	}
	return true
}

func checkMod(c *checker, id string, m Mod) {
	where := "mods." + id
	c.match(where, modIDRE, id)
	if !NameOK(m.Name) {
		c.fail("%s has a name that is empty, too long or not printable ASCII", where)
	}
	if len(m.Authors) == 0 {
		c.fail("%s has no authors", where)
	}
	for _, a := range m.Authors {
		if !NameOK(a) {
			c.fail("%s has an author that is empty, too long or not printable ASCII", where)
		}
		if !m.Official && ReservedName(a) {
			c.fail("%s has the reserved author %q", where, a)
		}
	}
	if !m.Official && ReservedName(m.Name) {
		c.fail("%s has a reserved name", where)
	}
	c.text(where+".summary", m.Summary, MaxSummaryLen, true)
	switch m.Kind {
	case KindGame, KindAddon, KindLibrary:
	default:
		c.fail("%s kind %q", where, m.Kind)
	}
	for _, g := range m.Games {
		c.match(where+".games", gameIDRE, g)
	}
	for _, t := range slices.Concat(m.For, m.Provides) {
		c.match(where+".for", targetRE, t)
	}
	c.text(where+".license", m.License, 64, true)
	c.match(where+".repo", repoRE, m.Repo)
	if m.OwnerLogin != "" {
		c.match(where+".owner_login", loginRE, m.OwnerLogin)
	}
	if m.RepoID < 0 || m.OwnerID < 0 {
		c.fail("%s has a negative GitHub id", where)
	}
	if m.Icon.Builtin != "" {
		c.match(where+".icon", iconRE, m.Icon.Builtin)
	}
	c.file(where+".icon", m.Icon.PNG, MaxIconBytes)
	c.file(where+".readme", m.Readme, MaxTextBytes)
	switch m.Status {
	case StatusListed, StatusHold, StatusRemoved:
	default:
		c.fail("%s status %q", where, m.Status)
	}
	c.text(where+".note", m.Note, MaxNotesBytes, false)
	for _, ver := range sortedKeys(m.Versions) {
		checkVersion(c, id, ver, m.Repo, m.Versions[ver])
	}
}

func checkVersion(c *checker, id, ver, repo string, v Version) {
	where := "mods." + id + ".versions." + ver
	if _, ok := parseSemver(ver); !ok {
		c.fail("%s is not a version", where)
	}
	if r, tag, file, ok := releaseURL(v.URL); !ok || !strings.EqualFold(r, repo) {
		c.fail("%s url is not a release of %s", where, repo)
	} else if file != id+"-"+ver+PackExt || (tag != ver && tag != "v"+ver) {
		c.fail("%s url names another pack", where)
	}
	if v.Size <= 0 || v.Size > MaxPackBytes {
		c.fail("%s size %d", where, v.Size)
	}
	c.match(where+".sha256", sha256RE, v.SHA256)
	if _, _, ok := apiOf(v.API); !ok {
		c.fail("%s api %q", where, v.API)
	}
	for i, os := range v.OS {
		if (os != OSWindows && os != OSLinux) || slices.Contains(v.OS[:i], os) {
			c.fail("%s os %q", where, os)
		}
	}
	if v.Published.IsZero() {
		c.fail("%s has no release time", where)
	}
	for _, dep := range sortedKeys(v.Dependencies) {
		d := v.Dependencies[dep]
		c.match(where+".dependencies", modIDRE, dep)
		switch d.Kind {
		case DepRequired, DepOptional, DepIncompatible:
		default:
			c.fail("%s dependency kind %q", where, d.Kind)
		}
		if _, ok := parseRange(d.Version); !ok {
			c.fail("%s dependency range %q", where, d.Version)
		}
	}
	checkPermissions(c, where+".permissions", v.Permissions)
	c.file(where+".changelog", v.Changelog, MaxTextBytes)
	c.text(where+".notes", v.Notes, MaxNotesBytes, false)
}

func checkPermissions(c *checker, where string, p Permissions) {
	for _, name := range sortedKeys(p.Files) {
		d := p.Files[name]
		c.match(where+".files", declRE, name)
		for _, s := range []string{d.Path, d.Windows, d.Linux} {
			c.text(where+".files."+name, s, 1024, true)
		}
		if d.Path == "" {
			c.fail("%s.files.%s has no path", where, name)
		}
		if d.Setting != "" {
			c.match(where+".files."+name+".setting", declRE, d.Setting)
		}
		c.text(where+".files."+name+".label", d.Label, MaxNameLen, true)
		if d.Proton < 0 {
			c.fail("%s.files.%s proton", where, name)
		}
	}
	for _, s := range p.Paths {
		c.text(where+".paths", s, 1024, true)
		if s == "" {
			c.fail("%s.paths has an empty path", where)
		}
	}
	for _, port := range p.UDP {
		if port < 1 || port > 65535 {
			c.fail("%s.udp port %d", where, port)
		}
	}
	for _, t := range p.UDPForward {
		host, port, err := net.SplitHostPort(t)
		n, perr := strconv.Atoi(port)
		if err != nil || host == "" || perr != nil || n < 1 || n > 65535 || !TextOK(t) || len(t) > 260 {
			c.fail("%s.udp_forward %q", where, t)
		}
	}
}

// releaseURL splits a release download link,
// https://github.com/<owner>/<repo>/releases/download/<tag>/<file>.
func releaseURL(raw string) (repo, tag, file string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return "", "", "", false
	}
	s := strings.Split(u.Path, "/")
	if len(s) != 7 || s[0] != "" || s[3] != "releases" || s[4] != "download" ||
		!repoPartRE.MatchString(s[1]) || !repoPartRE.MatchString(s[2]) ||
		!assetNameRE.MatchString(s[5]) || !assetNameRE.MatchString(s[6]) ||
		s[5] == "." || s[5] == ".." || s[6] == "." || s[6] == ".." || path.Clean(u.Path) != u.Path {
		return "", "", "", false
	}
	return s[1] + "/" + s[2], s[5], s[6], true
}

// ReleaseLink is the download link of a release asset.
func ReleaseLink(repo, tag, asset string) string {
	return "https://github.com/" + repo + "/releases/download/" + tag + "/" + asset
}

// checkStats applies Bururu's rules to a stats file.
func checkStats(st *Stats) error {
	c := &checker{}
	switch {
	case st.Stats != StatsFormat:
		c.fail("stats format %d", st.Stats)
	case st.Generated.IsZero():
		c.fail("stats have no time")
	}
	return c.err
}

// readIndex reads index.json as Bururu does.
func readIndex(b []byte) (*Index, error) {
	var ix Index
	if err := decode(b, MaxIndexBytes, &ix, true); err != nil {
		return nil, err
	}
	if err := checkIndex(&ix); err != nil {
		return nil, err
	}
	return &ix, nil
}

// readStats reads stats.json as Bururu does: numbers only, unknown fields
// ignored.
func readStats(b []byte) (*Stats, error) {
	var st Stats
	if err := decode(b, MaxStatsBytes, &st, false); err != nil {
		return nil, err
	}
	if err := checkStats(&st); err != nil {
		return nil, err
	}
	return &st, nil
}

// sortedKeys are m's keys in order, so the first problem found is the
// same on every run.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
