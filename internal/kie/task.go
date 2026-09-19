package kie

import (
	"context"
	"encoding/json"
	"fmt"
)

// CreateTask submits one task to path and returns the id kie.ai gave it.
//
// The body is built by the caller, which is the only place that knows the
// model it names.
//
// An answer this cannot read an id out of is a failure. The task has been
// created and charged for by then, but an empty id travelling on would be
// recorded as a task nothing can ever be asked about; the message quotes what
// came back so that the id can be recovered by hand if it is in there.
func (c *Client) CreateTask(ctx context.Context, path string, body any) (string, error) {
	raw, err := c.post(ctx, path, body)
	if err != nil {
		return "", err
	}
	var created struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.TaskID == "" {
		return "", fmt.Errorf("kie.ai: %s: the answer carries no task id: %s", path, snippet(raw))
	}
	return created.TaskID, nil
}
