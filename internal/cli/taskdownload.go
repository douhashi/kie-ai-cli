package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/douhashi/kie-ai-cli/internal/kie"
	"github.com/douhashi/kie-ai-cli/internal/ledger"
)

// The flags task download takes of its own. --unsaved is shared with task
// list, where it means the same thing.
const (
	flagUnsaved = "unsaved"
	flagDir     = "dir"
)

func bindTaskDownload(_ *env, fs *flag.FlagSet, _ []string) (binding, error) {
	unsaved := fs.Bool(flagUnsaved, false, "save every task that has produced something nothing has saved yet")
	dir := fs.String(flagDir, "", "the directory to save into, made if it is not there (default: the current one)")
	return binding{run: func(e *env, args []string) error {
		return runTaskDownload(e, args, *unsaved, *dir)
	}}, nil
}

// runTaskDownload writes what tasks produced to disk and records where it put
// it.
//
// One task at a time. What a task produced is a video or a set of images, and
// several of those at once would compete for the same uplink to arrive no
// sooner; the queries task refresh overlaps are short exchanges, which is a
// different thing. A task that cannot be saved does not stop the ones after it
// -- the results expire, so a run that gave up half way would lose them -- and
// the command fails at the end with everything that went wrong.
func runTaskDownload(e *env, args []string, unsaved bool, dir string) error {
	if len(args) > 1 {
		return usagef("task download: expected one <task-id>, got %d arguments", len(args))
	}
	// Exactly one of the two says what to save. Neither is a command that
	// would do nothing, and both is a contradiction rather than a task id
	// with an ignored flag beside it.
	named := len(args) == 1
	if named == unsaved {
		return usagef("task download: expected <task-id> or --%s", flagUnsaved)
	}
	// Resolved before anything is opened or written, so that a missing key
	// is reported as itself: a refused result is fetched again through a
	// fresh link, and that call is an authenticated one.
	client, err := e.client()
	if err != nil {
		return err
	}
	into, err := destination(dir)
	if err != nil {
		return err
	}

	ctx := context.Background()
	l, err := ledger.Open(ctx, e.paths.Ledger)
	if err != nil {
		return err
	}
	defer func() { _ = l.Close() }()

	tasks, err := toSave(ctx, l, args, unsaved)
	if err != nil {
		return err
	}
	if unsaved && len(tasks) == 0 {
		fmt.Fprintf(e.stderr, "%s: there is nothing left to save\n", name)
	}

	saved := make([]savedTask, 0, len(tasks))
	var problems []error
	for _, task := range tasks {
		paths, err := saveTask(ctx, e, client, l, task, into)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", task.TaskID, err))
			continue
		}
		if len(paths) > 0 {
			saved = append(saved, savedTask{TaskID: task.TaskID, SavedPaths: paths})
		}
	}

	if err := writeSaved(e, saved); err != nil {
		return err
	}
	return errors.Join(problems...)
}

// toSave is the tasks the command was asked to save: the one named, or every
// one that has something left to save.
func toSave(ctx context.Context, l *ledger.Ledger, args []string, unsaved bool) ([]ledger.Task, error) {
	if unsaved {
		return l.ListUnsaved(ctx)
	}
	task, err := l.Get(ctx, args[0])
	if err != nil {
		return nil, err
	}
	return []ledger.Task{task}, nil
}

// destination is the directory to save into, as an absolute path, made if it
// is not there yet.
//
// The default is the current directory rather than anywhere under the state
// directory: what a task produced is the user's work, not this tool's state,
// and it is theirs to put wherever they keep such things.
func destination(dir string) (string, error) {
	if dir == "" {
		dir = "."
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	// 0755, unlike the state directory: these are the user's own files and
	// nothing about them is private to this tool.
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", absolute, err)
	}
	return absolute, nil
}

// saveTask writes everything one task produced into dir and records the paths.
// It answers with no paths and no error for a task there is nothing to do
// about, having said on stderr why.
//
// The recording comes last and covers the whole task: half a task recorded as
// saved would leave the unsaved listing, and nothing would ever go back for the
// rest of it. A file already renamed when a later one fails is left where it
// is -- the task stays unsaved, so the next run fetches it again and writes
// over it.
func saveTask(ctx context.Context, e *env, client *kie.Client, l *ledger.Ledger, task ledger.Task, dir string) ([]string, error) {
	if len(task.SavedPaths) > 0 {
		fmt.Fprintf(e.stderr, "%s: %s is already saved to %s\n", name, task.TaskID, strings.Join(task.SavedPaths, " "))
		return nil, nil
	}
	if len(task.ResultURLs) == 0 {
		return nil, nothingToSave(task)
	}
	if err := usableAsAName(task.TaskID); err != nil {
		return nil, err
	}

	saved := make([]string, 0, len(task.ResultURLs))
	for i, resultURL := range task.ResultURLs {
		path, err := saveResult(ctx, client, task.TaskID, i+1, resultURL, dir)
		if err != nil {
			return nil, err
		}
		saved = append(saved, path)
	}
	if err := l.MarkSaved(ctx, task.TaskID, saved); err != nil {
		return nil, err
	}
	return saved, nil
}

// nothingToSave says why a task the caller named has no files behind it. Which
// of the two reasons it is decides what to do next, so neither is reported as
// the other.
func nothingToSave(task ledger.Task) error {
	if task.Status != kie.StatusSucceeded {
		return fmt.Errorf("the task is %s; only one that has succeeded has anything to save, and `%s task refresh` is what finds out",
			task.Status, name)
	}
	return errors.New("the task succeeded with no files: this model answers with its result rather than with a link to one")
}

// usableAsAName refuses a task id that would not stay inside the destination.
// The ids come from kie.ai and are hexadecimal in practice, but they are read
// off the network and are about to be made part of a path.
func usableAsAName(taskID string) error {
	if taskID == "" || strings.ContainsAny(taskID, `/\`) {
		return errors.New("the task id cannot be part of a file name")
	}
	return nil
}

// tempPattern is what a file is called while it is being written. It is hidden
// and named after this tool so that an interrupted run leaves something
// recognisable rather than a file that looks like a result.
const tempPattern = ".kie-ai-cli-download-*"

// saveResult writes one result into dir and answers with the path it took.
//
// It is written under a temporary name in that same directory and renamed once
// it is whole, so an interrupted download cannot leave a truncated file under
// the name of a complete one -- and the rename is within one directory, so it
// is the atomic kind. The name is only settled at the end because what the file
// is called depends on what the host said it was serving.
func saveResult(ctx context.Context, client *kie.Client, taskID string, n int, resultURL, dir string) (string, error) {
	tmp, err := os.CreateTemp(dir, tempPattern)
	if err != nil {
		return "", err
	}
	// Removing the temporary file is the failure path; on success it has
	// been renamed by then and there is nothing at that name to remove.
	defer func() { _ = os.Remove(tmp.Name()) }()

	mediaType, err := client.Download(ctx, resultURL, tmp)
	if err != nil {
		_ = tmp.Close()
		return "", err
	}
	// CreateTemp makes the file readable by its owner alone. What is in it
	// is the user's own work rather than a credential, so it is left as
	// ordinary files are.
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}

	saved := filepath.Join(dir, fmt.Sprintf("%s-%d%s", taskID, n, extension(resultURL, mediaType)))
	if err := os.Rename(tmp.Name(), saved); err != nil {
		return "", err
	}
	return saved, nil
}

// extension is what to call the file, after the URL it came from and then
// after what the host said it was serving.
//
// The URL comes first because it is where kie.ai puts the extension and it is
// the more specific of the two: a host that serves every result as
// application/octet-stream still names them .png and .mp4. A result that
// answers neither way is saved without one, rather than under a guess.
func extension(resultURL, mediaType string) string {
	if ext := plain(path.Ext(urlPath(resultURL))); ext != "" {
		return ext
	}
	// The first of the extensions registered for the type, which is the
	// alphabetically first: they are alternative spellings of one format,
	// and any of them names the file correctly.
	known, err := mime.ExtensionsByType(mediaType)
	if err != nil || len(known) == 0 {
		return ""
	}
	return plain(known[0])
}

// urlPath is the path part of an address, which is the part an extension can
// be read out of: a query string is not part of the name of the file.
func urlPath(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Path
}

// maxExtension is how long a suffix may be and still be an extension. Four is
// the longest in ordinary use (.jpeg, .webp); eight leaves room without
// letting a URL name the file whatever it likes.
const maxExtension = 8

// plain keeps an extension only if it reads as one: a dot and a few letters or
// digits. What is read off a URL is not a name this tool chose, and a suffix
// holding a space, a quote or anything else a shell reads is not worth the file
// being called what it is.
func plain(ext string) string {
	if len(ext) < 2 || len(ext) > maxExtension+1 || ext[0] != '.' {
		return ""
	}
	for _, r := range ext[1:] {
		alphanumeric := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if !alphanumeric {
			return ""
		}
	}
	return ext
}

// savedTask is the JSON contract of task download: what each task produced is
// now at, on this machine.
type savedTask struct {
	TaskID     string   `json:"taskId"`
	SavedPaths []string `json:"savedPaths"`
}

// writeSaved prints what this run saved. The plain form is the paths alone,
// one per line: the reason to save a result is to open or process the file, and
// `kie task download --unsaved | xargs -n1 open` has to be all it takes. Which
// task each belongs to is in the name, and in --json as a field of its own.
func writeSaved(e *env, saved []savedTask) error {
	if e.json {
		return writeJSON(e.stdout, saved)
	}
	for _, task := range saved {
		for _, path := range task.SavedPaths {
			if _, err := fmt.Fprintln(e.stdout, path); err != nil {
				return err
			}
		}
	}
	return nil
}
