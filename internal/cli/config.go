package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

const (
	defaultAPIURL = "http://localhost:8080"
	bindingFile   = ".lorvik.toml"
)

// Flags are the values given on the command line. Empty means not given.
type Flags struct {
	APIURL  string
	Project string
}

// Settings are the resolved connection settings.
type Settings struct {
	APIURL  string
	Token   string
	Project string
	// TokenFromEnv is true when Token came from LORVIK_TOKEN rather than the
	// user config, which decides where a rotated token can be stored.
	TokenFromEnv bool
	// TokenServer is the server Token may be sent to: the api_url saved beside
	// it in the user config, else the URL from the flag, LORVIK_API_URL or the
	// default. For LORVIK_TOKEN the flag and LORVIK_API_URL come first. A
	// repo's .lorvik.toml never sets it, since a cloned repo is not trusted
	// with your credential.
	TokenServer string
	// APIURLFrom names where APIURL came from, for messages.
	APIURLFrom string
	// Binding is the .lorvik.toml that supplied values, if any.
	Binding string
	// ConfigPath is the file Token and the inbox cursors live in: the user
	// config, or the file of this harness's own agent.
	ConfigPath string
	// Agent is set when this chat acts as its harness's own agent.
	Agent *agentIdentity
}

// Resolve applies flag > env > file > default. The files are the user config
// (api_url, token) and the .lorvik.toml found by walking up from cwd
// (project, api_url); the repo binding wins over the user config for api_url
// because it is closer to the work.
func Resolve(fl Flags, getenv func(string) string, cwd string) (Settings, error) {
	user, err := readTOMLFile(userConfigPath(getenv))
	if err != nil {
		return Settings{}, err
	}
	bindPath, bound, err := findBinding(cwd)
	if err != nil {
		return Settings{}, err
	}
	envToken := getenv("LORVIK_TOKEN")
	envURL := getenv("LORVIK_API_URL")
	s := Settings{
		APIURL:       first(fl.APIURL, envURL, bound["api_url"], user["api_url"], defaultAPIURL),
		Token:        first(envToken, user["token"]),
		Project:      first(fl.Project, getenv("LORVIK_PROJECT"), bound["project"]),
		TokenFromEnv: envToken != "",
		TokenServer:  first(user["api_url"], fl.APIURL, envURL, defaultAPIURL),
		Binding:      bindPath,
	}
	if s.TokenFromEnv {
		s.TokenServer = first(fl.APIURL, envURL, user["api_url"], defaultAPIURL)
	}
	switch {
	case fl.APIURL != "":
		s.APIURLFrom = "--api-url"
	case envURL != "":
		s.APIURLFrom = "LORVIK_API_URL"
	case bound["api_url"] != "":
		s.APIURLFrom = bindPath
	case user["api_url"] != "":
		s.APIURLFrom = userConfigPath(getenv)
	default:
		s.APIURLFrom = "the default"
	}
	return s, nil
}

// credential returns the token to send to APIURL. A token goes only to the
// server it is for: a .lorvik.toml in a cloned repo naming another server
// would otherwise collect it.
func (s Settings) credential() (string, error) {
	if s.Token == "" {
		return "", nil
	}
	if sameServer(s.APIURL, s.TokenServer) {
		return s.Token, nil
	}
	fix := fmt.Sprintf("to use that server, sign in to it with lorvik login --api-url %s, which replaces the saved token", s.APIURL)
	if s.TokenFromEnv {
		fix = "set LORVIK_API_URL to the server LORVIK_TOKEN is for"
	}
	return "", &cmdutil.ExitError{
		Code: cmdutil.ExitUnauthorized,
		Err:  fmt.Errorf("%s points at %s, but your token is for %s; no credential sent; %s", s.APIURLFrom, s.APIURL, s.TokenServer, fix),
	}
}

// sameServer reports whether two server URLs share scheme, host and port.
func sameServer(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	return err1 == nil && err2 == nil && client.SameOrigin(ua, ub)
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// userConfigPath is $LORVIK_CONFIG when set, else
// $XDG_CONFIG_HOME/lorvik/config.toml, falling back to
// ~/.config/lorvik/config.toml.
func userConfigPath(getenv func(string) string) string {
	if p := getenv("LORVIK_CONFIG"); p != "" {
		return p
	}
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "lorvik", "config.toml")
	}
	home := getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, ".config", "lorvik", "config.toml")
}

// readTOMLFile returns an empty map when the file does not exist.
func readTOMLFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()
	m, err := parseTOML(f)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, nil
}

// isGitRoot reports whether dir is the top of a git working tree: its .git is
// a directory holding HEAD, or a file (the pointer a worktree or a submodule
// has in its place). A .git directory without HEAD is not a repository to
// git either; a stray empty /tmp/.git once made every folder under /tmp
// count as one repository.
func isGitRoot(dir string) bool {
	git := filepath.Join(dir, ".git")
	fi, err := os.Stat(git)
	if err != nil {
		return false
	}
	if !fi.IsDir() {
		return fi.Mode().IsRegular()
	}
	_, err = os.Stat(filepath.Join(git, "HEAD"))
	return err == nil
}

// findBinding walks up from dir to the git root (isGitRoot) or the
// filesystem root, returning the first .lorvik.toml it finds.
func findBinding(dir string) (string, map[string]string, error) {
	for {
		p := filepath.Join(dir, bindingFile)
		if _, err := os.Stat(p); err == nil {
			m, err := readTOMLFile(p)
			return p, m, err
		}
		if isGitRoot(dir) {
			return "", map[string]string{}, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", map[string]string{}, nil
		}
		dir = parent
	}
}

// UpdateUserConfig sets keys in the user config file, keeping every other
// line (comments, tables, unknown keys) as it is. An empty value removes the
// key. New keys go before the first table so they stay top-level. The file is
// replaced atomically with mode 0600 and the directory created with 0700.
func UpdateUserConfig(getenv func(string) string, updates map[string]string) error {
	return editUserConfig(getenv, func(map[string]string) (map[string]string, error) { return updates, nil })
}

// editUserConfig is UpdateUserConfig with updates computed from the file's
// current keys. The read, the change and the replace run under an exclusive
// lock beside the file: chats sharing one config would otherwise each write
// back what they read, and the later write would undo a cursor save or a
// rotated token of the earlier one.
func editUserConfig(getenv func(string) string, change func(current map[string]string) (map[string]string, error)) error {
	return editConfig(userConfigPath(getenv), change)
}

// editConfig is editUserConfig for the config file at path.
func editConfig(path string, change func(current map[string]string) (map[string]string, error)) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	unlock, err := lockFile(path + ".lock")
	if err != nil {
		return fmt.Errorf("lock config: %w", err)
	}
	defer unlock()
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read config: %w", err)
	}
	current, err := parseTOML(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	updates, err := change(current)
	if err != nil {
		return err
	}
	var lines []string
	if len(raw) > 0 {
		lines = strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	}
	done := map[string]bool{}
	var out []string
	inTable := false
	firstTable := -1
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			if !inTable {
				firstTable = len(out)
			}
			inTable = true
		}
		if !inTable && t != "" && t[0] != '#' {
			if key, _, ok := strings.Cut(t, "="); ok {
				key = strings.TrimSpace(key)
				if v, set := updates[key]; set {
					done[key] = true
					if v != "" {
						out = append(out, key+" = "+strconv.Quote(v))
					}
					continue
				}
			}
		}
		out = append(out, line)
	}
	var added []string
	for _, key := range slices.Sorted(maps.Keys(updates)) {
		if !done[key] && updates[key] != "" {
			added = append(added, key+" = "+strconv.Quote(updates[key]))
		}
	}
	if firstTable < 0 {
		firstTable = len(out)
	}
	out = slices.Insert(out, firstTable, added...)
	body := ""
	if len(out) > 0 {
		body = strings.Join(out, "\n") + "\n"
	}
	return writeFileAtomic(path, []byte(body))
}

// writeFileAtomic writes data to a temp file beside path and renames it, so a
// concurrent reader never sees a half-written file. The directory exists:
// the caller creates it before locking.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}
