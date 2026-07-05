package forgejo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFindOpenPullRequestFiltersByHeadBranch(t *testing.T) {
	client := NewClient("https://forgejo.example", "")
	client.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v1/repos/ac/demo/pulls" {
			t.Fatalf("path = %q, want pulls endpoint", r.URL.Path)
		}
		if r.URL.Query().Get("state") != "open" {
			t.Fatalf("state query = %q, want open", r.URL.Query().Get("state"))
		}
		return jsonResponse(`[
			{"index":1,"html_url":"https://forgejo/ac/demo/pulls/1","head":{"ref":"forge-ai/ac/demo/issue-1","repo":{"owner":{"login":"ac"}}}},
			{"index":2,"html_url":"https://forgejo/ac/demo/pulls/2","head":{"ref":"forge-ai/ac/demo/issue-2","repo":{"owner":{"login":"ac"}}}}
		]`), nil
	})}

	pull, err := client.FindOpenPullRequest(context.Background(), "ac", "demo", "forge-ai/ac/demo/issue-2")
	if err != nil {
		t.Fatalf("FindOpenPullRequest() error = %v", err)
	}
	if pull == nil || pull.NumberValue() != 2 {
		t.Fatalf("FindOpenPullRequest() = %#v, want PR #2", pull)
	}
}

func TestFindOpenPullRequestReturnsNilWhenNoHeadMatch(t *testing.T) {
	client := NewClient("https://forgejo.example", "")
	client.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(`[
			{"index":1,"head":{"ref":"forge-ai/ac/demo/issue-1","repo":{"owner":{"login":"ac"}}}}
		]`), nil
	})}

	pull, err := client.FindOpenPullRequest(context.Background(), "ac", "demo", "forge-ai/ac/demo/issue-2")
	if err != nil {
		t.Fatalf("FindOpenPullRequest() error = %v", err)
	}
	if pull != nil {
		t.Fatalf("FindOpenPullRequest() = %#v, want nil", pull)
	}
}

func TestUpdatePullRequestSendsBaseBranch(t *testing.T) {
	client := NewClient("https://forgejo.example", "")
	client.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %q, want PATCH", r.Method)
		}
		if r.URL.Path != "/api/v1/repos/ac/demo/pulls/7" {
			t.Fatalf("path = %q, want pull endpoint", r.URL.Path)
		}
		var request UpdatePullRequestRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Base != "release/1.2" {
			t.Fatalf("base = %q, want release/1.2", request.Base)
		}
		return jsonResponse(`{"index":7,"base":{"ref":"release/1.2"}}`), nil
	})}

	pull, err := client.UpdatePullRequest(context.Background(), "ac", "demo", 7, UpdatePullRequestRequest{Base: "release/1.2"})
	if err != nil {
		t.Fatalf("UpdatePullRequest() error = %v", err)
	}
	if pull == nil || pull.Base.Ref != "release/1.2" {
		t.Fatalf("UpdatePullRequest() = %#v, want release/1.2 base", pull)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
