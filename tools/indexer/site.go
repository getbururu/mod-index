package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
)

// The published site is the pages branch: v1/ holds what Bururu reads,
// index.json, stats.json, icons/ and text/. publish.yml and stats.yml
// each write their files into it and deploy the whole branch, one at a
// time, so neither drops the other's files.

var siteNameRE = regexp.MustCompile(`^(index\.json|stats\.json|icons/[0-9a-f]{64}\.png|text/[0-9a-f]{64}\.txt)$`)

// WriteSite puts files (paths under v1/) into the site at v1, and takes
// away anything else at the top of v1/ that is none of the site's files.
func WriteSite(v1 string, files map[string][]byte) error {
	for _, name := range sortedKeys(files) {
		if !siteNameRE.MatchString(name) {
			return fmt.Errorf("the site takes no file %q", name)
		}
		if err := writeFile(filepath.Join(v1, filepath.FromSlash(name)), files[name]); err != nil {
			return err
		}
	}
	ents, err := os.ReadDir(v1)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if !slices.Contains([]string{IndexFile, StatsFile, IconsDir, TextDir}, e.Name()) {
			if err := os.RemoveAll(filepath.Join(v1, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
