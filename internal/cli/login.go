package cli

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

func newLogin(a *app) *cobra.Command {
	var in apitypes.Login
	var token, createOrg string
	cmd := &cobra.Command{
		Use:   "login [login]",
		Short: "Sign in with GitHub or a password, or store a token you were given",
		Long: `Sign in and save the token, with the server URL, in your user config file so
later commands use it. There are two methods.

GitHub (the default when no login is given): lorvik login prints a code and a
GitHub address, you open it in a browser and enter the code, and the CLI
exchanges your GitHub identity for a token. The server must have GitHub login
enabled. If your access to the org is still waiting for an admin, login exits 5
and says so.

Password: lorvik login <login> (or --password with --login) exchanges a login
and password for a token. Password login must be enabled on the server. The
password comes from LORVIK_PASSWORD when set; otherwise it is prompted for on a
terminal; otherwise login fails. It is never taken from a flag, which would
leak into shell history.

Which org: if you belong to one org you are signed in to it. If you belong to
several, or an org has invited you, pass --org <id>; without it login lists
them and asks which on a terminal, and without a terminal exits 4 with the
list. An invitation is accepted by naming or picking its org, never just by
signing in. If you belong to none, login exits 5 and says so. --create-org
NAME makes a new org that you own and signs you in to it, also when you belong
to others already. --org with an org you are no member of asks to join it
(GitHub only) and exits 5 until an admin of that org lets you in.

Either way the new secret is saved in the config file and not printed, so it
stays out of terminal scrollback and pasted output; the line on stderr names
its prefix. A script that needs the secret uses --json, which prints the token
object with it.

With --token <secret> nothing is exchanged: the secret and the server URL are
written to the config file and no password is asked for. Combine it with
LORVIK_CONFIG to provision an agent's own file:

  LORVIK_CONFIG=~/.config/lorvik/claude-code.toml lorvik login --token <secret>

A chat of a harness lorvik recognises needs no file of its own: it acts as
the harness's own agent, made and renewed from this login (lorvik help config).

The config file is written with mode 0600.

A server named only by the repo's .lorvik.toml, and not the one saved in
your config, could be anyone's: login asks before signing in to it, and
without a terminal refuses (exit 5) unless --api-url names it.

Examples:
  lorvik login
  lorvik login --org 106666050296328877
  lorvik login --create-org "Acme"
  LORVIK_PASSWORD=... lorvik login ana --org 106666050296328877`,
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("create-org") {
				if cmd.Flags().Changed("token") || in.Org != "" {
					return cmdutil.Usagef("--create-org makes a new org; do not combine it with --org or --token")
				}
				if createOrg = strings.TrimSpace(createOrg); createOrg == "" {
					return cmdutil.Usagef("--create-org needs a name")
				}
				in.Choose = true
			}
			if err := a.trustBoundServer(cmd.Context()); err != nil {
				return err
			}
			if cmd.Flags().Changed("token") {
				return a.storeToken(token, in, args)
			}
			if len(args) > 0 {
				in.Login = args[0]
			}
			var res apitypes.TokenCreated
			var s Settings
			var err error
			if strings.TrimSpace(in.Login) == "" && !cmd.Flags().Changed("password") {
				res, s, err = a.githubLogin(cmd.Context(), in, createOrg)
			} else {
				if strings.TrimSpace(in.Login) == "" {
					return cmdutil.Usagef("a login is required: lorvik login <login>")
				}
				if in.Password, err = a.readPassword(); err != nil {
					return err
				}
				res, s, err = a.passwordLogin(cmd.Context(), in, createOrg)
			}
			if err != nil {
				return err
			}
			if a.asJSON {
				return cmdutil.WriteJSON(a.out(), res)
			}
			fmt.Fprintf(a.errOut(), "lorvik: signed in to %s; token %s saved to %s\n", s.APIURL, res.Token.Prefix, userConfigPath(a.getenv))
			a.noHolding = true
			return nil
		},
	}
	f := cmd.Flags()
	f.Bool("password", false, "Authenticate with a password instead of GitHub")
	f.StringVar(&in.Login, "login", "", "Login name (or give it as the argument)")
	f.StringVar(&in.Org, "org", "", "Org id to sign in to, when you belong to several orgs or an org has invited you")
	f.StringVar(&createOrg, "create-org", "", "Create an org with this name, owned by you, and sign in to it")
	f.StringVar(&token, "token", "", "Store this token in the config file instead of signing in")
	f.StringVar(&in.TokenName, "token-name", "", "Label for the token")
	return cmd
}

// trustBoundServer stands before every sign-in. A server named only by the
// repo's .lorvik.toml, and not your saved one, is the repo's choice: a cloned
// repo could collect your password or a GitHub token, and login would save it
// as the server every later command talks to. On a terminal you confirm it;
// without one, --api-url or LORVIK_API_URL must name it.
func (a *app) trustBoundServer(ctx context.Context) error {
	s, err := a.settings()
	if err != nil {
		return err
	}
	if s.Binding == "" || s.APIURLFrom != s.Binding || sameServer(s.APIURL, s.TokenServer) {
		return nil
	}
	if !a.interactive() {
		return &cmdutil.ExitError{
			Code: cmdutil.ExitUnauthorized,
			Err:  fmt.Errorf("%s names %s, not your saved server %s; nothing sent. If you trust it, sign in with --api-url %s", s.Binding, s.APIURL, s.TokenServer, s.APIURL),
		}
	}
	ok, err := a.prompter().Confirm(ctx, cmdutil.Question{Title: fmt.Sprintf("This repo's %s (%s) names %s. Sign in to it?", bindingFile, a.tilde(s.Binding), s.APIURL)}, false)
	if err != nil {
		return err
	}
	if !ok {
		return cmdutil.ErrCancelled
	}
	return nil
}

// passwordLogin exchanges a login and password for a token and saves it, with
// the server URL, in the user config. It drops any cached client so the next
// command picks up the new token. Callers print the outcome.
func (a *app) passwordLogin(ctx context.Context, in apitypes.Login, createOrg string) (apitypes.TokenCreated, Settings, error) {
	c, err := a.anonClient()
	if err != nil {
		return apitypes.TokenCreated{}, Settings{}, err
	}
	res, err := c.Login(ctx, in)
	if err != nil {
		return apitypes.TokenCreated{}, Settings{}, err
	}
	return a.finishLogin(ctx, c, res, in.TokenName, createOrg)
}

// saveLogin writes a minted token and the server URL to the user config and
// drops the cached client.
func (a *app) saveLogin(res apitypes.TokenCreated) (apitypes.TokenCreated, Settings, error) {
	s, err := a.settings()
	if err != nil {
		return apitypes.TokenCreated{}, Settings{}, err
	}
	if err := UpdateUserConfig(a.getenv, map[string]string{"api_url": s.APIURL, "token": res.Secret}); err != nil {
		return apitypes.TokenCreated{}, Settings{}, fmt.Errorf("signed in but could not save the token: %w", err)
	}
	a.cl = nil
	return res, s, nil
}

// storeToken writes a given secret and the server URL to the config file
// without contacting the server. Sign-in arguments make no sense with it.
func (a *app) storeToken(secret string, in apitypes.Login, args []string) error {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return cmdutil.Usagef("--token needs a secret")
	}
	if len(args) > 0 || in.Login != "" || in.Org != "" || in.TokenName != "" {
		return cmdutil.Usagef("--token stores a secret as it is; do not combine it with a login, --org or --token-name")
	}
	s, err := a.settings()
	if err != nil {
		return err
	}
	if err := UpdateUserConfig(a.getenv, map[string]string{"api_url": s.APIURL, "token": secret}); err != nil {
		return fmt.Errorf("could not save the token: %w", err)
	}
	path := userConfigPath(a.getenv)
	if a.asJSON {
		return cmdutil.WriteJSON(a.out(), map[string]string{"api_url": s.APIURL, "config": path})
	}
	fmt.Fprintf(a.errOut(), "lorvik: token %s saved to %s for %s\n", tokenPrefix(secret), path, s.APIURL)
	return nil
}

// readPassword takes the password from LORVIK_PASSWORD, else prompts on a
// terminal. It never reads a non-terminal stdin, so a script cannot hang or
// feed a password by accident.
func (a *app) readPassword() (string, error) {
	if pw := a.getenv("LORVIK_PASSWORD"); pw != "" {
		return pw, nil
	}
	s := a.f.IOStreams
	if !s.IsStdinTTY() {
		return "", cmdutil.Usagef("no password: set LORVIK_PASSWORD, or run lorvik login on a terminal")
	}
	fmt.Fprint(a.errOut(), "Password: ")
	var pw string
	var err error
	if s.ReadPassword != nil {
		pw, err = s.ReadPassword()
	} else {
		pw, err = bufio.NewReader(s.In).ReadString('\n')
		pw = strings.TrimRight(pw, "\r\n")
	}
	fmt.Fprintln(a.errOut())
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if pw == "" {
		return "", cmdutil.Usagef("empty password")
	}
	return pw, nil
}
