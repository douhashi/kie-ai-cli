package kie

import (
	"context"
	"encoding/json"
	"fmt"
)

// creditPath reads the balance of the account the key belongs to. It is the
// only endpoint that creates nothing, which makes it the one safe call to make
// merely to find out whether a key works.
const creditPath = "/api/v1/chat/credit"

// Credits reports the balance.
//
// The number is returned as the API wrote it. A balance is money, and reading
// it into a float would round it before anyone has decided how it should be
// displayed.
func (c *Client) Credits(ctx context.Context) (json.Number, error) {
	raw, err := c.get(ctx, creditPath)
	if err != nil {
		return "", err
	}
	var balance json.Number
	if err := json.Unmarshal(raw, &balance); err != nil || balance == "" {
		return "", fmt.Errorf("kie.ai: %s: the balance is not a number: %s", creditPath, snippet(raw))
	}
	return balance, nil
}
