package updater

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type sourceFunc func(context.Context) (Pin, string, error)

func (f sourceFunc) Published(ctx context.Context) (Pin, string, error) { return f(ctx) }
func fixturePin(tag string) Pin                                         { return Pin{1, tag, strings.Repeat("a", 64), strings.Repeat("b", 40)} }

func TestVersionOrdering(t *testing.T) {
	for _, v := range []struct {
		a, b string
		want int
	}{{"0.51.9", "v0.51.10", -1}, {"v0.52.0", "0.51.999", 1}, {"0.51.9", "v0.51.9", 0}} {
		n, e := Compare(v.a, v.b)
		if e != nil || n != v.want {
			t.Fatalf("%s vs %s: %d %v", v.a, v.b, n, e)
		}
	}
	for _, v := range []string{"latest", "v0.51.09", "v0.51.10-beta", "0.51.999999999999"} {
		if _, e := Compare(v, "0.51.9"); e == nil {
			t.Fatal("accepted", v)
		}
	}
}

func TestDailyCheckAndManualRetry(t *testing.T) {
	now := time.Unix(1791300000, 0)
	calls := 0
	c := Config{Installed: "0.51.5", StatePath: privateState(t), Now: func() time.Time { return now }}
	c.Source = sourceFunc(func(context.Context) (Pin, string, error) { calls++; return fixturePin("v0.51.9"), "v0.51.9", nil })
	s, e := c.Check(context.Background(), false)
	if e != nil || s.State != "available" || s.Available != "0.51.9" {
		t.Fatalf("%+v %v", s, e)
	}
	for i := 0; i < 3; i++ {
		if _, e = c.Check(context.Background(), false); e != nil {
			t.Fatal(e)
		}
	}
	if calls != 1 {
		t.Fatal("did not cache check", calls)
	}
	now = now.Add(CheckInterval)
	if _, e = c.Check(context.Background(), false); e != nil {
		t.Fatal(e)
	}
	if calls != 2 {
		t.Fatal("daily check missing", calls)
	}
	if _, e = c.Check(context.Background(), true); e != nil {
		t.Fatal(e)
	}
	if calls != 3 {
		t.Fatal("manual check missing", calls)
	}
}

func TestQuietOfflinePreservesNotice(t *testing.T) {
	now := time.Unix(1791300000, 0)
	c := Config{Installed: "0.51.5", StatePath: privateState(t), Now: func() time.Time { return now }}
	c.Source = sourceFunc(func(context.Context) (Pin, string, error) { return fixturePin("v0.51.9"), "v0.51.9", nil })
	if _, e := c.Check(context.Background(), false); e != nil {
		t.Fatal(e)
	}
	c.Source = sourceFunc(func(context.Context) (Pin, string, error) { return Pin{}, "", errors.New("offline") })
	now = now.Add(CheckInterval)
	s, e := c.Check(context.Background(), false)
	if e != nil || s.State != "available" || s.Available != "0.51.9" {
		t.Fatalf("lost notice: %+v %v", s, e)
	}
	s, e = c.Check(context.Background(), true)
	if e != nil || s.State != "failed" || !strings.Contains(s.Message, "connection") {
		t.Fatalf("manual failure: %+v %v", s, e)
	}
}

func TestPublicationGapAndNoDowngrade(t *testing.T) {
	for _, v := range []struct{ installed, latest, state string }{{"0.51.9", "v0.51.10", "publishing"}, {"0.51.5", "v0.51.10", "available"}, {"0.52.0", "v0.51.9", "current"}} {
		c := Config{Installed: v.installed, StatePath: privateState(t), Source: sourceFunc(func(context.Context) (Pin, string, error) { return fixturePin("v0.51.9"), v.latest, nil })}
		s, e := c.Check(context.Background(), true)
		if e != nil || s.State != v.state {
			t.Fatalf("%+v: %+v %v", v, s, e)
		}
	}
}

func TestDevelopmentNeverChecks(t *testing.T) {
	c := Config{Installed: "0.51.9", Development: true, StatePath: privateState(t), Source: sourceFunc(func(context.Context) (Pin, string, error) {
		t.Fatal("development contacted network")
		return Pin{}, "", nil
	})}
	s, e := c.Check(context.Background(), true)
	if e != nil || s.State != "development" {
		t.Fatalf("%+v %v", s, e)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestGitHubStableReleaseValidation(t *testing.T) {
	for _, body := range []string{`{"tag_name":"v0.51.9","draft":true,"prerelease":false}`, `{"tag_name":"v0.51.9","draft":false,"prerelease":true}`, `{"tag_name":"v0.51.9","tag_name":"v0.51.10","draft":false,"prerelease":false}`, `{"tag_name":"v0.51.9-beta","draft":false,"prerelease":false}`} {
		g := GitHubSource{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})}}
		if _, _, e := g.Published(context.Background()); e == nil {
			t.Fatal("accepted unstable/malformed release", body)
		}
	}
}

func privateState(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	if e := os.Chmod(p, 0700); e != nil {
		t.Fatal(e)
	}
	return p
}
