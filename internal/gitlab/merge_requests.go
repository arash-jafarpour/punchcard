package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// MergeRequestQuery describes one /merge_requests call. Empty fields are not
// sent.
type MergeRequestQuery struct {
	AuthorUsername   string
	ReviewerUsername string
	ReviewerID       int
	UpdatedAfter     string
	UpdatedBefore    string
	State            string
}

// MergeRequests lists merge requests across projects visible to the token.
func (c *Client) MergeRequests(ctx context.Context, query MergeRequestQuery) ([]MergeRequest, error) {
	q := url.Values{}
	q.Set("scope", "all")
	state := query.State
	if state == "" {
		state = "all"
	}
	q.Set("state", state)
	if query.AuthorUsername != "" {
		q.Set("author_username", query.AuthorUsername)
	}
	if query.ReviewerUsername != "" {
		q.Set("reviewer_username", query.ReviewerUsername)
	}
	if query.ReviewerID > 0 {
		q.Set("reviewer_id", strconv.Itoa(query.ReviewerID))
	}
	if query.UpdatedAfter != "" {
		q.Set("updated_after", query.UpdatedAfter)
	}
	if query.UpdatedBefore != "" {
		q.Set("updated_before", query.UpdatedBefore)
	}
	q.Set("order_by", "updated_at")
	q.Set("sort", "desc")
	return paginate[MergeRequest](ctx, c, "/merge_requests", q)
}

// ReviewerMergeRequests lists MRs where username is a reviewer, falling back
// to reviewer_id when the instance rejects reviewer_username with a 400.
func (c *Client) ReviewerMergeRequests(ctx context.Context, username string, userID int, after, before string) ([]MergeRequest, error) {
	mrs, err := c.MergeRequests(ctx, MergeRequestQuery{
		ReviewerUsername: username,
		UpdatedAfter:     after,
		UpdatedBefore:    before,
	})
	if err == nil {
		return mrs, nil
	}
	if IsStatus(err, http.StatusBadRequest) && userID > 0 {
		return c.MergeRequests(ctx, MergeRequestQuery{
			ReviewerID:    userID,
			UpdatedAfter:  after,
			UpdatedBefore: before,
		})
	}
	return nil, err
}

// MergeRequestNotes returns all notes on an MR, newest first.
func (c *Client) MergeRequestNotes(ctx context.Context, projectID, iid int) ([]Note, error) {
	q := url.Values{}
	q.Set("sort", "desc")
	q.Set("order_by", "created_at")
	path := fmt.Sprintf("/projects/%d/merge_requests/%d/notes", projectID, iid)
	return paginate[Note](ctx, c, path, q)
}
