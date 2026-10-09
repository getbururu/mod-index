package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// TestWorkflowRules checks every workflow against the list's security
// rules: the workflows are the root of trust for every player, so a
// change that breaks a rule fails here before review.
func TestWorkflowRules(t *testing.T) {
	dir := filepath.Join("..", "..", ".github", "workflows")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	want := []string{"check-entry.yml", "check-report.yml", "poll.yml", "publish.yml", "stats.yml"}
	if !slices.Equal(names, want) {
		t.Fatalf("the workflows are %v; this test knows %v (a new workflow needs its rules here first)", names, want)
	}
	for _, name := range names {
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range workflowProblems(name, string(src)) {
			t.Errorf("%s: %s", name, p)
		}
		// the reader saw every step: as many uses and runs as the lines say
		tree, err := parseYAML(string(src))
		if err != nil {
			t.Fatal(err)
		}
		jobs, _ := tree.(map[string]any)["jobs"].(map[string]any)
		uses, runs := 0, 0
		for _, j := range jobs {
			steps, _ := j.(map[string]any)["steps"].([]any)
			if len(steps) == 0 {
				t.Errorf("%s: a job without steps", name)
			}
			for _, st := range steps {
				m := st.(map[string]any)
				if str(m["uses"]) != "" {
					uses++
				}
				if str(m["run"]) != "" {
					runs++
				}
			}
		}
		if n := len(regexp.MustCompile(`(?m)^\s*(- )?uses: `).FindAllString(string(src), -1)); n != uses {
			t.Errorf("%s: %d uses lines, %d read", name, n, uses)
		}
		if n := len(regexp.MustCompile(`(?m)^\s*(- )?run: `).FindAllString(string(src), -1)); n != runs || runs == 0 {
			t.Errorf("%s: %d run lines, %d read", name, n, runs)
		}
	}
}

// TestWorkflowRulesCatch: each rule catches a workflow that breaks it.
func TestWorkflowRulesCatch(t *testing.T) {
	good := `name: x
on:
  workflow_dispatch:
permissions: {}
jobs:
  a:
    runs-on: ubuntu-latest
    permissions:
      contents: read
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - run: echo hi
`
	if p := workflowProblems("stats.yml", good); len(p) > 0 {
		t.Fatalf("a good workflow: %v", p)
	}
	cases := map[string]struct{ from, to string }{
		"pull_request_target":    {"  workflow_dispatch:\n", "  pull_request_target:\n"},
		"permissions: {}":        {"permissions: {}\n", "permissions: write-all\n"},
		"names no permissions":   {"    permissions:\n      contents: read\n", ""},
		"not pinned":             {"@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1", "@v7"},
		"persist-credentials":    {"        with:\n          persist-credentials: false\n", ""},
		"${{":                    {"echo hi", "echo ${{ github.event.issue.title }}"},
		"self-hosted":            {"ubuntu-latest", "self-hosted"},
		"secret":                 {"echo hi", "echo $X\n        env:\n          X: ${{ secrets.MY_PAT }}"},
		"write permissions on a": {"  workflow_dispatch:\n", "  pull_request:\n"},
	}
	for want, c := range cases {
		bad := strings.Replace(good, c.from, c.to, 1)
		if want == "write permissions on a" {
			bad = strings.Replace(bad, "contents: read", "contents: write", 1)
		}
		if bad == good {
			t.Fatalf("%s: the case changes nothing", want)
		}
		p := workflowProblems("stats.yml", bad)
		if !slices.ContainsFunc(p, func(s string) bool { return strings.Contains(s, want) }) {
			t.Errorf("%s: not caught, got %v", want, p)
		}
	}
}

var (
	pinnedRE  = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+@[0-9a-f]{40}$`)
	secretsRE = regexp.MustCompile(`secrets\.([A-Za-z0-9_]+)`)
)

// workflowProblems are the rules a workflow file breaks.
func workflowProblems(name, src string) []string {
	var probs []string
	add := func(format string, args ...any) { probs = append(probs, fmt.Sprintf(format, args...)) }
	tree, err := parseYAML(src)
	if err != nil {
		return []string{"does not parse: " + err.Error()}
	}
	root, _ := tree.(map[string]any)
	if root == nil {
		return []string{"is not a mapping"}
	}

	// triggers
	on, _ := root["on"].(map[string]any)
	if on == nil {
		add("names no triggers under on:")
	}
	triggers := sortedKeys(on)
	allowed := map[string][]string{
		"check-entry.yml":  {"pull_request"},
		"check-report.yml": {"workflow_run"},
		"poll.yml":         {"schedule", "workflow_dispatch", "push"},
		"publish.yml":      {"push", "workflow_dispatch"},
		"stats.yml":        {"schedule", "workflow_dispatch"},
	}
	for _, tr := range triggers {
		if tr == "pull_request_target" {
			add("runs on pull_request_target, which gives pull request content a write token and secrets")
			continue
		}
		if !slices.Contains(allowed[name], tr) {
			add("runs on %s, which this workflow may not", tr)
		}
	}
	untrusted := slices.Contains(triggers, "pull_request")
	if push, ok := on["push"].(map[string]any); ok {
		if b := list(push["branches"]); !slices.Equal(b, []string{"main"}) {
			add("runs on pushes to %v; only main", b)
		}
		if name == "publish.yml" {
			if p := list(push["paths"]); !slices.Equal(p, []string{"state/**", "games/**", "policy/**", "tools/indexer/**"}) {
				add("publishes on pushes to %v; only state/**, games/**, policy/**, tools/indexer/**", p)
			}
		}
	}
	if wr, ok := on["workflow_run"].(map[string]any); ok && !slices.Equal(list(wr["workflows"]), []string{"check-entry"}) {
		add("follows %v; only check-entry", list(wr["workflows"]))
	}

	// least permissions: nothing at the top, each job names its own
	if p, ok := root["permissions"].(map[string]any); !ok || len(p) != 0 {
		add("does not start with permissions: {}")
	}
	jobs, _ := root["jobs"].(map[string]any)
	if len(jobs) == 0 {
		add("has no jobs")
	}
	for _, jn := range sortedKeys(jobs) {
		job, _ := jobs[jn].(map[string]any)
		where := "job " + jn
		perms, ok := job["permissions"].(map[string]any)
		if !ok {
			add("%s names no permissions", where)
		}
		writes := false
		for _, k := range sortedKeys(perms) {
			switch v := str(perms[k]); v {
			case "read", "none":
			case "write":
				writes = true
			default:
				add("%s: permission %s: %q", where, k, v)
			}
		}
		contentsWrite := str(perms["contents"]) == "write"
		if r := str(job["runs-on"]); r != "ubuntu-latest" {
			add("%s runs on %q: GitHub's own ubuntu-latest only, never self-hosted", where, r)
		}
		env := str(job["environment"])
		if m, ok := job["environment"].(map[string]any); ok {
			env = str(m["name"])
		}
		all := strings.Join(strings_(job), "\n")
		secrets := secretsRE.FindAllStringSubmatch(all, -1)
		for _, s := range secrets {
			if s[1] != "BURURU_READ" || name != "poll.yml" || jn != "fetch" {
				add("%s reads the secret %s; only BURURU_READ in poll.yml's fetch job, to read Bururu's repo (GITHUB_TOKEN is github.token)", where, s[1])
			}
		}
		if untrusted && (writes || len(secrets) > 0 || env != "") {
			add("%s has write permissions on a pull_request workflow, a secret or an environment: pull request content runs here", where)
		}
		if name == "poll.yml" && jn == "fetch" && (len(perms) != 1 || str(perms["contents"]) != "read") {
			add("%s parses packs nobody reviewed: contents: read and nothing else", where)
		}
		if name == "check-report.yml" {
			for _, k := range sortedKeys(perms) {
				if !slices.Contains([]string{"actions", "pull-requests", "statuses"}, k) {
					add("%s: check-report needs actions, pull-requests and statuses only, not %s", where, k)
				}
			}
		}
		if contentsWrite && (name == "publish.yml" || name == "stats.yml") {
			c, _ := job["concurrency"].(map[string]any)
			if str(c["group"]) != "pages" || str(c["cancel-in-progress"]) != "false" {
				add("%s writes the pages branch without concurrency group pages, cancel-in-progress false", where)
			}
		}
		steps, _ := job["steps"].([]any)
		for i, st := range steps {
			step, _ := st.(map[string]any)
			sw := fmt.Sprintf("%s step %d", where, i+1)
			uses := str(step["uses"])
			if uses != "" {
				if !pinnedRE.MatchString(uses) {
					add("%s uses %s: not pinned to a full 40-hex commit SHA", sw, uses)
				}
				if strings.HasPrefix(uses, "actions/checkout@") {
					with, _ := step["with"].(map[string]any)
					if name == "check-report.yml" {
						add("%s checks out code: check-report checks out nothing", sw)
					}
					if str(with["persist-credentials"]) != "false" && !contentsWrite {
						add("%s: actions/checkout without persist-credentials: false in a job that pushes nothing", sw)
					}
					if repo := str(with["repository"]); repo != "" && (repo != "getbururu/bururu" || name != "poll.yml" || jn != "fetch") {
						add("%s checks out %s", sw, repo)
					}
					if untrusted && str(with["persist-credentials"]) != "false" {
						add("%s keeps credentials in a pull_request job: persist-credentials", sw)
					}
				}
			}
			run := str(step["run"])
			if strings.Contains(run, "${{") {
				add("%s puts ${{ }} into a script: pass it through env:", sw)
			}
			if strings.Contains(run, "git push") {
				if !contentsWrite {
					add("%s pushes without contents: write", sw)
				}
				for _, line := range strings.Split(run, "\n") {
					if !strings.Contains(line, "git push") {
						continue
					}
					switch name {
					case "publish.yml", "stats.yml":
						if !strings.Contains(line, "HEAD:refs/heads/pages") {
							add("%s pushes somewhere else than the pages branch", sw)
						}
					case "poll.yml":
						if !strings.Contains(line, `HEAD:refs/heads/$branch`) {
							add("%s pushes somewhere else than a bot branch", sw)
						}
					default:
						add("%s pushes", sw)
					}
				}
			}
			if name == "poll.yml" && jn == "propose" && strings.Contains(run, "bururu") {
				add("%s runs Bururu in the writing job", sw)
			}
		}
	}
	// the jobs that write never run the pull request's code
	if name == "check-entry.yml" {
		for _, jn := range sortedKeys(jobs) {
			job, _ := jobs[jn].(map[string]any)
			perms, _ := job["permissions"].(map[string]any)
			if len(perms) != 1 || str(perms["contents"]) != "read" {
				add("job %s: check-entry's jobs have contents: read and nothing else", jn)
			}
		}
	}
	if name == "publish.yml" || name == "stats.yml" {
		c, _ := root["concurrency"].(map[string]any)
		if name == "publish.yml" && (str(c["group"]) != "publish" || str(c["cancel-in-progress"]) != "false") {
			add("publish needs concurrency group publish, cancel-in-progress false, so waiting runs collapse")
		}
	}
	sort.Strings(probs)
	return probs
}

// list is a YAML sequence of scalars.
func list(v any) []string {
	var out []string
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			out = append(out, str(e))
		}
	case string:
		out = append(out, x)
	}
	return out
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// strings_ are every scalar under v.
func strings_(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, e := range x {
			out = append(out, strings_(e)...)
		}
		return out
	case map[string]any:
		var out []string
		for _, k := range sortedKeys(x) {
			out = append(out, k)
			out = append(out, strings_(x[k])...)
		}
		return out
	}
	return nil
}

// parseYAML reads the part of YAML the workflows use: block mappings and
// sequences, flow sequences and mappings of scalars, block scalars,
// quoted and plain scalars, comments. It is a test's reader, strict
// about what it does not know.
func parseYAML(src string) (any, error) {
	p := &yamlParser{raw: strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")}
	for i, l := range p.raw {
		t := strings.TrimLeft(l, " ")
		if strings.HasPrefix(t, "\t") {
			return nil, fmt.Errorf("line %d: a tab", i+1)
		}
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		p.lines = append(p.lines, yamlLine{raw: i, ind: len(l) - len(t), text: t})
	}
	if len(p.lines) == 0 {
		return nil, fmt.Errorf("empty")
	}
	v, err := p.block(p.lines[0].ind)
	if err != nil {
		return nil, err
	}
	if p.i < len(p.lines) {
		return nil, fmt.Errorf("line %d: unexpected", p.lines[p.i].raw+1)
	}
	return v, nil
}

type yamlLine struct {
	raw  int // index in raw
	ind  int
	text string
}

type yamlParser struct {
	raw   []string
	lines []yamlLine
	i     int
}

func (p *yamlParser) block(ind int) (any, error) {
	l := p.lines[p.i]
	if l.text == "-" || strings.HasPrefix(l.text, "- ") {
		return p.seq(ind)
	}
	return p.mapping(ind)
}

func (p *yamlParser) mapping(ind int) (any, error) {
	m := map[string]any{}
	for p.i < len(p.lines) {
		l := p.lines[p.i]
		if l.ind < ind || (l.ind == ind && (l.text == "-" || strings.HasPrefix(l.text, "- "))) {
			break
		}
		if l.ind > ind {
			return nil, fmt.Errorf("line %d: indented too far", l.raw+1)
		}
		key, rest, ok := cutKey(l.text)
		if !ok {
			return nil, fmt.Errorf("line %d: no key", l.raw+1)
		}
		if _, dup := m[key]; dup {
			return nil, fmt.Errorf("line %d: the key %s twice", l.raw+1, key)
		}
		p.i++
		v, err := p.value(l, rest, ind)
		if err != nil {
			return nil, err
		}
		m[key] = v
	}
	return m, nil
}

func (p *yamlParser) seq(ind int) (any, error) {
	var s []any
	for p.i < len(p.lines) {
		l := p.lines[p.i]
		if l.ind != ind || !(l.text == "-" || strings.HasPrefix(l.text, "- ")) {
			if l.ind > ind {
				return nil, fmt.Errorf("line %d: indented too far", l.raw+1)
			}
			break
		}
		item := strings.TrimPrefix(strings.TrimPrefix(l.text, "-"), " ")
		item = strings.TrimLeft(item, " ")
		if item == "" {
			p.i++
			if p.i >= len(p.lines) || p.lines[p.i].ind <= ind {
				s = append(s, "")
				continue
			}
			v, err := p.block(p.lines[p.i].ind)
			if err != nil {
				return nil, err
			}
			s = append(s, v)
			continue
		}
		if _, _, ok := cutKey(item); ok && !strings.HasPrefix(item, "'") && !strings.HasPrefix(item, `"`) {
			// a mapping that starts on the dash's line
			inner := len(l.text) - len(item) + ind
			p.lines[p.i] = yamlLine{raw: l.raw, ind: inner, text: item}
			v, err := p.mapping(inner)
			if err != nil {
				return nil, err
			}
			s = append(s, v)
			continue
		}
		p.i++
		v, err := scalar(item)
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", l.raw+1, err)
		}
		s = append(s, v)
	}
	return s, nil
}

// value reads what follows "key:" on line l.
func (p *yamlParser) value(l yamlLine, rest string, ind int) (any, error) {
	rest = stripComment(rest)
	switch {
	case rest == "|" || rest == "|-" || rest == ">" || rest == ">-":
		var b []string
		end := l.raw + 1
		for ; end < len(p.raw); end++ {
			line := p.raw[end]
			t := strings.TrimLeft(line, " ")
			if t != "" && len(line)-len(t) <= ind {
				break
			}
			b = append(b, line)
		}
		for p.i < len(p.lines) && p.lines[p.i].raw < end {
			p.i++
		}
		// dedent by the first line's indent
		cut := -1
		for _, line := range b {
			if t := strings.TrimLeft(line, " "); t != "" {
				cut = len(line) - len(t)
				break
			}
		}
		for i, line := range b {
			if len(line) >= cut && cut >= 0 {
				b[i] = line[cut:]
			} else {
				b[i] = strings.TrimLeft(line, " ")
			}
		}
		return strings.TrimRight(strings.Join(b, "\n"), "\n") + "\n", nil
	case rest == "":
		if p.i < len(p.lines) {
			n := p.lines[p.i]
			if n.ind > ind || (n.ind == ind && (n.text == "-" || strings.HasPrefix(n.text, "- "))) {
				return p.block(n.ind)
			}
		}
		return "", nil
	}
	v, err := scalar(rest)
	if err != nil {
		return nil, fmt.Errorf("line %d: %v", l.raw+1, err)
	}
	return v, nil
}

// cutKey splits "key: value" or "key:".
func cutKey(s string) (key, rest string, ok bool) {
	if strings.HasPrefix(s, "'") || strings.HasPrefix(s, `"`) {
		return "", "", false
	}
	if i := strings.Index(s, ": "); i > 0 {
		return s[:i], strings.TrimSpace(s[i+2:]), true
	}
	if strings.HasSuffix(s, ":") && !strings.Contains(s, " ") {
		return strings.TrimSuffix(s, ":"), "", true
	}
	if k, _, found := strings.Cut(s, ":"); found && strings.HasSuffix(strings.TrimSpace(stripComment(s)), ":") {
		return k, "", true
	}
	return "", "", false
}

// stripComment drops a " #" comment outside quotes.
func stripComment(s string) string {
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '#' && (i == 0 || s[i-1] == ' '):
			return strings.TrimSpace(s[:i])
		}
	}
	return strings.TrimSpace(s)
}

// scalar reads a plain, quoted or flow value.
func scalar(s string) (any, error) {
	s = stripComment(s)
	switch {
	case strings.HasPrefix(s, "["):
		if !strings.HasSuffix(s, "]") {
			return nil, fmt.Errorf("an open flow sequence %q", s)
		}
		var out []any
		for _, part := range splitFlow(s[1 : len(s)-1]) {
			v, err := scalar(part)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case strings.HasPrefix(s, "{"):
		if !strings.HasSuffix(s, "}") {
			return nil, fmt.Errorf("an open flow mapping %q", s)
		}
		m := map[string]any{}
		for _, part := range splitFlow(s[1 : len(s)-1]) {
			k, v, ok := strings.Cut(part, ":")
			if !ok {
				return nil, fmt.Errorf("a flow mapping without a key %q", part)
			}
			sv, err := scalar(strings.TrimSpace(v))
			if err != nil {
				return nil, err
			}
			m[strings.TrimSpace(k)] = sv
		}
		return m, nil
	case len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'':
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'"), nil
	case len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"':
		return s[1 : len(s)-1], nil
	}
	return s, nil
}

// splitFlow splits a flow collection's inside on commas outside quotes.
func splitFlow(s string) []string {
	var out []string
	quote := byte(0)
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == ',':
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	if last := strings.TrimSpace(s[start:]); last != "" {
		out = append(out, last)
	}
	return out
}
