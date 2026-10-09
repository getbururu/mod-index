package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// The indexer cannot import Bururu's packages, so it runs the `bururu`
// command line, built from the ref in tools/bururu.ref, and reads its
// JSON: `mod check --json` and `mod listing --json`. Both only read the
// pack.

// CheckResult is `bururu mod check --json` (format 1).
type CheckResult struct {
	Check    int    `json:"check"`
	ID       string `json:"id"`
	Version  string `json:"version"`
	Result   string `json:"result"` // "pass" or "fail"
	Errors   int    `json:"errors"`
	Warnings int    `json:"warnings"`
	Taste    int    `json:"taste"`
	Problems []struct {
		File string `json:"file"`
		Line int    `json:"line"`
		Col  int    `json:"col"`
		Text string `json:"text"`
		Hint string `json:"hint"`
		Warn bool   `json:"warn"`
	} `json:"problems"`
	TasteNotes []string `json:"taste_notes"`
	CheckedBy  string   `json:"checked_by"`
}

// Listing is `bururu mod listing --json` (format 1): what the list
// records of a pack.
type Listing struct {
	Listing      int                   `json:"listing"`
	ID           string                `json:"id"`
	Version      string                `json:"version"`
	SHA256       string                `json:"sha256"`
	Size         int64                 `json:"size"`
	Name         string                `json:"name"`
	Summary      string                `json:"summary"`
	Authors      []string              `json:"authors"`
	Kind         string                `json:"kind"`
	Games        []string              `json:"games"`
	For          []string              `json:"for"`
	Provides     []string              `json:"provides"`
	License      string                `json:"license"`
	Homepage     string                `json:"homepage"`
	API          string                `json:"api"`
	OS           []string              `json:"os"`
	Icon         Icon                  `json:"icon"`
	Readme       *FileRef              `json:"readme"`
	Changelog    *FileRef              `json:"changelog"`
	Notes        string                `json:"notes"`
	Dependencies map[string]Dependency `json:"dependencies"`
	Permissions  Permissions           `json:"permissions"`
	GameRecords  map[string]GameRecord `json:"game_records"`
	Files        []struct {
		Path   string `json:"path"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
	Problems []string `json:"problems"`
	Reserved []string `json:"reserved"`
	Warnings []string `json:"warnings"`
}

// The formats of the two answers this indexer reads.
const (
	CheckJSONFormat   = 1
	ListingJSONFormat = 1
)

// Runner runs Bururu's checks on a pack. Tests use a fake.
type Runner interface {
	Check(ctx context.Context, pack string, with []string) (*CheckResult, error)
	Listing(ctx context.Context, pack, iconOut, textOut string) (*Listing, error)
}

// ExecRunner runs the bururu program at Path.
type ExecRunner struct {
	Path    string
	Timeout time.Duration // per run; 0: two minutes
}

func (r ExecRunner) run(ctx context.Context, v any, args ...string) error {
	if r.Path == "" {
		return errors.New("no bururu program, so packs cannot be checked")
	}
	t := r.Timeout
	if t == 0 {
		t = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, t)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.Path, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &limited{w: &out, n: 16 << 20}
	cmd.Stderr = &limited{w: &errOut, n: 64 << 10}
	runErr := cmd.Run()
	// both commands print their JSON also when they exit 1
	if out.Len() > 0 {
		d := json.NewDecoder(&out)
		if err := d.Decode(v); err == nil {
			return nil
		}
	}
	if runErr == nil {
		runErr = errors.New("no JSON")
	}
	return fmt.Errorf("bururu %s: %v: %s", args[0]+" "+args[1], runErr, bytes.TrimSpace(errOut.Bytes()))
}

// Check runs `bururu mod check --json [--with <dep>...] <pack>`.
func (r ExecRunner) Check(ctx context.Context, pack string, with []string) (*CheckResult, error) {
	args := []string{"mod", "check", "--json"}
	for _, w := range with {
		args = append(args, "--with", w)
	}
	var c CheckResult
	if err := r.run(ctx, &c, append(args, pack)...); err != nil {
		return nil, err
	}
	if c.Check != CheckJSONFormat {
		return nil, fmt.Errorf("bururu mod check: format %d; this indexer reads %d", c.Check, CheckJSONFormat)
	}
	return &c, nil
}

// Listing runs `bururu mod listing --json --icon-out <dir> --text-out
// <dir> <pack>`.
func (r ExecRunner) Listing(ctx context.Context, pack, iconOut, textOut string) (*Listing, error) {
	var l Listing
	if err := r.run(ctx, &l, "mod", "listing", "--json", "--icon-out", iconOut, "--text-out", textOut, pack); err != nil {
		return nil, err
	}
	if l.Listing != ListingJSONFormat {
		return nil, fmt.Errorf("bururu mod listing: format %d; this indexer reads %d", l.Listing, ListingJSONFormat)
	}
	return &l, nil
}

// limited keeps at most n bytes of what is written to it.
type limited struct {
	w interface{ Write([]byte) (int, error) }
	n int
}

func (l *limited) Write(p []byte) (int, error) {
	k := min(len(p), l.n)
	if k > 0 {
		if _, err := l.w.Write(p[:k]); err != nil {
			return 0, err
		}
		l.n -= k
	}
	return len(p), nil
}
