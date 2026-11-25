package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

const (
	maxSlugLen      = 64
	maxListedSlugs  = 10
	initProjectDesc = "Cards, decisions and notes from here go to this project."
)

// initOpts are the init flags.
type initOpts struct {
	create     bool
	name       string
	visibility string
}

func newInit(a *app) *cobra.Command {
	var o initOpts
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Bind this directory to a project with .lorvik.toml",
		Long: `Write .lorvik.toml so every lorvik command run inside this repo uses the
project. On a terminal, init walks you through where to bind, which project to
use and, if you may, how to create one. Agents, scripts and CI never get a
prompt: init takes its answers from flags only (--project, --create, --name,
--visibility) and fails with the exact command to run when one is missing.

The project comes from --project, then LORVIK_PROJECT, then the directory name
(agent path only). --api-url is saved in the file when given; an api_url
already in the file is kept.

Examples:
  lorvik init
  lorvik init -p payments --create --name Payments --visibility private`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := oneOf("visibility", o.visibility, "org", "private"); err != nil {
				return err
			}
			s, err := a.settings()
			if err != nil {
				return err
			}
			cwd, err := a.cwd()
			if err != nil {
				return err
			}
			if a.interactive() {
				err := a.initInteractive(cmd.Context(), o, s, cwd)
				// Line-mode prompts read a cooked terminal, where Ctrl-C is
				// SIGINT and cancels the context instead of returning
				// ErrCancelled.
				if errors.Is(err, cmdutil.ErrCancelled) || errors.Is(err, context.Canceled) {
					fmt.Fprintln(a.errOut(), "  Cancelled. Nothing was changed.")
					return &cmdutil.ExitError{Code: cmdutil.ExitUsage, Err: cmdutil.ErrSilent}
				}
				return err
			}
			return a.initPlain(cmd.Context(), o, s, cwd)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.create, "create", false, "Create the project on the server if it does not exist")
	f.StringVar(&o.name, "name", "", "Project name for --create (default: the slug)")
	f.StringVar(&o.visibility, "visibility", "", "Visibility for --create: org or private (default: the org setting)")
	return cmd
}

// cwd returns the working directory.
func (a *app) cwd() (string, error) {
	getwd := os.Getwd
	if a.f.Getwd != nil {
		getwd = a.f.Getwd
	}
	cwd, err := getwd()
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	return cwd, nil
}

// notSignedIn is the error for a missing token. A factory client carries its
// own credentials, so it is never "not signed in".
func (a *app) notSignedIn(s Settings) error {
	if s.Token != "" || a.f.Client != nil {
		return nil
	}
	return &cmdutil.ExitError{
		Code: cmdutil.ExitUnauthorized,
		Err:  fmt.Errorf("not signed in to %s; run lorvik login (agents and CI: LORVIK_TOKEN or LORVIK_CONFIG)", s.APIURL),
	}
}

// initPlain is the agent and script path: flags only, never a prompt.
func (a *app) initPlain(ctx context.Context, o initOpts, s Settings, cwd string) error {
	if err := a.notSignedIn(s); err != nil {
		return err
	}
	slug, derived := s.Project, false
	if slug == "" {
		slug, derived = slugify(filepath.Base(cwd)), true
	}
	if slug == "" {
		return cmdutil.Usagef("cannot derive a project slug; pass --project <slug>")
	}
	c, err := a.client()
	if err != nil {
		return err
	}
	if _, err := c.GetProject(ctx, slug); err != nil {
		if !isNotFound(err) {
			return err
		}
		if !o.create {
			return a.missingProject(ctx, c, slug, derived)
		}
		name := o.name
		if name == "" {
			name = slug
		}
		if _, err := c.CreateProject(ctx, apitypes.CreateProject{Slug: slug, Name: name, Visibility: o.visibility}); err != nil {
			return err
		}
		fmt.Fprintf(a.errOut(), "lorvik: created project %s\n", slug)
	}
	if _, ok := a.gitRoot(cwd); !ok {
		fmt.Fprintf(a.errOut(), "lorvik: %s is not inside a git repository; %s applies to every folder below it\n", cwd, bindingFile)
	}
	path, err := a.writeBinding(cwd, slug)
	if err != nil {
		return err
	}
	return a.emit(map[string]string{"project": slug, "file": path}, func(p *cmdutil.Printer) {
		p.Line("bound %s to project %s", path, slug)
	})
}

// missingProject is the error for a project that does not exist and was not
// asked to be created. It lists the projects the caller could use instead.
func (a *app) missingProject(ctx context.Context, c *client.Client, slug string, derived bool) error {
	var b strings.Builder
	fmt.Fprintf(&b, "project %q does not exist", slug)
	if derived {
		b.WriteString(" (named after this folder)")
	}
	var names []string
	if ps, err := allProjects(ctx, c); err == nil {
		for _, p := range ps {
			if p.Status != "archived" {
				names = append(names, p.Slug)
			}
		}
	}
	if len(names) > 0 {
		shown := names[:min(len(names), maxListedSlugs)]
		list := strings.Join(shown, ", ")
		if len(names) > maxListedSlugs {
			list += ", ..."
		}
		fmt.Fprintf(&b, "\nprojects you can use: %s", list)
		fmt.Fprintf(&b, "\nhint: lorvik init -p %s, or create it: lorvik init -p %s --create", names[0], slug)
	} else {
		fmt.Fprintf(&b, "\nhint: create it: lorvik init -p %s --create", slug)
	}
	return cmdutil.Usagef("%s", b.String())
}

// writeBinding writes dir/.lorvik.toml with the project and the api_url from
// --api-url, else the one already in that file.
func (a *app) writeBinding(dir, slug string) (string, error) {
	path := filepath.Join(dir, bindingFile)
	apiURL := a.apiURL
	if apiURL == "" {
		old, err := readTOMLFile(path)
		if err != nil {
			return "", err
		}
		apiURL = old["api_url"]
	}
	body := renderTOML([2]string{"project", slug}, [2]string{"api_url", apiURL})
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil { //nolint:gosec // repo file, meant to be committed
		return "", fmt.Errorf("write %s: %w", bindingFile, err)
	}
	return path, nil
}

// gitRoot walks up from dir to the top of its git working tree (isGitRoot).
// Like git, it never walks up into a directory listed in
// GIT_CEILING_DIRECTORIES.
func (a *app) gitRoot(dir string) (string, bool) {
	ceilings := filepath.SplitList(a.getenv("GIT_CEILING_DIRECTORIES"))
	for {
		if isGitRoot(dir) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir || slices.ContainsFunc(ceilings, func(c string) bool { return c != "" && filepath.Clean(c) == parent }) {
			return "", false
		}
		dir = parent
	}
}

// tilde shortens a path under $HOME to ~/....
func (a *app) tilde(path string) string {
	home := a.getenv("HOME")
	if home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~/" + filepath.ToSlash(rest)
	}
	return path
}

// style decorates stderr text. It is plain unless stderr is a terminal and
// NO_COLOR is unset; the Printer is not used because it writes stdout.
type style struct{ on bool }

func (a *app) style() style {
	return style{on: a.f.IOStreams.IsStderrTTY() && a.getenv("NO_COLOR") == ""}
}

func (s style) wrap(code, text string) string {
	if !s.on {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s style) bold(t string) string  { return s.wrap("1", t) }
func (s style) faint(t string) string { return s.wrap("2", t) }
func (s style) green(t string) string { return s.wrap("32", t) }

// identity names the caller: the user's name, else login, else the agent name.
func identity(me apitypes.Me) string {
	switch {
	case me.User != nil && me.User.Name != "":
		return me.User.Name
	case me.User != nil && me.User.Login != "":
		return me.User.Login
	case me.Agent != nil && me.Agent.Name != "":
		return me.Agent.Name
	}
	return me.Actor.ID
}

// initInteractive is the guided flow. Every prompt goes through the
// Prompter; ErrCancelled from any of them reaches the caller unchanged.
func (a *app) initInteractive(ctx context.Context, o initOpts, s Settings, cwd string) error {
	p, w, st := a.prompter(), a.errOut(), a.style()

	if a.notSignedIn(s) != nil {
		var err error
		if s, err = a.inlineLogin(ctx, p, s, "You are not signed in to %s."); err != nil {
			return err
		}
	}
	c, err := a.client()
	if err != nil {
		return err
	}
	me, err := c.Me(ctx)
	// A stored token the server rejects (expired or revoked) gets the same
	// inline sign-in. One from LORVIK_TOKEN or a factory client would still
	// win over the new one, so those keep the error and its hint.
	if isUnauthorized(err) && !s.TokenFromEnv && a.f.Client == nil {
		if s, err = a.inlineLogin(ctx, p, s, "Your sign-in to %s has expired or was revoked."); err != nil {
			return err
		}
		if c, err = a.client(); err != nil {
			return err
		}
		me, err = c.Me(ctx)
	}
	if err != nil {
		return err
	}
	who := "Signed in as " + identity(me) + " · " + me.Org.Name
	if me.Role != "" {
		who += " · " + me.Role
	}
	fmt.Fprintf(w, "\n  %s  %s\n  %s\n\n", st.bold("lorvik init"), s.APIURL, who)

	target, inRepo, err := a.chooseTarget(ctx, p, cwd)
	if err != nil {
		return err
	}

	canCreate := me.Role == "admin" || me.Role == "owner"
	explicit := first(a.project, a.getenv("LORVIK_PROJECT"))
	slug, created, err := a.chooseProject(ctx, p, c, me, o, target, explicit, canCreate)
	if err != nil {
		return err
	}

	if _, err := a.writeBinding(target, slug); err != nil {
		return err
	}
	if created {
		fmt.Fprintf(w, "  %s Created project %s\n", st.green("✓"), slug)
	}
	fmt.Fprintf(w, "  %s Bound %s to %s\n", st.green("✓"), a.tilde(target), slug)
	if inRepo {
		fmt.Fprintf(w, "%s\n", st.faint("    Commit "+bindingFile+" so teammates and agents land in the same project."))
	}
	fmt.Fprintf(w, "\n%s\n    lorvik add \"First card\"   put work on the board\n    lorvik next               see the best ready card\n", st.faint("  Next"))
	return nil
}

// inlineLogin signs in with a password and saves the token, as `lorvik login`
// does, then returns the settings that now carry it. reason is the line shown
// first, with %s for the server URL.
func (a *app) inlineLogin(ctx context.Context, p cmdutil.Prompter, s Settings, reason string) (Settings, error) {
	fmt.Fprintf(a.errOut(), "  "+reason+"\n", s.APIURL)
	if err := a.trustBoundServer(ctx); err != nil {
		return s, err
	}
	login, err := p.Input(ctx, cmdutil.Question{Title: "Login"}, "", func(v string) error {
		if strings.TrimSpace(v) == "" {
			return errors.New("a login is required")
		}
		return nil
	})
	if err != nil {
		return s, err
	}
	pw := a.getenv("LORVIK_PASSWORD")
	if pw == "" {
		if pw, err = p.Password(ctx, cmdutil.Question{Title: "Password"}); err != nil {
			return s, err
		}
	}
	res, s, err := a.passwordLogin(ctx, apitypes.Login{Login: strings.TrimSpace(login), Password: pw}, "")
	if err != nil {
		return s, err
	}
	fmt.Fprintf(a.errOut(), "lorvik: signed in to %s; token %s saved to %s\n", s.APIURL, res.Token.Prefix, userConfigPath(a.getenv))
	return s, nil
}

// chooseTarget decides which directory gets the binding: the repo root or the
// current folder when they differ, the current folder after a confirmation
// when there is no repository.
func (a *app) chooseTarget(ctx context.Context, p cmdutil.Prompter, cwd string) (target string, inRepo bool, err error) {
	st, w := a.style(), a.errOut()
	root, inRepo := a.gitRoot(cwd)
	switch {
	case !inRepo:
		fmt.Fprintf(w, "  ! %s is not inside a git repository.\n%s\n", a.tilde(cwd),
			st.faint("    A "+bindingFile+" here also applies to every folder below it that is not a repository."))
		ok, err := p.Confirm(ctx, cmdutil.Question{Title: "Bind " + a.tilde(cwd) + " anyway?"}, false)
		if err != nil {
			return "", false, err
		}
		if !ok {
			return "", false, cmdutil.ErrCancelled
		}
		return cwd, false, nil
	case root == cwd:
		return cwd, true, nil
	}
	i, err := p.Select(ctx, cmdutil.Question{Title: "Bind which folder?"}, []cmdutil.Option{
		{Label: "The whole repository", Detail: a.tilde(root)},
		{Label: "Only this folder", Detail: a.tilde(cwd)},
	}, 0)
	if err != nil {
		return "", false, err
	}
	if i == 1 {
		return cwd, true, nil
	}
	return root, true, nil
}

// chooseProject resolves the slug to bind, creating the project when that is
// what the person or the flags ask for. created reports a creation.
func (a *app) chooseProject(ctx context.Context, p cmdutil.Prompter, c *client.Client, me apitypes.Me, o initOpts, target, explicit string, canCreate bool) (slug string, created bool, err error) {
	if explicit != "" {
		if _, err := c.GetProject(ctx, explicit); err == nil {
			return explicit, false, nil
		} else if !isNotFound(err) {
			return "", false, err
		}
		if !canCreate {
			return "", false, a.missingProject(ctx, c, explicit, false)
		}
		slug, err = a.createProject(ctx, p, c, me, o, target, explicit, nil)
		return slug, err == nil, err
	}

	all, err := allProjects(ctx, c)
	if err != nil {
		return "", false, err
	}
	var projects []apitypes.Project
	for _, pr := range all {
		if pr.Status != "archived" {
			projects = append(projects, pr)
		}
	}
	if len(projects) == 0 && !canCreate {
		return "", false, &cmdutil.ExitError{
			Code: cmdutil.ExitNotFound,
			Err:  errors.New("no projects you can use, and only org admins can create one; ask an admin for access"),
		}
	}

	current := ""
	if _, bound, err := findBinding(target); err == nil {
		current = bound["project"]
	}
	folder := slugify(filepath.Base(target))
	taken := make(map[string]bool, len(projects))
	opts := make([]cmdutil.Option, 0, len(projects)+1)
	byBinding, byFolder := -1, -1
	for i, pr := range projects {
		taken[pr.Slug] = true
		parts := slices.DeleteFunc([]string{pr.Name, pr.Role, pr.Visibility}, func(s string) bool { return s == "" })
		if pr.Slug == current {
			parts = append(parts, "current")
			byBinding = i
		}
		if pr.Slug == folder {
			parts = append(parts, "matches this folder")
			byFolder = i
		}
		opts = append(opts, cmdutil.Option{Label: pr.Slug, Detail: strings.Join(parts, " · ")})
	}
	if canCreate {
		label := "Create a new project"
		if folder != "" && !taken[folder] {
			label = fmt.Sprintf("Create a new project %q", folder)
		}
		opts = append(opts, cmdutil.Option{Label: label})
	}
	// Preselect the current binding, else the folder match, else create,
	// else the first project.
	def := 0
	switch {
	case byBinding >= 0:
		def = byBinding
	case byFolder >= 0:
		def = byFolder
	case canCreate:
		def = len(opts) - 1
	}

	i := len(opts) - 1
	if len(projects) > 0 {
		i, err = p.Select(ctx, cmdutil.Question{
			Title:       "Which project should " + a.tilde(target) + " use?",
			Description: initProjectDesc,
		}, opts, def)
		if err != nil {
			return "", false, err
		}
	}
	if i < len(projects) {
		return projects[i].Slug, false, nil
	}
	slug, err = a.createProject(ctx, p, c, me, o, target, "", taken)
	return slug, err == nil, err
}

// createProject asks for what the flags did not answer and creates the
// project. fixed is a slug already decided (by -p), so it is not asked.
func (a *app) createProject(ctx context.Context, p cmdutil.Prompter, c *client.Client, me apitypes.Me, o initOpts, target, fixed string, taken map[string]bool) (string, error) {
	name := o.name
	if name == "" {
		var err error
		name, err = p.Input(ctx, cmdutil.Question{Title: "Name", Description: "Shown on the dashboard."},
			upperFirst(filepath.Base(target)), func(v string) error {
				if strings.TrimSpace(v) == "" {
					return errors.New("a name is required")
				}
				return nil
			})
		if err != nil {
			return "", err
		}
		name = strings.TrimSpace(name)
	}
	slug := fixed
	if slug == "" {
		var err error
		slug, err = p.Input(ctx, cmdutil.Question{
			Title:       "Slug",
			Description: "Short name for URLs and --project: lowercase letters, digits and dashes.",
		}, slugify(name), func(v string) error {
			switch {
			case v == "":
				return errors.New("a slug is required")
			case slugify(v) != v:
				return errors.New("use lowercase letters, digits and single dashes")
			case len(v) > maxSlugLen:
				return fmt.Errorf("at most %d characters", maxSlugLen)
			case taken[v]:
				return fmt.Errorf("%s already exists; pick it from the list", v)
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	vis := o.visibility
	if vis == "" {
		def := 0
		if me.Org.Settings != nil && me.Org.Settings.ProjectVisibilityDefault == "private" {
			def = 1
		}
		i, err := p.Select(ctx, cmdutil.Question{Title: "Who can see it?"}, []cmdutil.Option{
			{Label: "Everyone in " + me.Org.Name},
			{Label: "Only people you add"},
		}, def)
		if err != nil {
			return "", err
		}
		vis = []string{"org", "private"}[i]
	}
	if _, err := c.CreateProject(ctx, apitypes.CreateProject{Slug: slug, Name: name, Visibility: vis}); err != nil {
		return "", err
	}
	return slug, nil
}

// upperFirst upper-cases the first letter of s.
func upperFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}
