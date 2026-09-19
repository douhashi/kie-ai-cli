package kie

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// The states a task is in, as this tool records them.
//
// The Market endpoint answers with waiting/queuing/generating/success/fail.
// Every answer is normalised to one of these four before it is written down,
// so that the ledger speaks one vocabulary of its own rather than kie.ai's.
const (
	// StatusSubmitted is a task kie.ai has accepted and that nothing has
	// asked about since.
	StatusSubmitted = "submitted"
	// StatusRunning is a task kie.ai says it has not finished with. It
	// covers being queued as well as being worked on: the two are the same
	// to a caller who can only wait.
	StatusRunning = "running"
	// StatusSucceeded is a task that finished. It does not promise a URL:
	// a lyrics task answers with the text itself.
	StatusSucceeded = "succeeded"
	// StatusFailed is a task that will not produce anything.
	StatusFailed = "failed"
)

// Statuses is the whole vocabulary, in the order a task moves through it.
var Statuses = []string{StatusSubmitted, StatusRunning, StatusSucceeded, StatusFailed}

// Unfinished are the states a task can still move out of, which are the tasks
// worth asking kie.ai about again.
var Unfinished = []string{StatusSubmitted, StatusRunning}

// TaskState is what one query said about a task: where it has got to, what it
// has produced, why it will not produce anything, and what it has cost.
type TaskState struct {
	Status     string
	ResultURLs []string
	Error      string
	// CreditsConsumed is what kie.ai says the task has cost, nil when the
	// answer did not say. Nil is not zero: a failure kie.ai did not charge
	// for answers with 0, and the two must not read the same.
	CreditsConsumed *float64
}

// decoder reads the data field of one endpoint's answer.
type decoder func(json.RawMessage) (TaskState, error)

// decoders maps a query endpoint to the reader for the answers it gives.
//
// Every model in the catalog is followed through the Market endpoint (#51).
// Any other path is absent rather than guessed at: kie.ai's older per-model
// endpoints each answered in a shape of their own, and a decoder written from
// documentation alone would report some failures as progress and some
// successes as failures while every test written from the same documentation
// passed.
var decoders = map[string]decoder{
	"/api/v1/jobs/recordInfo": decodeMarket,
}

// QueryTask asks kie.ai what became of one task and normalises the answer.
//
// path and param come from the catalog: which endpoint reports on a task, and
// which query parameter carries its id, is a property of the model it was
// submitted to.
//
// An endpoint that is not in decoders is refused before the request is made.
// Asking would cost nothing, but there would be nothing to do with the answer,
// and a caller told "unknown" would have no way to tell it from a task that is
// still running.
func (c *Client) QueryTask(ctx context.Context, path, param, taskID string) (TaskState, error) {
	decode, ok := decoders[path]
	if !ok {
		return TaskState{}, fmt.Errorf("kie.ai: %s: this build cannot read the answers this endpoint gives, so the task is left as it was recorded", path)
	}
	raw, err := c.get(ctx, path+"?"+url.Values{param: {taskID}}.Encode())
	if err != nil {
		return TaskState{}, err
	}
	state, err := decode(raw)
	if err != nil {
		return TaskState{}, fmt.Errorf("kie.ai: %s: %w", path, err)
	}
	return state, nil
}

// marketStates maps the Market endpoint's vocabulary. Queued and waiting are
// running: what a caller can do about either is the same.
var marketStates = map[string]string{
	"waiting":    StatusRunning,
	"queuing":    StatusRunning,
	"generating": StatusRunning,
	"success":    StatusSucceeded,
	"fail":       StatusFailed,
}

// marketAnswer is the part of the Market answer that decides the outcome.
type marketAnswer struct {
	State string `json:"state"`
	// ResultJSON is a JSON document inside a JSON string, which is how this
	// endpoint carries a result whose shape depends on the model.
	ResultJSON string `json:"resultJson"`
	FailCode   scalar `json:"failCode"`
	FailMsg    string `json:"failMsg"`
	// CreditsConsumed is left nil by both null and a missing field.
	CreditsConsumed *float64 `json:"creditsConsumed"`
}

func decodeMarket(raw json.RawMessage) (TaskState, error) {
	var answer marketAnswer
	if err := json.Unmarshal(raw, &answer); err != nil {
		return TaskState{}, fmt.Errorf("the answer is not the one this endpoint gives: %s", snippet(raw))
	}
	status, err := place(marketStates, answer.State, "state")
	if err != nil {
		return TaskState{}, err
	}
	urls, err := marketResultURLs(answer.ResultJSON)
	if err != nil {
		return TaskState{}, err
	}
	return TaskState{
		Status:          status,
		ResultURLs:      urls,
		Error:           reason(string(answer.FailCode), answer.FailMsg),
		CreditsConsumed: answer.CreditsConsumed,
	}, nil
}

// marketResultURLs reads the URLs out of the document the resultJson string
// carries.
//
// A result with no resultUrls is not an error: the OmniHuman models answer
// with a resultObject instead. A resultJson that is not JSON at all is one,
// because the alternative is recording a success that produced nothing while
// the URLs sit unread in an answer nobody looked at again.
func marketResultURLs(resultJSON string) ([]string, error) {
	if resultJSON == "" {
		return []string{}, nil
	}
	var result struct {
		ResultURLs []string `json:"resultUrls"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		return nil, fmt.Errorf("the result is not the JSON document it is declared to be: %s", snippet([]byte(resultJSON)))
	}
	if result.ResultURLs == nil {
		return []string{}, nil
	}
	return result.ResultURLs, nil
}

// place translates one endpoint's word for where a task has got to.
//
// A word that is not in the table is an error rather than a default. Any
// default would be a guess written into the ledger as fact, and the guess that
// looks most harmless -- "still running" -- is the one that hides a finished
// task until whatever it produced has expired.
func place(states map[string]string, word, field string) (string, error) {
	if word == "" {
		return "", fmt.Errorf("the answer carries no %s", field)
	}
	status, ok := states[word]
	if !ok {
		return "", fmt.Errorf("%s %q is not one this build knows; it reads %s", field, word, strings.Join(known(states), ", "))
	}
	return status, nil
}

// known lists an endpoint's vocabulary for a message, grouped by the state
// each word means and in the order the four states are in, so that the list
// reads as a progression rather than as whatever order the map was walked in.
func known(states map[string]string) []string {
	var words []string
	for _, status := range Statuses {
		var group []string
		for word, mapped := range states {
			if mapped == status {
				group = append(group, word)
			}
		}
		slices.Sort(group)
		words = append(words, group...)
	}
	return words
}

// reason renders why a task failed, from a code and a message either of which
// may be absent.
func reason(code, message string) string {
	switch {
	case code == "":
		return message
	case message == "":
		return code
	default:
		return code + ": " + message
	}
}

// scalar is a field kie.ai does not spell consistently: failCode is documented
// as a string, may arrive as an integer, and arrives as null when there is
// nothing to report. It reads all three as the text the field would be
// printed as, so that which spelling arrived cannot decide whether the row can
// be read at all.
type scalar string

func (s *scalar) UnmarshalJSON(raw []byte) error {
	if string(raw) == "null" {
		*s = ""
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		*s = scalar(text)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return fmt.Errorf("%s is neither a string nor a number", snippet(raw))
	}
	*s = scalar(number)
	return nil
}
