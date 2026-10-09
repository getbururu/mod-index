// Command indexer builds Bururu's mod list from this repo: it checks the
// entries authors add, polls their repos for new releases, proposes each
// new version as a pull request, builds the list and counts downloads
// and stars. The workflows under .github/workflows run it; see
// docs/listing.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const usage = `indexer: Bururu's mod list (getbururu/mod-index)

  indexer validate [-root .]
      read every data file strictly; exit 1 on any problem
  indexer check-pr -base <dir> -pr <dir> [-bururu <program> -ref <ref>] -out <dir> [-author <login>] [-association <a>]
      the checks of a pull request: writes <out>/report.txt and result.json
  indexer poll [-root .] -bururu <program> -ref <ref> -out <dir> [-cache <dir>] [-only <id>]
      find new versions and write the proposed changes into <out> (read-only job)
  indexer plan -in <dir> [-root .]
      read the poll's result strictly and print: key, id, branch, title
  indexer apply -in <dir> -key <key> [-root .] -body <file>
      write one change into the repo and its pull request's body into <file>
  indexer build [-root .] -site <v1 dir> -out <dir>
      build index.json from the repo; nothing when the published one says the same
  indexer site -in <dir> -site <v1 dir>
      put built files into the site
  indexer stats [-root .] -out <dir> [-now <RFC 3339>]
      count downloads and stars into stats.json
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	err := run(context.Background(), os.Args[1], os.Args[2:])
	var ex exitError
	switch {
	case errors.As(err, &ex):
		os.Exit(int(ex))
	case err != nil:
		fmt.Fprintln(os.Stderr, "indexer:", err)
		os.Exit(1)
	}
}

// exitError ends the program with that code after the command printed
// what it found.
type exitError int

func (e exitError) Error() string { return fmt.Sprintf("exit %d", int(e)) }

func run(ctx context.Context, cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	root := fs.String("root", ".", "the repo's checkout")
	parse := func() error { return fs.Parse(args) }
	switch cmd {
	case "validate":
		if err := parse(); err != nil {
			return err
		}
		_, probs := LoadRepo(*root)
		for _, p := range probs {
			fmt.Println(p)
		}
		if len(probs) > 0 {
			return exitError(1)
		}
		fmt.Println("every data file reads")
		return nil

	case "check-pr":
		base := fs.String("base", "", "the base branch's checkout")
		pr := fs.String("pr", "", "the pull request's checkout")
		bururu := fs.String("bururu", "", "the bururu program")
		ref := fs.String("ref", "", "the Bururu ref it was built from")
		out := fs.String("out", "", "where the report goes")
		author := fs.String("author", "", "the pull request's author")
		assoc := fs.String("association", "", "GitHub's author association")
		if err := parse(); err != nil {
			return err
		}
		p, err := poller(*root, *bururu, *ref, "")
		if err != nil {
			return err
		}
		res := CheckPullRequest(ctx, p, *base, *pr, *author, *assoc)
		if err := writeFile(filepath.Join(*out, "report.txt"), []byte(res.Report)); err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(*out, "result.json"), map[string]bool{"pass": res.Pass}, "  "); err != nil {
			return err
		}
		fmt.Print(res.Report)
		if !res.Pass {
			return exitError(1)
		}
		return nil

	case "poll":
		bururu := fs.String("bururu", "", "the bururu program")
		ref := fs.String("ref", "", "the Bururu ref it was built from")
		out := fs.String("out", "", "where the changes go")
		cache := fs.String("cache", "", "a folder for GitHub's ETags")
		only := fs.String("only", "", "poll this entry only")
		if err := parse(); err != nil {
			return err
		}
		if *bururu == "" {
			return errors.New("poll needs -bururu: the program whose mod check and mod listing judge each pack")
		}
		p, err := poller(*root, *bururu, *ref, *cache)
		if err != nil {
			return err
		}
		r, probs := LoadRepo(*root)
		if len(probs) > 0 {
			return fmt.Errorf("the repo has problems:\n%s", strings.Join(probs, "\n"))
		}
		p.Repo = r
		var changes []*Change
		var skipped []string
		for _, id := range sortedKeys(r.Entries) {
			if *only != "" && id != *only {
				continue
			}
			c, s, err := p.Poll(ctx, r.Entries[id])
			if err != nil {
				skipped = append(skipped, id+": "+err.Error())
				continue
			}
			changes = append(changes, c...)
			skipped = append(skipped, s...)
		}
		if err := WriteChanges(*out, changes, skipped); err != nil {
			return err
		}
		fmt.Printf("%d changes, %d releases not taken, %d GitHub requests\n", len(changes), len(skipped), p.GH.Requests)
		for _, c := range changes {
			fmt.Println("change:", c.Title)
		}
		for _, s := range skipped {
			fmt.Println("not taken:", s)
		}
		return nil

	case "plan":
		in := fs.String("in", "", "the poll's result")
		if err := parse(); err != nil {
			return err
		}
		r, probs := LoadRepo(*root)
		if len(probs) > 0 {
			return fmt.Errorf("the repo has problems:\n%s", strings.Join(probs, "\n"))
		}
		cf, err := ReadChanges(*in)
		if err != nil {
			return err
		}
		for _, h := range cf.List {
			if _, err := ReadProposal(*in, h, r); err != nil {
				return err
			}
			fmt.Printf("%s\t%s\tbot/%s\t%s\n", h.Key, h.ID, h.Key, h.Title)
		}
		return nil

	case "apply":
		in := fs.String("in", "", "the poll's result")
		key := fs.String("key", "", "the change")
		body := fs.String("body", "", "where the pull request's body goes")
		if err := parse(); err != nil {
			return err
		}
		r, probs := LoadRepo(*root)
		if len(probs) > 0 {
			return fmt.Errorf("the repo has problems:\n%s", strings.Join(probs, "\n"))
		}
		cf, err := ReadChanges(*in)
		if err != nil {
			return err
		}
		for _, h := range cf.List {
			if h.Key != *key {
				continue
			}
			p, err := ReadProposal(*in, h, r)
			if err != nil {
				return err
			}
			if err := p.Apply(*root); err != nil {
				return err
			}
			if _, probs := LoadRepo(*root); len(probs) > 0 {
				return fmt.Errorf("after the change the repo has problems:\n%s", strings.Join(probs, "\n"))
			}
			return writeFile(*body, []byte(p.Body()))
		}
		return fmt.Errorf("no change %q", *key)

	case "build":
		site := fs.String("site", "", "the v1 folder of the pages branch")
		out := fs.String("out", "", "where the index goes")
		if err := parse(); err != nil {
			return err
		}
		r, probs := LoadRepo(*root)
		if len(probs) > 0 {
			return fmt.Errorf("the repo has problems:\n%s", strings.Join(probs, "\n"))
		}
		prev, err := ReadSite(*site)
		if err != nil {
			return err
		}
		b, err := BuildIndex(r, prev)
		if err != nil {
			return err
		}
		if b.Same {
			fmt.Printf("%s: serial %d says the same; nothing to publish\n", IndexFile, b.Index.Serial)
			return nil
		}
		files := map[string][]byte{IndexFile: b.File}
		for name, fb := range b.Files {
			files[name] = fb
		}
		for _, name := range sortedKeys(files) {
			if err := writeFile(filepath.Join(*out, filepath.FromSlash(name)), files[name]); err != nil {
				return err
			}
		}
		fmt.Printf("%s: serial %d, %d mods, %d games, %d bytes\n", IndexFile, b.Index.Serial, len(b.Index.Mods), len(b.Index.Games), len(b.File))
		return nil

	case "site":
		in := fs.String("in", "", "the built files")
		site := fs.String("site", "", "the v1 folder of the pages branch")
		if err := parse(); err != nil {
			return err
		}
		files := map[string][]byte{}
		err := filepath.WalkDir(*in, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(*in, path)
			b, err := os.ReadFile(path)
			files[filepath.ToSlash(rel)] = b
			return err
		})
		if errors.Is(err, os.ErrNotExist) {
			fmt.Println("nothing new for the site")
			return nil
		}
		if err != nil {
			return err
		}
		if err := os.MkdirAll(*site, 0o755); err != nil {
			return err
		}
		if err := WriteSite(*site, files); err != nil {
			return err
		}
		fmt.Printf("%d files into the site\n", len(files))
		return nil

	case "stats":
		out := fs.String("out", "", "where the stats go")
		now := fs.String("now", "", "the time (RFC 3339); default now")
		if err := parse(); err != nil {
			return err
		}
		t, err := nowFlag(*now)
		if err != nil {
			return err
		}
		r, probs := LoadRepo(*root)
		if len(probs) > 0 {
			return fmt.Errorf("the repo has problems:\n%s", strings.Join(probs, "\n"))
		}
		st, notes, err := CountStats(ctx, newGitHub(""), r, t)
		if err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(*out, StatsFile), st, ""); err != nil {
			return err
		}
		for _, n := range notes {
			fmt.Println("note:", n)
		}
		fmt.Printf("%s: %d mods\n", StatsFile, len(st.Mods))
		return nil
	}
	fmt.Fprint(os.Stderr, usage)
	return exitError(2)
}

// poller is a Poller with GitHub, the bururu program (none: packs are
// not checked) and a scratch folder.
func poller(root, bururu, ref, cache string) (*Poller, error) {
	work, err := os.MkdirTemp("", "indexer-")
	if err != nil {
		return nil, err
	}
	if ref == "" {
		ref = "unknown"
	}
	p := &Poller{GH: newGitHub(cache), Ref: ref, Work: work, Now: time.Now().UTC()}
	if bururu != "" {
		p.Run = ExecRunner{Path: bururu}
	}
	return p, nil
}

func newGitHub(cache string) *GitHub {
	api := os.Getenv("GITHUB_API_URL")
	if api == "" {
		api = "https://api.github.com"
	}
	return &GitHub{API: api, Token: os.Getenv("GITHUB_TOKEN"), Cache: cache}
}

func nowFlag(s string) (time.Time, error) {
	if s == "" {
		return time.Now().UTC(), nil
	}
	return time.Parse(time.RFC3339, s)
}
