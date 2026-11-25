package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

// finishLogin ends a sign-in. An ordinary token is saved as it is. A sign-in
// token belongs to no org: it is spent here on creating or choosing an org,
// and only the org's own token is saved. The sign-in secret is never written
// or printed.
func (a *app) finishLogin(ctx context.Context, c *client.Client, res apitypes.TokenCreated, tokenName, createOrg string) (apitypes.TokenCreated, Settings, error) {
	if res.Token.Scope != apitypes.ScopeSignIn {
		return a.saveLogin(res)
	}
	c.SetToken(res.Secret)
	var id string
	var err error
	if createOrg != "" {
		id, err = a.createOrg(ctx, c, createOrg)
	} else {
		id, err = a.chooseOrg(ctx, c)
	}
	if err != nil {
		return apitypes.TokenCreated{}, Settings{}, err
	}
	res, err = c.EnterOrg(ctx, id, apitypes.EnterOrg{TokenName: tokenName})
	if err != nil {
		return apitypes.TokenCreated{}, Settings{}, err
	}
	return a.saveLogin(res)
}

func (a *app) createOrg(ctx context.Context, c *client.Client, name string) (string, error) {
	org, err := c.CreateOrg(ctx, apitypes.CreateOrg{Name: name})
	if err != nil {
		return "", err
	}
	fmt.Fprintf(a.errOut(), "lorvik: created org %s (%s)\n", org.Name, org.ID)
	return org.ID, nil
}

// chooseOrg lists the orgs the person can enter and picks one: the answer on
// a terminal, else the only one when the person is a member of it, else it
// exits 4 with the ids for --org. An invitation is never entered on the
// person's behalf: accepting it is theirs to do, by answering or by --org.
func (a *app) chooseOrg(ctx context.Context, c *client.Client) (string, error) {
	all, err := c.SignInOrgs(ctx)
	if err != nil {
		return "", err
	}
	var enterable []apitypes.SignInOrg
	var waiting []string
	for _, o := range all.Orgs {
		switch o.Status {
		case "member", "invited":
			enterable = append(enterable, o)
		case "requested":
			waiting = append(waiting, fmt.Sprintf("your request to join %s is waiting for an admin", o.Name))
		}
	}
	if len(enterable) == 0 {
		s, err := a.settings()
		if err != nil {
			return "", err
		}
		msg := fmt.Sprintf("you belong to no org on %s; create one with \"lorvik login --create-org NAME\", or ask an admin of an org to invite your GitHub username", s.APIURL)
		if len(waiting) > 0 {
			msg += "\n" + strings.Join(waiting, "\n")
		}
		return "", &cmdutil.ExitError{Code: cmdutil.ExitUnauthorized, Err: errors.New(msg)}
	}
	if a.interactive() {
		return a.askOrg(enterable)
	}
	if len(enterable) == 1 && enterable[0].Status == "member" {
		return enterable[0].ID, nil
	}
	var b strings.Builder
	if slices.ContainsFunc(enterable, func(o apitypes.SignInOrg) bool { return o.Status == "invited" }) {
		b.WriteString("pass --org <id> to choose the org; naming one you are invited to accepts the invitation")
	} else {
		b.WriteString("you belong to several orgs; pass --org <id>")
	}
	for _, o := range enterable {
		role := o.Role
		if o.Status == "invited" {
			role += ", invited"
		}
		fmt.Fprintf(&b, "\n%s  %s  %s", o.ID, o.Name, role)
	}
	return "", &cmdutil.ExitError{Code: cmdutil.ExitConflict, Err: errors.New(b.String())}
}

// askOrg prints the orgs numbered and reads the number of one from stdin.
func (a *app) askOrg(orgs []apitypes.SignInOrg) (string, error) {
	w := a.errOut()
	for i, o := range orgs {
		role := o.Role
		if o.Status == "invited" {
			role = "invited as " + o.Role
		}
		fmt.Fprintf(w, "  %d  %s  %s\n", i+1, o.Name, role)
	}
	fmt.Fprintf(w, "Org [1-%d]: ", len(orgs))
	line, err := bufio.NewReader(a.f.IOStreams.In).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read the answer: %w", err)
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(line))
	if convErr != nil || n < 1 || n > len(orgs) {
		return "", cmdutil.Usagef("answer with a number from 1 to %d", len(orgs))
	}
	return orgs[n-1].ID, nil
}
