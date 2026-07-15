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

func TestFindOpenPullRequestMatchesHeadWhenOwnerMissing(t *testing.T) {
	client := NewClient("https://forgejo.example", "")
	client.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(`[
			{"index":3,"head":{"ref":"forge-ai/ac/demo/issue-3","repo":{}}}
		]`), nil
	})}

	pull, err := client.FindOpenPullRequest(context.Background(), "ac", "demo", "forge-ai/ac/demo/issue-3")
	if err != nil {
		t.Fatalf("FindOpenPullRequest() error = %v", err)
	}
	if pull == nil || pull.NumberValue() != 3 {
		t.Fatalf("FindOpenPullRequest() = %#v, want PR #3", pull)
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

func TestGetPullReviewCommentsUsesReviewID(t *testing.T) {
	client := NewClient("https://forgejo.example", "")
	client.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v1/repos/ac/demo/pulls/7/reviews/123/comments" {
			t.Fatalf("path = %q, want review comments endpoint", r.URL.Path)
		}
		return jsonResponse(`[
			{"id":1,"body":"@codex fix this"},
			{"id":2,"body":"   "}
		]`), nil
	})}

	comments, err := client.GetPullReviewComments(context.Background(), "ac", "demo", 7, 123)
	if err != nil {
		t.Fatalf("GetPullReviewComments() error = %v", err)
	}
	if len(comments) != 1 || comments[0].Body != "@codex fix this" {
		t.Fatalf("comments = %#v, want non-empty review comment", comments)
	}
}

func TestGetPullReviewCommentsFallsBackToLatestReviewWhenIDMissing(t *testing.T) {
	client := NewClient("https://forgejo.example", "")
	paths := []string{}
	client.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/repos/ac/demo/pulls/7/reviews":
			return jsonResponse(`[
				{"id":122},
				{"id":123}
			]`), nil
		case "/api/v1/repos/ac/demo/pulls/7/reviews/123/comments":
			return jsonResponse(`[{"id":1,"body":"@codex latest"}]`), nil
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
			return nil, nil
		}
	})}

	comments, err := client.GetPullReviewComments(context.Background(), "ac", "demo", 7, 0)
	if err != nil {
		t.Fatalf("GetPullReviewComments() error = %v", err)
	}
	if len(comments) != 1 || comments[0].Body != "@codex latest" {
		t.Fatalf("comments = %#v, want latest review comment", comments)
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %#v, want reviews lookup then comments lookup", paths)
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
