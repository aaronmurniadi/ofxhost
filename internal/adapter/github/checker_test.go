package github

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aaronmurniadi/ofxhost/internal/domain/project"
)

const listAll = `[
  {"tag_name":"v0.4.2","html_url":"https://example/0.4.2","published_at":"2026-01-02T00:00:00Z","draft":false,"prerelease":false,
   "assets":[{"name":"OfxHost-macOS-arm64.dmg","browser_download_url":"https://example/arm64.dmg"}]},
  {"tag_name":"v0.4.3","html_url":"https://example/0.4.3","published_at":"2026-01-03T00:00:00Z","draft":false,"prerelease":false},
  {"tag_name":"v0.5.0-dev-abc1234","html_url":"https://example/0.5.0-dev","published_at":"2026-01-04T00:00:00Z","draft":false,"prerelease":true},
  {"tag_name":"v9.9.9","html_url":"https://example/draft","published_at":"2026-01-05T00:00:00Z","draft":true,"prerelease":false}
]`

func TestPickReleaseStableIgnoresPrereleasesAndDrafts(t *testing.T) {
	got, err := PickRelease([]byte(listAll), project.ChannelStable, "0.4.1")
	if err != nil {
		t.Fatalf("PickRelease: %v", err)
	}
	if got.Tag != "v0.4.3" {
		t.Fatalf("tag = %q, want v0.4.3", got.Tag)
	}
	if got.PageURL != "https://example/0.4.3" {
		t.Fatalf("page = %q", got.PageURL)
	}
	if got.PublishedAt != "2026-01-03T00:00:00Z" {
		t.Fatalf("published = %q", got.PublishedAt)
	}
}

func TestPickReleasePrereleaseAcceptsBothKinds(t *testing.T) {
	got, err := PickRelease([]byte(listAll), project.ChannelPrerelease, "0.4.1")
	if err != nil {
		t.Fatalf("PickRelease: %v", err)
	}
	if got.Tag != "v0.5.0-dev-abc1234" {
		t.Fatalf("tag = %q, want the pre-release", got.Tag)
	}
}

func TestPickReleaseIgnoresDraftsEvenOnPrerelease(t *testing.T) {
	// A draft has no page a user could open, so offering it would produce a broken
	// link.
	got, err := PickRelease([]byte(listAll), project.ChannelPrerelease, "0.4.1")
	if err != nil {
		t.Fatalf("PickRelease: %v", err)
	}
	if got.Tag == "v9.9.9" {
		t.Fatal("a draft was offered")
	}
}

func TestPickReleaseChoosesTheHighestVersionNotTheFirst(t *testing.T) {
	// The API sorts by creation time, and an older version can be published after
	// a newer one.
	body := `[
	  {"tag_name":"v0.3.0","html_url":"https://example/old","published_at":"2026-02-01T00:00:00Z"},
	  {"tag_name":"v0.4.5","html_url":"https://example/new","published_at":"2026-01-01T00:00:00Z"}
	]`
	got, err := PickRelease([]byte(body), project.ChannelStable, "0.1.0")
	if err != nil {
		t.Fatalf("PickRelease: %v", err)
	}
	if got.Tag != "v0.4.5" {
		t.Fatalf("tag = %q, want v0.4.5", got.Tag)
	}
}

func TestPickReleaseRecordsAnAsset(t *testing.T) {
	got, err := PickRelease([]byte(listAll), project.ChannelStable, "0.4.1")
	if err != nil {
		t.Fatalf("PickRelease: %v", err)
	}
	if got.AssetURL != "" {
		// The chosen release has no assets; the field must stay empty rather than
		// borrowing another release's asset.
		t.Fatalf("asset = %q, want it empty", got.AssetURL)
	}

	body := `[{"tag_name":"v1.0.0","html_url":"https://example/1","assets":[
	  {"name":"a.dmg","browser_download_url":"https://example/a.dmg"}]}]`
	got, err = PickRelease([]byte(body), project.ChannelStable, "0.1.0")
	if err != nil {
		t.Fatalf("PickRelease: %v", err)
	}
	if got.AssetURL != "https://example/a.dmg" {
		t.Fatalf("asset = %q", got.AssetURL)
	}
}

func TestPickReleaseErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "empty list", body: `[]`},
		{name: "not json", body: `{"message":"Not Found"}`},
		{name: "no release on the channel", body: `[{"tag_name":"v1.0.0","prerelease":true}]`},
		{name: "only drafts", body: `[{"tag_name":"v1.0.0","draft":true}]`},
		{name: "no tag", body: `[{"html_url":"https://example"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := PickRelease([]byte(tc.body), project.ChannelStable, "0.1.0"); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestCheckAgainstAServer(t *testing.T) {
	var gotPath, gotAccept, gotUserAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAccept = r.Header.Get("Accept")
		gotUserAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listAll))
	}))
	defer server.Close()

	checker, err := New(Config{Owner: "aaronmurniadi", Repo: "ofxhost", APIBase: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got, err := checker.Check(project.ChannelStable, "0.1.0")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Tag != "v0.4.3" {
		t.Fatalf("tag = %q", got.Tag)
	}
	if gotPath != "/repos/aaronmurniadi/ofxhost/releases" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotAccept, "github") {
		t.Fatalf("accept = %q", gotAccept)
	}
	// GitHub rejects a request with no User-Agent, so it must always be set.
	if gotUserAgent == "" {
		t.Fatal("the request carried no User-Agent")
	}
}

func TestCheckReportsAStatusError(t *testing.T) {
	cases := map[int]string{
		http.StatusNotFound:            "no release list",
		http.StatusForbidden:           "rate limited",
		http.StatusTooManyRequests:     "rate limited",
		http.StatusInternalServerError: "answered 500",
	}
	for code, want := range cases {
		t.Run(http.StatusText(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
			}))
			defer server.Close()

			checker, err := New(Config{Owner: "o", Repo: "r", APIBase: server.URL})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = checker.Check(project.ChannelStable, "0.1.0")
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %q, want it to contain %q", err, want)
			}
		})
	}
}

func TestCheckTimesOut(t *testing.T) {
	// A user who is offline must see an answer, not a window that hangs.
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer server.Close()
	defer close(release)

	checker, err := New(Config{
		Owner:   "o",
		Repo:    "r",
		APIBase: server.URL,
		Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	start := time.Now()
	_, err = checker.Check(project.ChannelStable, "0.1.0")
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("the check took %s, which is past its deadline", elapsed)
	}
}

func TestNewRequiresAOwnerAndRepo(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("an empty config must be an error")
	}
	if _, err := New(Config{Owner: "o"}); err == nil {
		t.Fatal("a missing repository must be an error")
	}
}
