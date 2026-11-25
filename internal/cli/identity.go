package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

// A chat of a harness the CLI recognises acts as that harness's own agent on
// this machine, never as you. The first command registers the agent from
// your CLI login; later ones renew its one-hour token through the same
// login. Agent id, token and inbox cursors live in a file of the agent's
// own under agents/ beside your config. LORVIK_CONFIG or LORVIK_TOKEN, when
// set, win as before.

const (
	agentsDir = "agents"
	// renewBefore is how close to expiry an agent token is renewed, so no
	// command starts on a token about to lapse.
	renewBefore = 10 * time.Minute
	// maxAgentName is the server's limit on an agent's name.
	maxAgentName = 64
)

// agentIdentity is the harness's own agent and the login that renews it.
type agentIdentity struct {
	Harness string // the server's name for the harness
	Machine string
	Name    string // <harness>@<machine>
	Path    string // the agent's file
	ID      string // empty until registered
	Expires time.Time
	// Joined lists the projects the agent was found to be in since its token
	// was last renewed, so each is asked about once an hour at most.
	Joined []string
	// LoginToken and LoginServer are your own sign-in; they only register
	// the agent and renew its token.
	LoginToken  string
	LoginServer string
}

// serverHarness maps a harness name the CLI detects to the server's.
func serverHarness(name string) string {
	if name == "copilot" {
		return "copilot-cli"
	}
	return name
}

// detectedHarness is the harness whose session variable is set, or "".
func (a *app) detectedHarness() string {
	for _, h := range sessionHarnesses {
		if strings.TrimSpace(a.getenv(h.env)) != "" {
			return serverHarness(h.name)
		}
	}
	return ""
}

// harnessIdentity is the agent this chat acts as: set for a recognised
// harness unless LORVIK_CONFIG or LORVIK_TOKEN chooses the credential.
func (a *app) harnessIdentity() (agentIdentity, bool) {
	if a.getenv("LORVIK_CONFIG") != "" || a.getenv("LORVIK_TOKEN") != "" {
		return agentIdentity{}, false
	}
	h := a.detectedHarness()
	if h == "" {
		return agentIdentity{}, false
	}
	machine := a.hostname()
	name := agentName(h, machine)
	return agentIdentity{Harness: h, Machine: machine, Name: name,
		Path: filepath.Join(filepath.Dir(userConfigPath(a.getenv)), agentsDir, name+".toml")}, true
}

// agentName is <harness>@<machine>, the machine cut to fit the server's
// limit and kept to one path segment.
func agentName(harness, machine string) string {
	machine = strings.NewReplacer("/", "-", `\`, "-").Replace(machine)
	name := harness + "@" + machine
	return name[:min(len(name), maxAgentName)]
}

// asAgent replaces your own credential with the agent's from its file and
// keeps yours only for renewal. A file written while you were signed in to
// another server holds nothing usable here.
func (s Settings) asAgent(id agentIdentity) (Settings, error) {
	file, err := readTOMLFile(id.Path)
	if err != nil {
		return Settings{}, err
	}
	id.LoginToken, id.LoginServer = s.Token, s.TokenServer
	s.Token = ""
	if file["api_url"] != "" && sameServer(file["api_url"], id.LoginServer) {
		s.Token, id.ID, id.Joined = file["token"], file["agent_id"], splitList(file["joined"])
		id.Expires, _ = time.Parse(time.RFC3339, file["token_expires_at"])
	}
	s.ConfigPath, s.Agent = id.Path, &id
	return s, nil
}

// agentToken makes sure the agent has a token that outlives renewBefore,
// registering the agent or renewing its token through your login. It works
// under the file's lock and looks again once it holds it, so chats starting
// together register one agent and mint one token. refused, when set, is a
// token the server turned down, renewed whatever this machine's clock says.
func (a *app) agentToken(s Settings, refused string) (Settings, error) {
	id := s.Agent
	// The agent lives on your login: signed out, it stops at once rather than
	// running on until its cached token lapses.
	if id.LoginToken == "" {
		return s, &cmdutil.ExitError{Code: cmdutil.ExitUnauthorized, Err: fmt.Errorf(
			"this %s chat acts as its own agent %s, made from your lorvik login, and nobody is signed in on this machine; run `lorvik login` in a terminal, then retry. It never acts as you", id.Harness, id.Name)}
	}
	now := a.f.Time()
	if s.Token != "" && s.Token != refused && now.Add(renewBefore).Before(id.Expires) {
		return s, nil
	}
	var fresh map[string]string
	err := editConfig(id.Path, func(cur map[string]string) (map[string]string, error) {
		if exp, _ := time.Parse(time.RFC3339, cur["token_expires_at"]); sameServer(cur["api_url"], id.LoginServer) && cur["token"] != "" && cur["token"] != refused && now.Add(renewBefore).Before(exp) {
			fresh = cur
			return nil, nil
		}
		stale := cur
		if !sameServer(cur["api_url"], id.LoginServer) {
			cur = map[string]string{}
		}
		updates, err := a.mintAgentToken(cur, *id, now)
		if err != nil {
			return nil, err
		}
		for k := range stale {
			if _, set := updates[k]; !set && cur[k] == "" {
				updates[k] = ""
			}
		}
		fresh = updates
		return updates, nil
	})
	if err != nil {
		return s, err
	}
	s.Token, id.ID, id.Joined = fresh["token"], fresh["agent_id"], splitList(fresh["joined"])
	id.Expires, _ = time.Parse(time.RFC3339, fresh["token_expires_at"])
	return s, nil
}

// renewAgent renews the harness agent's token on c once it nears expiry, for
// a command that outlives the token, such as inbox --follow.
func (a *app) renewAgent(c *client.Client) error {
	id := a.agent
	if id == nil || a.f.Time().Add(renewBefore).Before(id.Expires) {
		return nil
	}
	s, err := a.agentToken(Settings{Token: c.Token(), Agent: id}, "")
	if err != nil {
		return err
	}
	c.SetToken(s.Token)
	return nil
}

// renewRefused is the client's Renew for a harness agent: a token the server
// refused is replaced even when this machine's clock, perhaps behind the
// server's, says it is fresh.
func (a *app) renewRefused(_ context.Context, refused string) (string, error) {
	s, err := a.agentToken(Settings{Agent: a.agent}, refused)
	return s.Token, err
}

// mintAgentToken registers the agent when its file names none, else mints
// it a new token, and returns the file's new keys. The inbox cursors move to
// the new token, and the projects found are asked about again.
func (a *app) mintAgentToken(cur map[string]string, id agentIdentity, now time.Time) (map[string]string, error) {
	hc, err := client.New(id.LoginServer, id.LoginToken)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	agentID := cur["agent_id"]
	var secret string
	var expires time.Time
	if agentID != "" {
		res, err := hc.CreateToken(ctx, apitypes.CreateToken{Agent: agentID, Name: id.Name})
		switch {
		case err == nil:
			secret, expires = res.Secret, res.Token.ExpiresAt
		case isNotFound(err):
			// The server no longer knows the agent (it was never revoked, or
			// that would have said so): register it again.
			agentID = ""
		default:
			return nil, err
		}
	}
	if agentID == "" {
		res, err := hc.CreateAgent(ctx, apitypes.CreateAgent{Name: id.Name, Harness: id.Harness, Machine: id.Machine, Auto: true})
		switch {
		case err == nil:
			agentID, secret, expires = res.Agent.ID, res.Token, res.TokenExpiresAt
			fmt.Fprintf(a.errOut(), "lorvik: registered agent %s for %s chats on this machine, from your login\n", id.Name, id.Harness)
		case isConflict(err):
			if agentID, err = yourAgent(ctx, hc, id.Name, err); err != nil {
				return nil, err
			}
		default:
			return nil, err
		}
	}
	if secret == "" {
		res, err := hc.CreateToken(ctx, apitypes.CreateToken{Agent: agentID, Name: id.Name})
		if err != nil {
			return nil, err
		}
		secret, expires = res.Secret, res.Token.ExpiresAt
	}
	updates := rotatedCursors(cur, cur["token"], secret, "", "", now)
	updates["api_url"], updates["agent_id"], updates["agent"], updates["harness"] = id.LoginServer, agentID, id.Name, id.Harness
	updates["token_expires_at"] = expires.UTC().Format(time.RFC3339)
	updates["joined"] = ""
	return updates, nil
}

// yourAgent finds the agent named name that you registered, after
// registration found the name taken: this machine lost the agent's file. A
// revoked agent of yours stays revoked until you restore or remove it, so
// registering's own refusal stands.
func yourAgent(ctx context.Context, hc *client.Client, name string, taken error) (string, error) {
	me, err := hc.Me(ctx)
	if err != nil {
		return "", err
	}
	agents, err := allPages(func(pg client.PageParams) ([]apitypes.Agent, apitypes.Page, error) {
		l, err := hc.ListAgents(ctx, false, pg)
		return l.Items, l.Page, err
	})
	if err != nil {
		return "", err
	}
	for _, ag := range agents {
		if ag.Name == name && ag.CreatedBy == me.Actor.ID {
			return ag.ID, nil
		}
	}
	if ae, ok := errors.AsType[*client.APIError](taken); ok && strings.Contains(ae.Detail, "revoked") {
		return "", taken
	}
	return "", &cmdutil.ExitError{Code: cmdutil.ExitConflict, Err: fmt.Errorf(
		"another user's agent is named %s; set LORVIK_CONFIG to the config file of an agent of your own", name)}
}

// joinProject asks the server, once per project and token, to let the
// harness's agent into the project it is about to work in. Allowed by your
// settings it joins and says so; otherwise the command stops with the
// server's message until you allow it on the dashboard.
func (a *app) joinProject(c *client.Client, slug string) error {
	id := a.agent
	if id == nil || slices.Contains(id.Joined, slug) {
		return nil
	}
	res, err := c.JoinProject(context.Background(), slug)
	if err != nil {
		return err
	}
	if res.Status == "pending" {
		return &cmdutil.ExitError{Code: cmdutil.ExitUnauthorized, Err: fmt.Errorf("%s; run the command again once allowed", res.Message)}
	}
	if res.Status == "joined" {
		fmt.Fprintf(a.errOut(), "lorvik: %s\n", res.Message)
	}
	id.Joined = append(id.Joined, slug)
	return editConfig(id.Path, func(cur map[string]string) (map[string]string, error) {
		joined := splitList(cur["joined"])
		if slices.Contains(joined, slug) {
			return nil, nil
		}
		return map[string]string{"joined": strings.Join(append(joined, slug), ",")}, nil
	})
}

// warnBorrowedConfig says so once when LORVIK_CONFIG names the file of an
// agent of another harness than the one this chat runs in: the chat would
// act on the board as that agent.
func (a *app) warnBorrowedConfig() {
	path := a.getenv("LORVIK_CONFIG")
	h := a.detectedHarness()
	if path == "" || h == "" {
		return
	}
	file, err := readTOMLFile(path)
	if err != nil || file["harness"] == "" || file["harness"] == h {
		return
	}
	fmt.Fprintf(a.errOut(), "lorvik: this chat runs in %s, but LORVIK_CONFIG names the file of %s agent %s; unset LORVIK_CONFIG so the chat acts as its own agent\n",
		h, file["harness"], orElse(file["agent"], path))
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// isConflict reports a 409 from the server.
func isConflict(err error) bool {
	ae, ok := errors.AsType[*client.APIError](err)
	return ok && ae.Status == 409
}
