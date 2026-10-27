package gitlab

import (
	"context"
	"fmt"
)

// Project returns metadata for one project, caching successful lookups.
func (c *Client) Project(ctx context.Context, id int) (*Project, error) {
	c.mu.Lock()
	cached, ok := c.projects[id]
	c.mu.Unlock()
	if ok {
		return &cached, nil
	}

	var project Project
	if _, err := c.get(ctx, fmt.Sprintf("/projects/%d", id), nil, &project); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.projects[id] = project
	c.mu.Unlock()
	return &project, nil
}
