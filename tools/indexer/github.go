package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// GitHub reads what the list needs from GitHub's REST API, with
// GITHUB_TOKEN when it is set, and ETags kept in Cache between runs
// (a 304 answer to an authorised request costs nothing of the rate
// limit). It only reads.
type GitHub struct {
	API   string       // "https://api.github.com"
	Token string       // GITHUB_TOKEN; "" none
	HTTP  *http.Client // nil: a client with a timeout
	Cache string       // a folder for ETags and bodies; "" none
	// Hosts are the hosts a release download may start at or be sent on
	// to; tests add their own
	Hosts []string
	// Loopback lets downloads use plain http on 127.0.0.1 (tests only)
	Loopback bool
	// ReleaseBase, when set, serves https://github.com/<repo>/releases/
	// download/... from this base instead (tests only)
	ReleaseBase string
	Requests    int // API requests made, for the run's summary
}

// The hosts of GitHub's release downloads.
var githubHosts = []string{"github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com"}

// ErrGone: GitHub answered 404 or 451: the repo or release is not there
// (deleted, renamed away, private or blocked).
var ErrGone = errors.New("not found on GitHub")

// RepoInfo is what GET /repos/<repo> gives the list.
type RepoInfo struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
	Owner    struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	} `json:"owner"`
	Private  bool  `json:"private"`
	Archived bool  `json:"archived"`
	Disabled bool  `json:"disabled"`
	Stars    int64 `json:"stargazers_count"`
}

// Release is one release of GET /repos/<repo>/releases.
type Release struct {
	ID          int64     `json:"id"`
	TagName     string    `json:"tag_name"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
}

// Asset is one asset of a release.
type Asset struct {
	Name          string `json:"name"`
	Size          int64  `json:"size"`
	State         string `json:"state"`
	Digest        string `json:"digest"` // "sha256:<hex>"; "" for assets older than GitHub's digests
	DownloadCount int64  `json:"download_count"`
	URL           string `json:"browser_download_url"`
}

// Asset is the release's asset of that name; nil none.
func (r *Release) Asset(name string) *Asset {
	for i := range r.Assets {
		if r.Assets[i].Name == name {
			return &r.Assets[i]
		}
	}
	return nil
}

func (g *GitHub) client() *http.Client {
	if g.HTTP != nil {
		return g.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

// cached is a body kept with its ETag.
type cached struct {
	ETag string          `json:"etag"`
	Body json.RawMessage `json:"body"`
}

// get reads one API path into v, with the ETag cache.
func (g *GitHub) get(ctx context.Context, path string, v any) error {
	u := strings.TrimSuffix(g.API, "/") + path
	var c cached
	cacheFile := ""
	if g.Cache != "" {
		sum := sha256.Sum256([]byte(u))
		cacheFile = filepath.Join(g.Cache, hex.EncodeToString(sum[:16])+".json")
		if b, err := os.ReadFile(cacheFile); err == nil && json.Unmarshal(b, &c) != nil {
			c = cached{}
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "mod-index-indexer")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	if c.ETag != "" && len(c.Body) > 0 {
		req.Header.Set("If-None-Match", c.ETag)
	}
	g.Requests++
	resp, err := g.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotModified:
		if len(c.Body) == 0 {
			return fmt.Errorf("GET %s: 304 with nothing cached", path)
		}
		return json.Unmarshal(c.Body, v)
	case http.StatusNotFound, http.StatusUnavailableForLegalReasons:
		return fmt.Errorf("GET %s: %w", path, ErrGone)
	default:
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GET %s: %s: %s", path, resp.Status, strings.TrimSpace(string(msg)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20+1))
	if err != nil {
		return err
	}
	if len(body) > 16<<20 {
		return fmt.Errorf("GET %s: the answer is too big", path)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("GET %s: %v", path, err)
	}
	if et := resp.Header.Get("ETag"); et != "" && cacheFile != "" {
		if b, err := json.Marshal(cached{ETag: et, Body: body}); err == nil {
			_ = writeFile(cacheFile, b)
		}
	}
	return nil
}

// Repo reads a repo.
func (g *GitHub) Repo(ctx context.Context, repo string) (*RepoInfo, error) {
	var r RepoInfo
	if err := g.get(ctx, "/repos/"+repo, &r); err != nil {
		return nil, err
	}
	if r.ID <= 0 || r.Owner.ID <= 0 || !loginRE.MatchString(r.Owner.Login) {
		return nil, fmt.Errorf("GET /repos/%s: an answer without ids", repo)
	}
	return &r, nil
}

// Releases reads a repo's newest 100 releases.
func (g *GitHub) Releases(ctx context.Context, repo string) ([]Release, error) {
	var rs []Release
	if err := g.get(ctx, "/repos/"+repo+"/releases?per_page=100", &rs); err != nil {
		return nil, err
	}
	return rs, nil
}

// Commit is the commit a tag points at (annotated tags peeled).
func (g *GitHub) Commit(ctx context.Context, repo, tag string) (string, error) {
	var c struct {
		SHA string `json:"sha"`
	}
	if err := g.get(ctx, "/repos/"+repo+"/commits/"+url.PathEscape(tag), &c); err != nil {
		return "", err
	}
	if !commitRE.MatchString(c.SHA) {
		return "", fmt.Errorf("the tag %s of %s names no commit", tag, repo)
	}
	return c.SHA, nil
}

// Download reads a release asset: at most max bytes, from a release
// download address, sent on only to GitHub's download hosts, over
// https, with no token. A file of more than max bytes is refused.
func (g *GitHub) Download(ctx context.Context, rawURL string, max int64) ([]byte, error) {
	if g.ReleaseBase != "" && strings.HasPrefix(rawURL, "https://github.com/") {
		rawURL = strings.TrimSuffix(g.ReleaseBase, "/") + strings.TrimPrefix(rawURL, "https://github.com")
	}
	hosts := slices.Concat(githubHosts, g.Hosts)
	ok := func(u *url.URL) bool {
		secure := u.Scheme == "https" || (g.Loopback && u.Scheme == "http" && u.Hostname() == "127.0.0.1")
		return secure && u.User == nil && slices.ContainsFunc(hosts, func(h string) bool { return strings.EqualFold(h, u.Host) })
	}
	u, err := url.Parse(rawURL)
	if err != nil || !ok(u) || !strings.Contains(u.Path, "/releases/download/") {
		return nil, fmt.Errorf("%s is not a release download", rawURL)
	}
	hc := *g.client()
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("too many redirects")
		}
		if !ok(req.URL) {
			return fmt.Errorf("a redirect to %s, off GitHub's download hosts", req.URL.Host)
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "mod-index-indexer")
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("download %s: %w", rawURL, ErrGone)
		}
		return nil, fmt.Errorf("download %s: %s", rawURL, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("download %s: more than %d bytes", rawURL, max)
	}
	return b, nil
}
