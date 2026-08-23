package cli

import (
	"context"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/douhashi/kie-ai-cli/internal/catalog"
)

func runCatalogUpdate(e *env, args []string) error {
	if len(args) != 0 {
		return usagef("catalog update: expected no arguments, got %d", len(args))
	}
	c, err := catalog.Update(context.Background(), e.paths.Catalog)
	if err != nil {
		return err
	}
	// What was just downloaded, in the shape catalog show reports: the command
	// has no line of its own, and what a caller wants to know afterwards is
	// exactly what the other verb answers.
	return writeCatalogState(e, c)
}

func runCatalogShow(e *env, args []string) error {
	if len(args) != 0 {
		return usagef("catalog show: expected no arguments, got %d", len(args))
	}
	c, err := loadCatalog(e)
	if err != nil {
		return err
	}
	return writeCatalogState(e, c)
}

// catalogState is what catalog show and catalog update report: which catalog
// is in effect, how old it is, and how much it holds.
//
// The path is the downloaded catalog's directory, and is absent for the
// embedded one, which is not a file anyone can point at. It is what makes the
// way back discoverable: deleting that directory is what returns the binary to
// the catalog it carries, and there is no command for it.
type catalogState struct {
	Origin      string `json:"origin"`
	Path        string `json:"path,omitempty"`
	GeneratedAt string `json:"generatedAt"`
	AgeDays     int    `json:"ageDays"`
	Models      int    `json:"models"`
}

func newCatalogState(c catalog.Catalog, now time.Time) catalogState {
	return catalogState{
		Origin:      string(c.Origin),
		Path:        c.Path,
		GeneratedAt: c.GeneratedAt.Format(time.DateOnly),
		AgeDays:     int(now.Sub(c.GeneratedAt) / (24 * time.Hour)),
		Models:      len(c.Models),
	}
}

func writeCatalogState(e *env, c catalog.Catalog) error {
	s := newCatalogState(c, e.now)
	if e.json {
		return writeJSON(e.stdout, s)
	}
	rows := [][2]string{{"origin", s.Origin}}
	if s.Path != "" {
		rows = append(rows, [2]string{"path", s.Path})
	}
	rows = append(rows,
		[2]string{"generated", fmt.Sprintf("%s (%s)", s.GeneratedAt, age(s.AgeDays))},
		[2]string{"models", fmt.Sprint(s.Models)},
	)

	w := tabwriter.NewWriter(e.stdout, 0, 0, 2, ' ', 0)
	for _, row := range rows {
		fmt.Fprintf(w, "%s\t%s\n", row[0], row[1])
	}
	return w.Flush()
}

// age reads the number of days as English. A clock behind the one that
// generated the catalog gives a negative count, which is reported as the same
// day rather than as a catalog from the future.
func age(days int) string {
	switch {
	case days <= 0:
		return "today"
	case days == 1:
		return "1 day ago"
	default:
		return fmt.Sprintf("%d days ago", days)
	}
}
