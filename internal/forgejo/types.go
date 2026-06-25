package forgejo

import "fmt"

type WebhookPayload struct {
	Action     string       `json:"action"`
	Repository Repository   `json:"repository"`
	Issue      *Issue       `json:"issue,omitempty"`
	Pull       *PullRequest `json:"pull_request,omitempty"`
	Comment    *Comment     `json:"comment,omitempty"`
	Review     *Review      `json:"review,omitempty"`
	Sender     *User        `json:"sender,omitempty"`
	Raw        []byte       `json:"-"`
}

type Repository struct {
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	CloneURL      string `json:"clone_url"`
	SSHURL        string `json:"ssh_url"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
	Owner         User   `json:"owner"`
}

type User struct {
	Login    string `json:"login"`
	UserName string `json:"username"`
	Name     string `json:"name"`
}

func (u User) Handle() string {
	if u.Login != "" {
		return u.Login
	}
	if u.UserName != "" {
		return u.UserName
	}
	return u.Name
}

type Label struct {
	Name string `json:"name"`
}

type Comment struct {
	ID      int64  `json:"id"`
	Body    string `json:"body"`
	Content string `json:"content"`
}

type Review struct {
	ID      int64    `json:"id"`
	Body    string   `json:"body"`
	Content string   `json:"content"`
	Comment *Comment `json:"comment"`
}

type Issue struct {
	Index       int       `json:"index"`
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	Labels      []Label   `json:"labels"`
	PullRequest *struct{} `json:"pull_request,omitempty"`
}

func (i Issue) NumberValue() int {
	if i.Index != 0 {
		return i.Index
	}
	return i.Number
}

func (i Issue) IsPullRequest() bool {
	return i.PullRequest != nil
}

type PullRequest struct {
	Index   int    `json:"index"`
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Labels  []Label
	Head    PullRequestBranch `json:"head"`
	Base    PullRequestBranch `json:"base"`
}

func (p PullRequest) NumberValue() int {
	if p.Index != 0 {
		return p.Index
	}
	return p.Number
}

type PullRequestBranch struct {
	Ref  string     `json:"ref"`
	Repo Repository `json:"repo"`
}

type CreatePullRequestRequest struct {
	Base  string `json:"base"`
	Head  string `json:"head"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

type Ticket struct {
	Owner         string
	Repo          string
	CloneURL      string
	DefaultBranch string
	Kind          string
	Number        int
	Title         string
	Body          string
	HTMLURL       string
	Labels        []Label
	HeadBranch    string
	BaseBranch    string
	CommentID     int64
	Instruction   string
}

func TicketFromPayload(event string, payload WebhookPayload) (Ticket, bool) {
	repo := payload.Repository
	owner := repo.Owner.Handle()
	if owner == "" || repo.Name == "" {
		return Ticket{}, false
	}

	if payload.Pull != nil {
		pull := payload.Pull
		return Ticket{
			Owner:         owner,
			Repo:          repo.Name,
			CloneURL:      firstNonEmpty(pull.Head.Repo.CloneURL, pull.Head.Repo.SSHURL, repo.CloneURL, repo.SSHURL),
			DefaultBranch: firstNonEmpty(repo.DefaultBranch, pull.Base.Ref, "main"),
			Kind:          "pr",
			Number:        pull.NumberValue(),
			Title:         pull.Title,
			Body:          pull.Body,
			HTMLURL:       pull.HTMLURL,
			Labels:        pull.Labels,
			HeadBranch:    pull.Head.Ref,
			BaseBranch:    firstNonEmpty(pull.Base.Ref, repo.DefaultBranch, "main"),
			CommentID:     commentID(payload),
			Instruction:   commentBody(payload),
		}, pull.NumberValue() != 0
	}

	if payload.Issue == nil {
		return Ticket{}, false
	}

	issue := payload.Issue
	kind := "issue"
	if issue.IsPullRequest() || event == "pull_request" {
		kind = "pr"
	}

	return Ticket{
		Owner:         owner,
		Repo:          repo.Name,
		CloneURL:      firstNonEmpty(repo.CloneURL, repo.SSHURL),
		DefaultBranch: firstNonEmpty(repo.DefaultBranch, "main"),
		Kind:          kind,
		Number:        issue.NumberValue(),
		Title:         issue.Title,
		Body:          issue.Body,
		HTMLURL:       issue.HTMLURL,
		Labels:        issue.Labels,
		BaseBranch:    firstNonEmpty(repo.DefaultBranch, "main"),
		CommentID:     commentID(payload),
		Instruction:   commentBody(payload),
	}, issue.NumberValue() != 0
}

func (t Ticket) Ref() string {
	return fmt.Sprintf("%s#%d", t.Kind, t.Number)
}

func LabelsContain(labels []Label, wanted string) bool {
	for _, label := range labels {
		if label.Name == wanted {
			return true
		}
	}
	return false
}

func commentID(payload WebhookPayload) int64 {
	if payload.Comment == nil {
		return 0
	}
	return payload.Comment.ID
}

func commentBody(payload WebhookPayload) string {
	if payload.Comment != nil {
		return firstNonEmpty(payload.Comment.Body, payload.Comment.Content)
	}
	if payload.Review != nil {
		values := []string{payload.Review.Body, payload.Review.Content}
		if payload.Review.Comment != nil {
			values = append(values, payload.Review.Comment.Body, payload.Review.Comment.Content)
		}
		return firstNonEmpty(values...)
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
