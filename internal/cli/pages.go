package cli

import (
	"fmt"
	"io"
	"net/url"
	"os"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

// maxPageFileBytes only guards memory. The 1 MiB page limit is the server's
// rule and comes back as its 422.
const maxPageFileBytes = 4 << 20

// readPageFile reads the HTML page named by --page-file: a path, or - for
// stdin. The text is returned exactly as read.
func (a *app) readPageFile(path string) (string, error) {
	var r io.Reader
	if path == "-" {
		r = a.f.IOStreams.In
	} else {
		f, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("read page file: %w", err)
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, maxPageFileBytes+1))
	if err != nil {
		return "", fmt.Errorf("read page file: %w", err)
	}
	if len(b) > maxPageFileBytes {
		return "", cmdutil.Usagef("page file is larger than %d MiB", maxPageFileBytes>>20)
	}
	// An empty file would read as removing the page; that takes --page-file "".
	if len(b) == 0 {
		return "", cmdutil.Usagef("page file %s is empty; --page-file \"\" removes a page", path)
	}
	return string(b), nil
}

// noStdinClash is a usage error when --body and --page-file both read stdin.
func noStdinClash(body, pageFile string) error {
	if body == "-" && pageFile == "-" {
		return cmdutil.Usagef("--body - and --page-file - both read stdin; give one of them a file or text")
	}
	return nil
}

// pageSize renders a page size: bytes under 1 KB, KB without decimals, MB
// with one decimal from 1 MiB.
func pageSize(n int) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

// dashboardOrigin is where lorvikd serves the dashboard: the origin of the
// API URL.
func dashboardOrigin(c *client.Client) string {
	u, err := url.Parse(c.BaseURL())
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// cardLink and noteLink are the dashboard routes in web/src/router.ts.
func cardLink(origin, project string, number int64) string {
	return fmt.Sprintf("%s/p/%s/cards/%d", origin, url.PathEscape(project), number)
}

func noteLink(origin string, project *string, slug string) string {
	l := origin + "/notes/" + url.PathEscape(slug)
	if project != nil && *project != "" {
		l += "?project=" + url.QueryEscape(*project)
	}
	return l
}

// pageLine prints the one-line pointer to a page on the dashboard.
func pageLine(p *cmdutil.Printer, bytes int, link string) {
	p.Line("Page: %s, open %s", pageSize(bytes), link)
}

// revisionPageLine points at the command that prints a past version's page;
// an old version has no dashboard link.
func revisionPageLine(p *cmdutil.Printer, page, command string) {
	p.Line("Page: %s, print it with %s", pageSize(len(page)), command)
}

// showRevisionPage prints a past version's HTML source, or the revision under
// --json. An empty page is exit 3.
func (a *app) showRevisionPage(what string, rev any, page string) error {
	if page == "" {
		return &cmdutil.ExitError{Code: cmdutil.ExitNotFound, Err: fmt.Errorf("%s has no HTML page", what)}
	}
	if a.asJSON {
		return cmdutil.WriteJSON(a.out(), rev)
	}
	return a.payload(page)
}

// showPage prints a page's HTML source, or the HTMLPage under --json. A 404
// on the page route means no page when check succeeds, which tells it apart
// from a missing card or note.
func (a *app) showPage(what string, get func() (apitypes.HTMLPage, error), check func() error) error {
	page, err := get()
	if isNotFound(err) {
		if cerr := check(); cerr != nil {
			return cerr
		}
		return &cmdutil.ExitError{Code: cmdutil.ExitNotFound, Err: fmt.Errorf("%s has no HTML page", what)}
	}
	if err != nil {
		return err
	}
	if a.asJSON {
		return cmdutil.WriteJSON(a.out(), page)
	}
	return a.payload(page.HTML)
}
