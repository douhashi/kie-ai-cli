package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/douhashi/kie-ai-cli/internal/catalog"
	"github.com/douhashi/kie-ai-cli/internal/kie"
	"github.com/douhashi/kie-ai-cli/internal/ledger"
)

func bindTaskList(_ *env, fs *flag.FlagSet, _ []string) (binding, error) {
	status := fs.String("status", "", "only the tasks in this state: "+strings.Join(kie.Statuses, ", "))
	return binding{run: func(e *env, args []string) error {
		return runTaskList(e, args, *status)
	}}, nil
}

// runTaskList reports what the ledger holds, and asks kie.ai nothing.
//
// kie.ai has no endpoint that lists tasks, so a listing that asked after each
// one would take a round trip per row and get slower the more there is to
// list. What that costs is that a row can be out of date, which the count at
// the end is there to say.
func runTaskList(e *env, args []string, status string) error {
	if len(args) != 0 {
		return usagef("task list: expected no arguments, got %d", len(args))
	}
	statuses, err := statusFilter(status)
	if err != nil {
		return err
	}

	ctx := context.Background()
	l, err := ledger.Open(ctx, e.paths.Ledger)
	if err != nil {
		return err
	}
	defer func() { _ = l.Close() }()

	tasks, err := l.List(ctx, statuses...)
	if err != nil {
		return err
	}
	if err := writeTasks(e, tasks); err != nil {
		return err
	}
	reportUnasked(e, tasks)
	return nil
}

// statusFilter turns the --status flag into the states to list, which is all
// of them when it was not given.
//
// A word that is not one of the four is refused rather than answered with an
// empty listing: an empty listing reads as "you have no such tasks", which is
// the one thing a typo must not be able to say.
func statusFilter(status string) ([]string, error) {
	if status == "" {
		return nil, nil
	}
	if !slices.Contains(kie.Statuses, status) {
		return nil, usagef("task list: unknown status %q; a task is one of: %s", status, strings.Join(kie.Statuses, ", "))
	}
	return []string{status}, nil
}

// reportUnasked says how many of the listed tasks may have moved on since
// anything last asked about them. It goes to stderr so that --json stays a
// document a machine can read, and is silent when there is nothing to say.
func reportUnasked(e *env, tasks []ledger.Task) {
	unasked := 0
	for _, task := range tasks {
		if slices.Contains(kie.Unfinished, task.Status) {
			unasked++
		}
	}
	if unasked == 0 {
		return
	}
	fmt.Fprintf(e.stderr, "%s: %d of these tasks may have moved on since kie.ai was last asked; run `%s task refresh`\n",
		name, unasked, name)
}

// refreshConcurrency is how many tasks are asked about at once.
//
// kie.ai reports on one task per request, so a ledger with a dozen unfinished
// tasks is a dozen round trips and doing them in turn makes the command as
// slow as the network is. Four overlap: enough that the waiting is shared, few
// enough that a large ledger is not a burst of requests at a service whose
// rate limit this project does not know.
const refreshConcurrency = 4

// runTaskRefresh asks kie.ai what became of every task that can still change,
// and records what it says.
//
// A task whose endpoint this build cannot read, and one whose answer it cannot
// place, are reported and left exactly as they were: the id stays in the
// ledger, so the task can be collected once there is a decoder for it. Every
// task is asked about even so -- one endpoint this build does not know must
// not stop the rest of the ledger being collected -- and the command ends with
// a failure, because something the user asked for did not happen.
func runTaskRefresh(e *env, args []string) error {
	if len(args) != 0 {
		return usagef("task refresh: expected no arguments, got %d", len(args))
	}
	client, err := e.client()
	if err != nil {
		return err
	}
	c, err := loadCatalog(e)
	if err != nil {
		return err
	}

	ctx := context.Background()
	l, err := ledger.Open(ctx, e.paths.Ledger)
	if err != nil {
		return err
	}
	defer func() { _ = l.Close() }()

	tasks, err := l.List(ctx, kie.Unfinished...)
	if err != nil {
		return err
	}
	answers := askAbout(ctx, client, c, tasks)

	// The queries overlap; the writing does not. The ledger is one SQLite
	// file that every command shares, and holding it open across the
	// network calls would be holding it for as long as kie.ai takes.
	rows := make([]ledger.Task, 0, len(tasks))
	var problems []error
	for i, task := range tasks {
		// A row that could not be read is shown as it stands, so that
		// the listing is of every task that was asked about.
		row, err := record(ctx, l, task, answers[i])
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", task.TaskID, err))
		}
		rows = append(rows, row)
	}

	if err := writeTasks(e, rows); err != nil {
		return err
	}
	return errors.Join(problems...)
}

// answer is what one query produced: a state, or the reason there is none.
type answer struct {
	state kie.TaskState
	err   error
}

// askAbout queries kie.ai about each task, at most refreshConcurrency at a
// time, and returns the answers in the order the tasks were given in.
func askAbout(ctx context.Context, client *kie.Client, c catalog.Catalog, tasks []ledger.Task) []answer {
	answers := make([]answer, len(tasks))
	// A slot per query in flight. Each goroutine writes only its own
	// element, so the results need no lock of their own.
	slots := make(chan struct{}, refreshConcurrency)
	var wg sync.WaitGroup

	for i, task := range tasks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			answers[i] = ask(ctx, client, c, task)
		}()
	}
	wg.Wait()
	return answers
}

func ask(ctx context.Context, client *kie.Client, c catalog.Catalog, task ledger.Task) answer {
	m, err := findModel(c, task.ModelID)
	if err != nil {
		return answer{err: err}
	}
	state, err := client.QueryTask(ctx, m.Query.Path, m.Query.Param, task.TaskID)
	return answer{state: state, err: err}
}

// record writes an answer to the ledger, and returns the row as it now stands.
//
// A row that says the same thing is left alone rather than rewritten, so that
// updated_at goes on meaning "when this task last changed": a query that finds
// nothing new would otherwise make a task stuck since yesterday look as fresh
// as one that just moved.
func record(ctx context.Context, l *ledger.Ledger, task ledger.Task, a answer) (ledger.Task, error) {
	if a.err != nil {
		return task, a.err
	}
	if a.state.Status == task.Status && slices.Equal(a.state.ResultURLs, task.ResultURLs) && a.state.Error == task.Error {
		return task, nil
	}
	result := ledger.Result{Status: a.state.Status, ResultURLs: a.state.ResultURLs, Error: a.state.Error}
	if err := l.Update(ctx, task.TaskID, result); err != nil {
		return task, err
	}
	return l.Get(ctx, task.TaskID)
}

// taskSummary is the JSON contract of task list and task refresh.
type taskSummary struct {
	TaskID     string   `json:"taskId"`
	ModelID    string   `json:"modelId"`
	Status     string   `json:"status"`
	ResultURLs []string `json:"resultUrls"`
	// Error is present only on a task that failed, so that a consumer
	// cannot read an empty string as a failure with nothing to say.
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// writeTasks prints a listing. Both commands use it: refresh answers with the
// tasks it asked about, and there is no reason for the same rows to be laid
// out two ways.
func writeTasks(e *env, tasks []ledger.Task) error {
	if e.json {
		summaries := make([]taskSummary, 0, len(tasks))
		for _, task := range tasks {
			summaries = append(summaries, taskSummary{
				TaskID:     task.TaskID,
				ModelID:    task.ModelID,
				Status:     task.Status,
				ResultURLs: task.ResultURLs,
				Error:      task.Error,
				CreatedAt:  task.CreatedAt.Format(time.RFC3339),
				UpdatedAt:  task.UpdatedAt.Format(time.RFC3339),
			})
		}
		return writeJSON(e.stdout, summaries)
	}
	return writeTaskRows(e.stdout, tasks)
}

func writeTaskRows(w io.Writer, tasks []ledger.Task) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, task := range tasks {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			task.TaskID, task.Status, task.ModelID,
			task.CreatedAt.Format(time.RFC3339), orDash(detail(task)))
	}
	return tw.Flush()
}

// detail is the last column: what the task produced, or why it produced
// nothing. It is last because it is the one column with no bound on its width,
// and a column after it would be pushed about by every row.
func detail(task ledger.Task) string {
	if len(task.ResultURLs) > 0 {
		return strings.Join(task.ResultURLs, " ")
	}
	return oneLine(task.Error)
}
