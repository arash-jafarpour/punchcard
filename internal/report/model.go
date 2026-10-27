package report

import "time"

// Report is the normalized, render-ready view of one day.
type Report struct {
	Date     string
	Timezone string
	Location *time.Location
	User     UserInfo
	Projects []Project
	Summary  Summary
	Warnings []string
}

// UserInfo is the display identity used in the report header.
type UserInfo struct {
	Name     string
	Username string
}

// Project groups every activity item that belongs to one GitLab project.
type Project struct {
	ID                int
	PathWithNamespace string
	Name              string
	WebURL            string
	// Available is false when the project metadata could not be fetched. The
	// section is still rendered, without links.
	Available        bool
	Pushes           int
	UnresolvedPushes int
	Branches         []string
	Tags             []string
	Commits          []Commit
	MergeRequests    []MergeRequest
	Reviews          []Review
	Issues           []Issue
}

// Commit is one deduplicated commit with the refs it appeared on.
type Commit struct {
	ShortID      string
	SHA          string
	Title        string
	WebURL       string
	PushedAt     time.Time
	Additions    int
	Deletions    int
	HasStats     bool
	Refs         []string
	AuthorName   string
	AuthorIsUser bool
}

// Action is the headline action an MR had on the report day.
type Action string

const (
	ActionOpened  Action = "opened"
	ActionMerged  Action = "merged"
	ActionClosed  Action = "closed"
	ActionUpdated Action = "updated"
)

// MergeRequest is an MR the user authored on the report day.
type MergeRequest struct {
	IID          int
	Title        string
	WebURL       string
	Action       Action
	CommentCount int
}

// Review is an MR the user reviewed but did not author.
type Review struct {
	IID          int
	Title        string
	WebURL       string
	CommentCount int
	Excerpts     []string
}

// Issue covers both issue events and the user's issue comments.
type Issue struct {
	IID    int
	Title  string
	WebURL string
	// Action is opened, closed, reopened, updated, or commented.
	Action string
	Note   string
	At     time.Time
}

// Summary is the deterministic count block at the top of the report.
type Summary struct {
	Projects  int
	Pushes    int
	Commits   int
	Additions int
	Deletions int

	MRsOpened  int
	MRsMerged  int
	MRsClosed  int
	MRsUpdated int

	Issues  int
	Reviews int
}

// HasActivity reports whether the day produced anything to report.
func (s Summary) HasActivity() bool {
	return s.Projects > 0
}

// Lines is the total number of changed lines across commits that carry stats.
func (s Summary) Lines() int { return s.Additions + s.Deletions }
