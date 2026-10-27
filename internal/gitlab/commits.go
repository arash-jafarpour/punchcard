package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// Commit returns one commit, including stats.
func (c *Client) Commit(ctx context.Context, projectID int, sha string) (*Commit, error) {
	if sha == "" {
		return nil, errors.New("gitlab: commit sha is empty")
	}
	var commit Commit
	path := fmt.Sprintf("/projects/%d/repository/commits/%s", projectID, url.PathEscape(sha))
	if _, err := c.get(ctx, path, nil, &commit); err != nil {
		return nil, err
	}
	return &commit, nil
}

// Compare returns the commits between from and to. The entries do not carry
// stats; callers fetch the single-commit endpoint for those.
func (c *Client) Compare(ctx context.Context, projectID int, from, to string) ([]Commit, error) {
	if from == "" || to == "" {
		return nil, errors.New("gitlab: compare needs both from and to")
	}
	q := url.Values{}
	q.Set("from", from)
	q.Set("to", to)
	var result CompareResult
	if _, err := c.get(ctx, fmt.Sprintf("/projects/%d/repository/compare", projectID), q, &result); err != nil {
		return nil, err
	}
	return result.Commits, nil
}

// Commits lists commits on a ref inside a time window. It is the fallback for
// new branches, tags and failed comparisons.
func (c *Client) Commits(ctx context.Context, projectID int, refName string, since, until time.Time) ([]Commit, error) {
	q := url.Values{}
	if refName != "" {
		q.Set("ref_name", refName)
	}
	if !since.IsZero() {
		q.Set("since", since.Format(time.RFC3339))
	}
	if !until.IsZero() {
		q.Set("until", until.Format(time.RFC3339))
	}
	return paginate[Commit](ctx, c, fmt.Sprintf("/projects/%d/repository/commits", projectID), q)
}
