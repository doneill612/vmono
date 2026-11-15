package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/gitx"
)

func TestSummarizeFetch(t *testing.T) {
	owners := map[string][]string{
		"/src/api":  {"api", "api-ref"},
		"/src/web":  {"web"},
		"/src/docs": {"docs"},
	}
	authErr := &gitx.Error{Args: []string{"fetch"}, Stderr: "Permission denied (publickey).", Err: errors.New("exit 128")}
	otherErr := &gitx.Error{Args: []string{"fetch"}, Stderr: "fatal: not a git repository\nsecond line", Err: errors.New("exit 128")}

	n, err := summarizeFetch(owners, []gitx.FetchResult{
		{Repo: "/src/api", Paths: []string{"/src/api"}},
		{Repo: "/src/web", Paths: []string{"/src/web"}},
	})
	if n != 3 || err != nil {
		t.Errorf("all succeeded: n=%d err=%v", n, err)
	}

	n, err = summarizeFetch(owners, []gitx.FetchResult{
		{Repo: "/src/api", Paths: []string{"/src/api"}},
		{Repo: "/src/web", Paths: []string{"/src/web"}, Err: authErr},
		{Repo: "/src/docs", Paths: []string{"/src/docs"}, Err: authErr},
	})
	if n != 2 || err == nil {
		t.Fatalf("partial auth failure: n=%d err=%v", n, err)
	}
	for _, want := range []string{"fetched 2 member(s)", "web, docs", gitx.AuthHint} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}

	_, err = summarizeFetch(owners, []gitx.FetchResult{
		{Repo: "/src/web", Paths: []string{"/src/web"}, Err: authErr},
		{Repo: "/src/docs", Paths: []string{"/src/docs"}, Err: otherErr},
	})
	if err == nil || strings.Contains(err.Error(), gitx.AuthHint) || strings.Contains(err.Error(), "\n") {
		t.Errorf("mixed failures should show git's own one-line reason: %v", err)
	}
}
