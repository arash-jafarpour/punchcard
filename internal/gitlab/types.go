package gitlab

import "time"

// User is GET /user.
type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Email    string `json:"email"`
}

// UserRef is the embedded author object GitLab returns on notes and MRs.
type UserRef struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

// Event is one entry of GET /events.
type Event struct {
	ID             int       `json:"id"`
	ProjectID      int       `json:"project_id"`
	ActionName     string    `json:"action_name"`
	TargetType     string    `json:"target_type"`
	TargetIID      int       `json:"target_iid"`
	TargetTitle    string    `json:"target_title"`
	CreatedAt      time.Time `json:"created_at"`
	AuthorUsername string    `json:"author_username"`
	PushData       *PushData `json:"push_data"`
	Note           *Note     `json:"note"`
}

// PushData is the payload attached to push events.
type PushData struct {
	CommitCount int    `json:"commit_count"`
	Action      string `json:"action"`
	RefType     string `json:"ref_type"`
	CommitFrom  string `json:"commit_from"`
	CommitTo    string `json:"commit_to"`
	Ref         string `json:"ref"`
	CommitTitle string `json:"commit_title"`
	RefCount    int    `json:"ref_count"`
}

// Note is both an event note and an MR note.
type Note struct {
	ID           int       `json:"id"`
	Body         string    `json:"body"`
	CreatedAt    time.Time `json:"created_at"`
	System       bool      `json:"system"`
	NoteableType string    `json:"noteable_type"`
	NoteableIID  int       `json:"noteable_iid"`
	Author       UserRef   `json:"author"`
}

// Commit is a repository commit. Stats are only present on the single-commit
// endpoint.
type Commit struct {
	ID           string       `json:"id"`
	ShortID      string       `json:"short_id"`
	Title        string       `json:"title"`
	Message      string       `json:"message"`
	AuthorName   string       `json:"author_name"`
	AuthorEmail  string       `json:"author_email"`
	CreatedAt    time.Time    `json:"created_at"`
	AuthoredDate time.Time    `json:"authored_date"`
	WebURL       string       `json:"web_url"`
	Stats        *CommitStats `json:"stats"`
}

// CommitStats is the per-commit line delta.
type CommitStats struct {
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
	Total     int `json:"total"`
}

// CompareResult is the subset of GET /repository/compare we use.
type CompareResult struct {
	Commits []Commit `json:"commits"`
}

// MergeRequest is one entry of GET /merge_requests.
type MergeRequest struct {
	ID           int        `json:"id"`
	IID          int        `json:"iid"`
	ProjectID    int        `json:"project_id"`
	Title        string     `json:"title"`
	State        string     `json:"state"`
	WebURL       string     `json:"web_url"`
	SourceBranch string     `json:"source_branch"`
	TargetBranch string     `json:"target_branch"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	MergedAt     *time.Time `json:"merged_at"`
	ClosedAt     *time.Time `json:"closed_at"`
	Author       UserRef    `json:"author"`
	References   struct {
		Short    string `json:"short"`
		Full     string `json:"full"`
		Relative string `json:"relative"`
	} `json:"references"`
}

// Project is GET /projects/:id.
type Project struct {
	ID                int    `json:"id"`
	Name              string `json:"name"`
	PathWithNamespace string `json:"path_with_namespace"`
	WebURL            string `json:"web_url"`
}
