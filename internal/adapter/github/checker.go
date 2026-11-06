// Package github asks the GitHub releases API for the newest build.
//
// It implements port.UpdateChecker. The request needs the network, so it is made
// with a short deadline and a single attempt: an update check is a convenience,
// and a user who is offline should not wait for a retry they did not ask for.
//
// A failure is always returned as an error rather than a panic or a silent zero,
// so the caller can show "the check did not finish" with a reason. Nothing here
// modifies a file, downloads an asset, or runs anything.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aaronmurniadi/ofxhost/internal/domain/project"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// DefaultTimeout is how long one check may take.
//
// Five seconds is long enough for a slow connection to answer and short enough
// that a user who pressed the button sees an answer before they assume it is
// broken.
const DefaultTimeout = 5 * time.Second

// maxResponseBytes bounds what is read from the API, so a hostile or broken
// endpoint cannot exhaust memory.
const maxResponseBytes = 1 << 20

// Config names the repository the releases come from.
type Config struct {
	Owner string
	Repo  string
	// APIBase overrides the API root, for a test. The default is the public one.
	APIBase string
	// Timeout overrides DefaultTimeout when positive.
	Timeout time.Duration
	// Client overrides the HTTP client, for a test. Nil means a client with the
	// timeout above.
	Client *http.Client
}

// Checker implements port.UpdateChecker against the GitHub releases API.
type Checker struct {
	owner   string
	repo    string
	apiBase string
	client  *http.Client
	timeout time.Duration
}

// New returns a checker for one repository.
func New(cfg Config) (*Checker, error) {
	if cfg.Owner == "" || cfg.Repo == "" {
		return nil, errors.New("github: an owner and a repository are required")
	}
	base := cfg.APIBase
	if base == "" {
		base = "https://api.github.com"
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	return &Checker{
		owner:   cfg.Owner,
		repo:    cfg.Repo,
		apiBase: strings.TrimRight(base, "/"),
		client:  client,
		timeout: timeout,
	}, nil
}

// releaseJSON is the part of a release the checker reads.
//
// The API returns far more. Only these fields are needed, and an unknown field is
// ignored, so a change to the rest of the payload cannot break the check.
type releaseJSON struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Published  string `json:"published_at"`
	Created    string `json:"created_at"`
	Assets     []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// Check asks for the newest release on a channel.
//
// A stable channel ignores drafts and pre-releases. A pre-release channel accepts
// both published kinds and still ignores drafts, because a draft has no page a
// user could open.
func (c *Checker) Check(channel project.UpdateChannel, currentVersion string) (port.Release, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=30", c.apiBase, c.owner, c.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return port.Release{}, fmt.Errorf("github: build the request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "OfxHost-UpdateCheck")

	resp, err := c.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return port.Release{}, fmt.Errorf("github: the check took longer than %s", c.timeout)
		}
		return port.Release{}, fmt.Errorf("github: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return port.Release{}, fmt.Errorf("github: %s/%s has no release list", c.owner, c.repo)
	case http.StatusForbidden, http.StatusTooManyRequests:
		return port.Release{}, errors.New("github: the release list is rate limited; try again later")
	default:
		return port.Release{}, fmt.Errorf("github: the release list answered %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return port.Release{}, fmt.Errorf("github: read the release list: %w", err)
	}
	return PickRelease(body, channel, currentVersion)
}

// PickRelease selects the release to offer from a release-list body.
//
// It is exported beside Check so the selection can be tested without a network.
// The body holds every release and the answer is the highest version that the
// channel allows, not simply the first entry: the API sorts by creation time, and
// an older version can be published after a newer one.
func PickRelease(body []byte, channel project.UpdateChannel, currentVersion string) (port.Release, error) {
	var list []releaseJSON
	if err := json.Unmarshal(body, &list); err != nil {
		return port.Release{}, fmt.Errorf("github: parse the release list: %w", err)
	}
	if len(list) == 0 {
		return port.Release{}, errors.New("github: the release list is empty")
	}

	prereleases := channel == project.ChannelPrerelease

	var best *releaseJSON
	for i := range list {
		entry := &list[i]
		if entry.TagName == "" || entry.Draft {
			continue
		}
		if entry.Prerelease && !prereleases {
			continue
		}
		if best == nil {
			best = entry
			continue
		}
		if project.CompareVersions(entry.TagName, best.TagName) > 0 {
			best = entry
		}
	}
	if best == nil {
		return port.Release{}, fmt.Errorf("github: no release matches the %s channel", channel)
	}

	return port.Release{
		Tag:         best.TagName,
		PublishedAt: firstNonEmpty(best.Published, best.Created),
		PageURL:     best.HTMLURL,
		AssetURL:    pickAsset(best.Assets),
	}, nil
}

// pickAsset returns the download URL of the first asset.
//
// Release assets are named per platform by the release workflow, so the first one
// is not always the right one. The field exists for a caller that wants to offer a
// download link; nothing in the program downloads an asset automatically, and this
// function does not try to guess a platform.
func pickAsset(assets []struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}) string {
	for _, asset := range assets {
		if asset.BrowserDownloadURL != "" {
			return asset.BrowserDownloadURL
		}
	}
	return ""
}

// firstNonEmpty returns the first argument that is not empty.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
