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
// kie.ai has no single vocabulary of its own: the Market endpoint answers with
// waiting/queuing/generating/success/fail, the Suno endpoints with PENDING and
// a family of *_FAILED, and others with a numeric flag. Every answer is
// normalised to one of these four before it is written down, so that a listing
// reads the same whichever endpoint produced the row.
const (
	// StatusSubmitted is a task kie.ai has accepted and that nothing has
	// asked about since.
	StatusSubmitted = "submitted"
	// StatusRunning is a task kie.ai says it has not finished with. It
	// covers being queued as well as being worked on: the two are the same
	// to a caller who can only wait.
	StatusRunning = "running"
	// StatusSucceeded is a task that finished. It does not promise a URL:
	// the lyrics endpoint answers with the text itself.
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
// has produced, and why it will not produce anything.
type TaskState struct {
	Status     string
	ResultURLs []string
	Error      string
}

// decoder reads the data field of one endpoint's answer.
type decoder func(json.RawMessage) (TaskState, error)

// decoders maps a query endpoint to the reader for the answers it gives.
//
// It holds the three endpoints whose answers have been read against the live
// API, which is 145 of the catalog's 161 models. The rest are absent rather
// than guessed at: the documentation describes fourteen different shapes over
// fifteen paths, and the state field alone is spelled state, status or
// successFlag, with successFlag arriving as both an integer and a string --
// and its integer 2 meaning "failed" on the veo3 and flux endpoints while
// meaning "generating" on the Suno cover endpoint. A decoder written from the
// documentation would therefore report some failures as progress and some
// successes as failures, and every test written from the same documentation
// would pass. Issue #36 adds the remaining twelve, each against a real task.
var decoders = map[string]decoder{
	"/api/v1/jobs/recordInfo":      decodeMarket,
	"/api/v1/generate/record-info": decodeSuno,
	"/api/v1/lyrics/record-info":   decodeLyrics,
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
	return TaskState{Status: status, ResultURLs: urls, Error: reason(string(answer.FailCode), answer.FailMsg)}, nil
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

// sunoStates maps the vocabulary the Suno endpoints share.
//
// One map serves both because both are the same service reporting on the same
// kinds of failure; only the verb in the middle of GENERATE_*_FAILED differs,
// and accepting the other family's spelling of a failure cannot turn one
// outcome into another.
//
// TEXT_SUCCESS and FIRST_SUCCESS are running rather than succeeded: they say
// part of the work is done, and a task recorded as finished on the strength of
// them would never be asked about again.
var sunoStates = map[string]string{
	"PENDING":                StatusRunning,
	"TEXT_SUCCESS":           StatusRunning,
	"FIRST_SUCCESS":          StatusRunning,
	"SUCCESS":                StatusSucceeded,
	"CREATE_TASK_FAILED":     StatusFailed,
	"GENERATE_AUDIO_FAILED":  StatusFailed,
	"GENERATE_LYRICS_FAILED": StatusFailed,
	"CALLBACK_EXCEPTION":     StatusFailed,
	"SENSITIVE_WORD_ERROR":   StatusFailed,
}

// sunoAnswer is the part of the Suno music answer that decides the outcome.
type sunoAnswer struct {
	Response struct {
		SunoData []struct {
			AudioURL string `json:"audioUrl"`
		} `json:"sunoData"`
	} `json:"response"`
	Status       string `json:"status"`
	ErrorCode    scalar `json:"errorCode"`
	ErrorMessage string `json:"errorMessage"`
}

func decodeSuno(raw json.RawMessage) (TaskState, error) {
	var answer sunoAnswer
	if err := json.Unmarshal(raw, &answer); err != nil {
		return TaskState{}, fmt.Errorf("the answer is not the one this endpoint gives: %s", snippet(raw))
	}
	status, err := place(sunoStates, answer.Status, "status")
	if err != nil {
		return TaskState{}, err
	}
	urls := []string{}
	for _, track := range answer.Response.SunoData {
		if track.AudioURL != "" {
			urls = append(urls, track.AudioURL)
		}
	}
	return TaskState{Status: status, ResultURLs: urls, Error: reason(string(answer.ErrorCode), answer.ErrorMessage)}, nil
}

// lyricsAnswer is the part of the lyrics answer that decides the outcome. The
// lyrics themselves are text in the answer rather than a file behind a URL, so
// this endpoint has no results to record.
type lyricsAnswer struct {
	Status       string `json:"status"`
	ErrorCode    scalar `json:"errorCode"`
	ErrorMessage string `json:"errorMessage"`
}

func decodeLyrics(raw json.RawMessage) (TaskState, error) {
	var answer lyricsAnswer
	if err := json.Unmarshal(raw, &answer); err != nil {
		return TaskState{}, fmt.Errorf("the answer is not the one this endpoint gives: %s", snippet(raw))
	}
	status, err := place(sunoStates, answer.Status, "status")
	if err != nil {
		return TaskState{}, err
	}
	return TaskState{Status: status, ResultURLs: []string{}, Error: reason(string(answer.ErrorCode), answer.ErrorMessage)}, nil
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
// as a string and errorCode as an integer, and both arrive as null when there
// is nothing to report. It reads all three as the text the field would be
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
