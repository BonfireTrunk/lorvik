package cli

import "github.com/spf13/cobra"

// topics returns the help topics: commands with no Run, which cobra lists
// apart from runnable commands and prints with 'lorvik help <topic>'.
// bold styles a heading, and is the identity when colour is off.
func topics(bold func(string) string) []*cobra.Command {
	return []*cobra.Command{
		{
			Use:   "config",
			Short: "Flags, environment variables and files, in order of precedence",
			Long:  configTopic(bold),
		},
		{
			Use:   "scripting",
			Short: "Stdout and stderr, --json, ids, paging, unread line, exit codes",
			Long:  scriptingTopic(bold),
		},
	}
}

func configTopic(bold func(string) string) string {
	return `Settings resolve in this order: flags, then environment, then files.

` + bold("Environment") + `
  LORVIK_API_URL    server base URL (default http://localhost:8080)
  LORVIK_PROJECT    project slug
  LORVIK_CONFIG     path of the user config file, so an agent on the same
                    machine user keeps its own; it turns off the harness
                    agent below
  LORVIK_SESSION    session label override (see lorvik session)
  LORVIK_TOKEN      API token for CI and unattended runs; people sign in
                    with lorvik login instead. It turns off the harness
                    agent below
  LORVIK_PASSWORD   password for lorvik login without a prompt
  NO_COLOR          turn off colour

` + bold("Files") + `
  $LORVIK_CONFIG, else $XDG_CONFIG_HOME/lorvik/config.toml
                    api_url, token and your inbox cursor (mode 0600)
  .lorvik.toml      project and api_url for a repository, written by
                    lorvik init; found by walking up to the repo root.
                    Its api_url never receives your token unless it is the
                    server the token is for
  agents/<harness>@<machine>.toml beside the user config
                    a harness agent's id, token and inbox cursor (mode 0600)

` + bold("Harness chats") + `
  In a chat of a harness lorvik recognises (Claude Code, Codex, OpenCode,
  pi, Qwen Code, Copilot CLI, dsh, Goose), commands act as that harness's
  own agent on this machine, <harness>@<machine>, never as you. The first
  command registers it from your lorvik login; its token lives an hour and
  is renewed from the same login. Each project it works in is asked about
  once: your settings let it in, or it waits until you allow it on the
  dashboard (exit 5). With nobody signed in it stops (exit 5).
`
}

func scriptingTopic(bold func(string) string) string {
	return `Stdout is data and stderr is diagnostics.

` + bold("Output") + `
  --json    add to any command for the raw API shape
  ids       are strings
  actors    are {kind, id}; names come from 'lorvik actors'
  lists     are capped at 10; raise with --limit or continue with --cursor

` + bold("Unread line") + `
  When others have acted since your last 'lorvik inbox', a line
  "N unread, run lorvik inbox" appears on stderr.

` + bold("Held cards") + `
  While this chat holds cards, every command ends its stdout with one line
  per card: "holding  #42  demo  Fix login  12m left", the soonest to end
  first, then "holding  3 more" past eight. Drop them with
  grep -v '^holding  '. With --json there are no such lines: an object gains
  a last field "holding" ({cards: [{project, number, title, expires_at}],
  more}); an array is printed unchanged. A page's source (--page), a secret
  shown once ('login', 'token create', 'agent register'), 'session',
  'lock run', 'watch' and 'inbox' (which lists the claims itself) print none.

` + bold("Exit codes") + `
  0  ok
  1  error
  2  usage
  3  not found
  4  conflict
  5  unauthorized
`
}
