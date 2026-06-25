package forgejo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func GenerateAccessToken(ctx context.Context, baseURL, username, password, tokenName string) (string, error) {
	request := struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}{
		Name:   fmt.Sprintf("%s-%d", tokenName, time.Now().Unix()),
		Scopes: []string{"all"},
	}

	encoded, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	endpoint := strings.TrimRight(baseURL, "/") + "/api/v1/users/" + url.PathEscape(username) + "/tokens"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(username, password)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("POST %s: %s: %s", endpoint, resp.Status, strings.TrimSpace(string(responseBody)))
	}

	var response struct {
		SHA1  string `json:"sha1"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return "", err
	}
	if response.Token != "" {
		return response.Token, nil
	}
	if response.SHA1 != "" {
		return response.SHA1, nil
	}
	return "", fmt.Errorf("POST %s: response did not contain token", endpoint)
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) CreateIssueComment(ctx context.Context, owner, repo string, index int, body string) error {
	request := struct {
		Body string `json:"body"`
	}{Body: body}

	_, err := c.do(ctx, http.MethodPost, apiPath("repos", owner, repo, "issues", fmt.Sprint(index), "comments"), nil, request)
	return err
}

func (c *Client) CreateCommentReaction(ctx context.Context, owner, repo string, commentID int64, content string) error {
	request := struct {
		Content string `json:"content"`
	}{Content: content}

	_, err := c.do(ctx, http.MethodPost, apiPath("repos", owner, repo, "issues", "comments", fmt.Sprint(commentID), "reactions"), nil, request)
	return err
}

func (c *Client) GetPullReviewComments(ctx context.Context, owner, repo string, prIndex int, reviewID int64) ([]string, error) {
	body, err := c.do(ctx, http.MethodGet, apiPath("repos", owner, repo, "pulls", fmt.Sprint(prIndex), "reviews", fmt.Sprint(reviewID), "comments"), nil, nil)
	if err != nil {
		return nil, err
	}

	var comments []struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal(body, &comments); err != nil {
		return nil, err
	}

	bodies := make([]string, 0, len(comments))
	for _, c := range comments {
		if strings.TrimSpace(c.Body) != "" {
			bodies = append(bodies, c.Body)
		}
	}
	return bodies, nil
}

func (c *Client) FindOpenPullRequest(ctx context.Context, owner, repo, head string) (*PullRequest, error) {
	values := url.Values{}
	values.Set("state", "open")
	values.Set("head", owner+":"+head)

	body, err := c.do(ctx, http.MethodGet, apiPath("repos", owner, repo, "pulls"), values, nil)
	if err != nil {
		return nil, err
	}

	var pulls []PullRequest
	if err := json.Unmarshal(body, &pulls); err != nil {
		return nil, err
	}
	if len(pulls) == 0 {
		return nil, nil
	}
	return &pulls[0], nil
}

func (c *Client) CreatePullRequest(ctx context.Context, owner, repo string, request CreatePullRequestRequest) (*PullRequest, error) {
	body, err := c.do(ctx, http.MethodPost, apiPath("repos", owner, repo, "pulls"), nil, request)
	if err != nil {
		return nil, err
	}

	var pull PullRequest
	if err := json.Unmarshal(body, &pull); err != nil {
		return nil, err
	}
	return &pull, nil
}

func (c *Client) do(ctx context.Context, method, apiPath string, query url.Values, requestBody any) ([]byte, error) {
	var body io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}

	endpoint := c.baseURL + "/api/v1/" + strings.TrimPrefix(apiPath, "/")
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s: %s: %s", method, endpoint, resp.Status, strings.TrimSpace(string(responseBody)))
	}
	return responseBody, nil
}

func apiPath(parts ...string) string {
	escaped := make([]string, 0, len(parts))
	for _, part := range parts {
		escaped = append(escaped, url.PathEscape(part))
	}
	return path.Join(escaped...)
}
