// Package llms reads docs.kie.ai/llms.txt, the index of every documentation
// page, and turns its breadcrumbs into the catalog's category and vendor axes.
package llms

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// apiDocsHeading is the section that lists the endpoint reference pages. The
// other sections hold quickstarts and callback descriptions, which carry no
// OpenAPI document.
const apiDocsHeading = "## API Docs"

// entryPattern matches `- <breadcrumb> [<title>](<url>): <description>`, where
// the breadcrumb and the description may both be absent. The title is matched
// but not kept: a model is named by its own OpenAPI summary.
var entryPattern = regexp.MustCompile(`^-\s+(.*?)\[[^\]]*\]\((\S+?)\)(?::\s?(.*))?$`)

// Entry is one documentation page as llms.txt describes it.
type Entry struct {
	// Breadcrumb is the site navigation path, e.g. ["Image Models", "Seedream"].
	// Pages outside the navigation tree have none.
	Breadcrumb []string
	URL        string
	// Description is the site's one-line summary, which is really just the
	// page's first line and is often a heading rather than prose.
	Description string
}

// DocsPath is the entry's URL without the site root and the .md suffix, e.g.
// "suno-api/generate-music". It is stable enough to key tables on, and standard
// API models use it as their id.
func (e Entry) DocsPath() string {
	return docsPath(e.URL)
}

func docsPath(entryURL string) string {
	parsed, err := url.Parse(entryURL)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(parsed.Path, "/"), ".md")
}

// ParseAPIDocs returns the English endpoint reference pages listed in llms.txt.
//
// Chinese translations live under /cn/ and duplicate the English pages, so they
// are dropped. Any line in the section that is not an entry fails the parse:
// silently skipping it would drop models without anyone noticing. So does a
// link that leaves the site the index itself came from.
func ParseAPIDocs(text string) ([]Entry, error) {
	var entries []Entry
	inSection := false
	seenSection := false
	host := ""

	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "#") {
			inSection = strings.TrimSpace(line) == apiDocsHeading
			seenSection = seenSection || inSection
			continue
		}
		if !inSection || strings.TrimSpace(line) == "" {
			continue
		}

		m := entryPattern.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("llms.txt: unparsable entry: %s", line)
		}
		entryURL := m[2]
		parsed, err := url.Parse(entryURL)
		if err != nil || parsed.Host == "" {
			return nil, fmt.Errorf("llms.txt: %s is not an absolute url", entryURL)
		}
		if host == "" {
			host = parsed.Host
		}
		if parsed.Host != host {
			return nil, fmt.Errorf("llms.txt: %s is not on %s", entryURL, host)
		}
		if strings.HasPrefix(docsPath(entryURL), "cn/") {
			continue
		}
		entries = append(entries, Entry{
			Breadcrumb:  splitBreadcrumb(m[1]),
			URL:         entryURL,
			Description: strings.TrimSpace(m[3]),
		})
	}

	if !seenSection {
		return nil, fmt.Errorf("llms.txt: %q section not found", apiDocsHeading)
	}
	return entries, nil
}

// splitBreadcrumb splits `Image    Models > Seedream` into its levels. The site
// pads some level names with runs of spaces, so whitespace is collapsed.
func splitBreadcrumb(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var levels []string
	for _, level := range strings.Split(s, ">") {
		levels = append(levels, strings.Join(strings.Fields(level), " "))
	}
	return levels
}

// categoryAliases pins the two first-level names that do not follow the
// `<X> Models` shape kie.ai uses everywhere else. Both are its own omissions:
// it already files Runway and 4o Image as `Video Models > Runway API` and
// `Image Models > 4o Image API`, and `Music Models` exists. Anything else that
// misses the shape fails the generation rather than growing this table by
// accident.
var categoryAliases = map[string]string{
	"Suno API":   "music",
	"Veo3.1 API": "video",
}

const modelsSuffix = " Models"

// Taxonomy derives the category and vendor axes from a breadcrumb.
//
// The category is the first level with its ` Models` suffix dropped, and the
// vendor is the second level; both are slugged. For the aliased first levels
// there is no usable second level (Suno files by feature — `WAV Conversion` —
// not by vendor), so the vendor comes from the first level instead.
func Taxonomy(breadcrumb []string) (category, vendor string, err error) {
	if len(breadcrumb) == 0 {
		return "", "", fmt.Errorf("empty breadcrumb")
	}
	first := breadcrumb[0]

	if category, ok := categoryAliases[first]; ok {
		return category, slug(first), nil
	}
	if !strings.HasSuffix(first, modelsSuffix) {
		return "", "", fmt.Errorf("unknown breadcrumb %q: expected %q or a name ending in %q",
			first, "<name> Models", modelsSuffix)
	}
	if len(breadcrumb) < 2 {
		return "", "", fmt.Errorf("breadcrumb %q has no vendor level", first)
	}
	return slug(strings.TrimSuffix(first, modelsSuffix)), slug(breadcrumb[1]), nil
}

// slug lowercases a breadcrumb level and makes it typable as a filter value:
// the decorative ` API` suffix goes, and spaces become hyphens.
func slug(level string) string {
	level = strings.TrimSuffix(level, " API")
	return strings.ToLower(strings.Join(strings.Fields(level), "-"))
}
