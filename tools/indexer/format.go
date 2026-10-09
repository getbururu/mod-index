package main

import (
	"slices"
	"strconv"
	"time"
)

// The published files' formats, as Bururu reads them
// (internal/modindex/types.go in Bururu's repo). Bururu reads them
// strictly and refuses a field it does not know, so these types and
// Bururu's must stay field for field alike.

// Where the list lives. Bururu has the same values.
const (
	ListOwner = "getbururu"
	ListRepo  = "mod-index"
	ListURL   = "https://github.com/" + ListOwner + "/" + ListRepo
	Home      = "https://" + ListOwner + ".github.io/" + ListRepo + "/v1/"
)

// The file formats.
const (
	IndexFormat = 1
	StatsFormat = 1
)

// The published files, relative to the home (the v1/ folder of the pages
// branch).
const (
	IndexFile = "index.json"
	StatsFile = "stats.json"
	IconsDir  = "icons"
	TextDir   = "text"
)

// Limits, as Bururu's.
const (
	MaxIndexBytes = 4 << 20
	MaxStatsBytes = 1 << 20
	MaxIconBytes  = 32 << 10
	MaxTextBytes  = 64 << 10
	MaxDepth      = 64
	MaxNameLen    = 64
	MaxSummaryLen = 200
	MaxNotesBytes = 2 << 10
	MaxPackBytes  = 64 << 20
)

// Patterns, as Bururu's.
const (
	ModIDPattern  = `^[a-z][a-z0-9-]{1,39}$`
	GameIDPattern = `^[a-z0-9][a-z0-9-]{1,47}$`
	SHA256Pattern = `^[0-9a-f]{64}$`
)

// Values of the formats' enumerations.
const (
	KindGame    = "game"
	KindAddon   = "addon"
	KindLibrary = "library"

	StatusListed  = "listed"
	StatusHold    = "hold"
	StatusRemoved = "removed"

	LevelWarn  = "warn"
	LevelBlock = "block"

	AdviseWarn         = "warn"
	AdviseCommunityOff = "community_off"

	DiffFirst    = "first"
	DiffSame     = "same"
	DiffNarrower = "narrower"
	DiffWider    = "wider"

	CheckPass = "pass"

	OSWindows = "windows"
	OSLinux   = "linux"

	DepRequired     = "required"
	DepOptional     = "optional"
	DepIncompatible = "incompatible"

	PackExt = ".brr"

	// ReviewBy is who reviews a version: a role, never a name
	ReviewBy = "maintainer"
)

// Index is index.json.
type Index struct {
	Index      int                   `json:"index"`
	Serial     int64                 `json:"serial"`
	MinClient  string                `json:"min_client"`
	Games      map[string]GameRecord `json:"games"`
	Mods       map[string]Mod        `json:"mods"`
	Revoked    []Revocation          `json:"revoked"`
	Advisories []Advisory            `json:"advisories"`
}

// Mod is one listed mod.
type Mod struct {
	Name       string             `json:"name"`
	Summary    string             `json:"summary"`
	Authors    []string           `json:"authors"`
	Official   bool               `json:"official"`
	Kind       string             `json:"kind"`
	Games      []string           `json:"games"`
	For        []string           `json:"for"`
	Provides   []string           `json:"provides"`
	License    string             `json:"license"`
	Repo       string             `json:"repo"`
	RepoID     int64              `json:"repo_id"`
	OwnerID    int64              `json:"owner_id"`
	OwnerLogin string             `json:"owner_login"`
	Icon       Icon               `json:"icon"`
	Readme     *FileRef           `json:"readme"`
	Status     string             `json:"status"`
	Note       string             `json:"note"`
	Versions   map[string]Version `json:"versions"`
}

// Icon is a mod's icon: a normalised PNG and a name of the window's set.
type Icon struct {
	Builtin string   `json:"builtin"`
	PNG     *FileRef `json:"png"`
}

// FileRef pins a published file by hash and exact size.
type FileRef struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Version is one listed version of a mod.
type Version struct {
	URL          string                `json:"url"`
	Size         int64                 `json:"size"`
	SHA256       string                `json:"sha256"`
	API          string                `json:"api"`
	OS           []string              `json:"os"`
	Prerelease   bool                  `json:"prerelease"`
	Published    time.Time             `json:"published"`
	Dependencies map[string]Dependency `json:"dependencies"`
	Permissions  Permissions           `json:"permissions"`
	Changelog    *FileRef              `json:"changelog"`
	Notes        string                `json:"notes"`
}

// Dependency is one dependency of a version.
type Dependency struct {
	Kind    string `json:"kind"`
	Version string `json:"version"`
}

// Permissions is what a version may reach.
type Permissions struct {
	Files      map[string]PathDecl `json:"files"`
	Paths      []string            `json:"paths"`
	Screen     bool                `json:"screen"`
	Keyboard   bool                `json:"keyboard"`
	UDP        []int               `json:"udp"`
	UDPForward []string            `json:"udp_forward"`
}

// PathDecl is one declared path.
type PathDecl struct {
	Path    string `json:"path"`
	Proton  int    `json:"proton,omitempty"`
	Windows string `json:"windows,omitempty"`
	Linux   string `json:"linux,omitempty"`
	Setting string `json:"setting,omitempty"`
	Label   string `json:"label,omitempty"`
}

// Template is the path as it stands on goos.
func (p PathDecl) Template(goos string) string {
	switch {
	case goos == OSWindows && p.Windows != "":
		return p.Windows
	case goos == OSLinux && p.Linux != "":
		return p.Linux
	}
	return p.Path
}

// Items are p's permission items on goos, spelt as Bururu and its
// installer spell them.
func (p Permissions) Items(goos string) []string {
	var out []string
	names := make([]string, 0, len(p.Files))
	for name := range p.Files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		out = append(out, "files:"+name+"="+p.Files[name].Template(goos))
	}
	for _, x := range p.Paths {
		out = append(out, "path:"+x)
	}
	if p.Screen {
		out = append(out, "screen")
	}
	if p.Keyboard {
		out = append(out, "keyboard")
	}
	for _, port := range p.UDP {
		out = append(out, "udp:"+strconv.Itoa(port))
	}
	for _, t := range p.UDPForward {
		out = append(out, "forward:"+t)
	}
	return out
}

// Checks is what the list's checks said of a version (kept in state/).
type Checks struct {
	Result    string `json:"result"`
	Warnings  int    `json:"warnings"`
	Taste     int    `json:"taste"`
	CheckedBy string `json:"checked_by"`
}

// Revocation takes versions of a mod back.
type Revocation struct {
	ID       string   `json:"id"`
	Versions []string `json:"versions"`
	SHA256   []string `json:"sha256"`
	Level    string   `json:"level"`
	Reason   string   `json:"reason"`
	Date     Date     `json:"date"`
}

// Advisory names Bururu versions with a known flaw.
type Advisory struct {
	Below  string `json:"below"`
	Action string `json:"action"`
	Text   string `json:"text"`
}

// GameRecord is a game the list knows.
type GameRecord struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Stores     GameStores        `json:"stores"`
	Exe        map[string]ExeSet `json:"exe"`
	Markers    []string          `json:"markers"`
	Proton     int               `json:"proton,omitempty"`
	Controller *GameController   `json:"controller,omitempty"`
}

// GameController is a game's profile: the controller Bururu shows the
// game on each connection, and whether the game needs Steam Input off.
// Bururu skips a key it does not know in it, and takes a value it does
// not know as no profile for that connection.
type GameController struct {
	Cable      string `json:"cable,omitempty"`       // the DualSense on its cable: one of Pads
	Bluetooth  string `json:"bluetooth,omitempty"`   // the DualSense on Bluetooth: one of Pads
	SteamInput string `json:"steam_input,omitempty"` // SteamInputOff: the game needs Steam Input off
}

// Pads are the controllers a game profile may name: the real controller
// itself, or one of Bururu's virtual ones.
var Pads = []string{"direct", "dualsense", "dualshock4", "xbox360"}

// SteamInputOff is GameController.SteamInput's one value.
const SteamInputOff = "off"

// GameStores are a game's ids in each store.
type GameStores struct {
	Steam     []int64  `json:"steam"`
	Epic      []EpicID `json:"epic"`
	GOG       []int64  `json:"gog"`
	Xbox      []XboxID `json:"xbox"`
	Ubisoft   []int64  `json:"ubisoft"`
	BattleNet []string `json:"battlenet"`
	EA        []string `json:"ea"`
}

// EpicID is a game's id in the Epic launcher's records.
type EpicID struct {
	App       string `json:"app"`
	Namespace string `json:"namespace"`
	Item      string `json:"item"`
}

// XboxID is a game's id in the Xbox app.
type XboxID struct {
	Family  string `json:"family"`
	StoreID string `json:"store_id"`
}

// ExeSet is a game's executables on one OS.
type ExeSet struct {
	Game     []string `json:"game"`
	Launcher []string `json:"launcher,omitempty"`
	Match    string   `json:"match,omitempty"`
}

// Stats is stats.json.
type Stats struct {
	Stats     int                 `json:"stats"`
	Generated time.Time           `json:"generated"`
	Mods      map[string]ModStats `json:"mods"`
}

// ModStats are one mod's numbers.
type ModStats struct {
	Downloads int64            `json:"downloads"`
	Stars     int64            `json:"stars"`
	Versions  map[string]int64 `json:"versions"`
}

// Date is a day, YYYY-MM-DD, in UTC.
type Date string

// Time is 00:00 UTC of d; ok is false when d is not a day.
func (d Date) Time() (t time.Time, ok bool) {
	if len(d) != len(time.DateOnly) {
		return time.Time{}, false
	}
	t, err := time.Parse(time.DateOnly, string(d))
	return t, err == nil
}

// DateOf is the UTC day of t.
func DateOf(t time.Time) Date { return Date(t.UTC().Format(time.DateOnly)) }
