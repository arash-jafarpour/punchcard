package gitlab

import (
	"context"
	"errors"
	"net/url"
)

// Events returns the authenticated user's event feed for the exclusive date
// window [after, before). GitLab treats both bounds as date-only values, so
// callers pass one day of slack around the report day.
func (c *Client) Events(ctx context.Context, after, before string) ([]Event, error) {
	if after == "" || before == "" {
		return nil, errors.New("gitlab: events need both after and before")
	}
	q := url.Values{}
	q.Set("scope", "all")
	q.Set("after", after)
	q.Set("before", before)
	return paginate[Event](ctx, c, "/events", q)
}

// CurrentUser returns GET /user, cached for the process lifetime.
func (c *Client) CurrentUser(ctx context.Context) (*User, error) {
	c.mu.Lock()
	cached := c.user
	c.mu.Unlock()
	if cached != nil {
		copy := *cached
		return &copy, nil
	}

	var user User
	if _, err := c.get(ctx, "/user", nil, &user); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.user = &user
	c.mu.Unlock()
	copy := user
	return &copy, nil
}
