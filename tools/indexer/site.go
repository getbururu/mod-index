package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
)

// The published site is the pages branch: v2/ holds what Bururu reads
// and v1/ what older Bururu builds read, each with index.json,
// stats.json, icons/ and text/. publish.yml and stats.yml each write
// their files into it and deploy the whole branch, one at a time, so
// neither drops the other's files.

var siteNameRE = regexp.MustCompile(`^(v1|v2)/(index\.json|stats\.json|icons/[0-9a-f]{64}\.png|text/[0-9a-f]{64}\.txt)$`)

// WriteSite puts files (paths like v1/index.json) into the site, the
// pages branch's checkout, and takes away anything else at the top of
// v1/ and v2/ that is none of the site's files. The rest of the branch
// stays as it is.
func WriteSite(site string, files map[string][]byte) error {
	for _, name := range sortedKeys(files) {
		if !siteNameRE.MatchString(name) {
			return fmt.Errorf("the site takes no file %q", name)
		}
		if err := writeFile(filepath.Join(site, filepath.FromSlash(name)), files[name]); err != nil {
			return err
		}
	}
	for _, dir := range []string{SiteV1, SiteV2} {
		ents, err := os.ReadDir(filepath.Join(site, dir))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, e := range ents {
			if !slices.Contains([]string{IndexFile, StatsFile, IconsDir, TextDir}, e.Name()) {
				if err := os.RemoveAll(filepath.Join(site, dir, e.Name())); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
