package cli

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

// The outbox holds the writes a command could not send because the server was
// unreachable: comment, add and done. Each entry keeps the command's own
// inputs, not a built request, so sending it later runs the code the command
// runs online. The entry's id is the request key of its first attempt and of
// every later send, so a write the server did receive is never made twice.
// A queue file belongs to one server and one project and never holds a token.

const (
	outboxDir  = "outbox"
	cmdComment = "comment"
	cmdAdd     = "add"
	cmdDone    = "done"
)

// cardInput is what add takes from its flags, as the entry stores it.
type cardInput struct {
	Kind       string   `json:"kind,omitempty"`
	Title      string   `json:"title,omitempty"`
	Body       string   `json:"body,omitempty"`
	Page       string   `json:"page,omitempty"`
	Priority   string   `json:"priority,omitempty"`
	Lane       string   `json:"lane,omitempty"`
	Parent     *int64   `json:"parent,omitempty"`
	Supersedes *int64   `json:"supersedes,omitempty"`
	Ready      bool     `json:"ready,omitempty"`
	Assignee   *string  `json:"assignee,omitempty"`
	Choices    []string `json:"choices,omitempty"`
	Blocks     []int64  `json:"blocks,omitempty"`
	ForPerson  bool     `json:"for_person,omitempty"`
}

// wire is the request body without the assignee, which needs the server.
func (in cardInput) wire() apitypes.CreateCard {
	req := apitypes.CreateCard{
		Kind: in.Kind, Title: in.Title, Body: in.Body, Page: in.Page, Priority: in.Priority,
		Choices: in.Choices, Blocks: in.Blocks, Lane: in.Lane, Parent: in.Parent, Supersedes: in.Supersedes,
		ForPerson: in.ForPerson,
	}
	if in.Ready {
		req.Status = "ready"
	}
	return req
}

type outboxEntry struct {
	ID      string    `json:"id"`
	At      time.Time `json:"at"`
	Server  string    `json:"server"`
	Project string    `json:"project"`
	Owner   string    `json:"owner"`
	Session string    `json:"session"`
	Command string    `json:"command"`
	// Card and Text are a comment's inputs, Card and the next three a done's.
	Card             int64  `json:"card,omitempty"`
	Text             string `json:"text,omitempty"`
	Note             string `json:"note,omitempty"`
	Into             string `json:"into,omitempty"`
	NoContractChange bool   `json:"no_contract_change,omitempty"`
	cardInput
	Refused *outboxRefusal `json:"refused,omitempty"`
	// raw is a line that is not an entry, kept verbatim so a hand edit is
	// never lost; line is its number in the file.
	raw  string
	line int
	// asRead is an entry's line as it stands in the file. An entry that did
	// not change is written back as it was, so a field a newer CLI wrote
	// survives a rewrite by this one.
	asRead string
}

// outboxRefusal is the server's answer to an entry it will never accept.
type outboxRefusal struct {
	At      time.Time `json:"at"`
	Status  int       `json:"status"`
	Message string    `json:"message"`
}

func (e outboxEntry) what() string {
	switch e.Command {
	case cmdComment:
		return fmt.Sprintf("comment on #%d", e.Card)
	case cmdAdd:
		return fmt.Sprintf("add %q", e.Title)
	}
	return fmt.Sprintf("%s #%d", e.Command, e.Card)
}

type outbox struct{ path string }

// newRequestKey is the id of a write, made before its first attempt.
func newRequestKey() string { return rand.Text() }

// outboxServerDir is the server's directory name: host and port, lowercased,
// every character outside [a-z0-9.-] replaced by an underscore.
func outboxServerDir(server string) (string, error) {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" {
		return "", errors.New("server url: need scheme and host")
	}
	dir := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-' {
			return r
		}
		return '_'
	}, strings.ToLower(u.Host))
	if dir == "." || dir == ".." {
		return "", errors.New("server url: its host cannot name a directory")
	}
	return dir, nil
}

// originOf is scheme://host[:port] of raw: what an entry stores and compares.
// The userinfo, path, query and fragment of a configured URL stay out of the
// queue file and the output.
func originOf(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("server url: need scheme and host")
	}
	return u.Scheme + "://" + u.Host, nil
}

// safeSlug reports whether slug is one path element that stays inside its
// directory.
func safeSlug(slug string) bool {
	if slug == "" || slug == "." || slug == ".." {
		return false
	}
	safe := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_'
	}
	for _, r := range slug {
		if !safe(r) {
			return false
		}
	}
	return true
}

// outboxPath is <state>/lorvik/outbox/<server>/<project>.jsonl, where state is
// $XDG_STATE_HOME or ~/.local/state.
func outboxPath(getenv func(string) string, server, project string) (string, error) {
	if !safeSlug(project) {
		return "", fmt.Errorf("project %q cannot name a queue file", project)
	}
	dir, err := outboxServerDir(server)
	if err != nil {
		return "", err
	}
	state := getenv("XDG_STATE_HOME")
	if state == "" {
		home := getenv("HOME")
		if home == "" {
			if home, err = os.UserHomeDir(); err != nil {
				return "", fmt.Errorf("state directory: %w", err)
			}
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "lorvik", outboxDir, dir, project+".jsonl"), nil
}

func (a *app) outboxAt(server, project string) (outbox, error) {
	p, err := outboxPath(a.getenv, server, project)
	return outbox{path: p}, err
}

// exists costs one stat; it is all a command pays when nothing is queued.
func (o outbox) exists() bool {
	_, err := os.Stat(o.path)
	return err == nil
}

// read returns the entries. A writer replaces the file whole, so a reader
// without the lock sees an old or a new file, never half of one.
func (o outbox) read() ([]outboxEntry, error) {
	f, err := os.Open(o.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read queue: %w", err)
	}
	defer f.Close()
	var out []outboxEntry
	r := bufio.NewReader(f)
	for n := 1; ; n++ {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var e outboxEntry
			text := string(bytes.TrimRight(line, "\r\n"))
			if jerr := json.Unmarshal(line, &e); jerr != nil || e.ID == "" {
				e = outboxEntry{raw: text, line: n}
			} else {
				e.asRead = text
			}
			out = append(out, e)
		}
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read queue: %w", err)
		}
	}
}

// edit reads the entries under the file's lock, lets change rework them, and
// writes the result when change says it changed anything, even when it also
// returns an error: a send that stopped halfway keeps what it did. An empty
// result removes the file.
func (o outbox) edit(change func(es []outboxEntry) (out []outboxEntry, changed bool, err error)) error {
	if err := os.MkdirAll(filepath.Dir(o.path), 0o700); err != nil {
		return fmt.Errorf("create queue dir: %w", err)
	}
	unlock, err := lockFile(o.path + ".lock")
	if err != nil {
		return fmt.Errorf("lock queue: %w", err)
	}
	defer unlock()
	o.sweep()
	es, err := o.read()
	if err != nil {
		return err
	}
	out, changed, cerr := change(es)
	if changed {
		if err := o.write(out); err != nil {
			return errors.Join(cerr, err)
		}
	}
	return cerr
}

// sweep removes the temporary copies of the queue that a killed write left
// behind. The caller holds the lock, under which every write runs, so any
// copy it finds belongs to no running write.
func (o outbox) sweep() {
	dir, prefix := filepath.Dir(o.path), "."+filepath.Base(o.path)+"-"
	names, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, n := range names {
		rest, ok := strings.CutPrefix(n.Name(), prefix)
		if ok && rest != "" && strings.Trim(rest, "0123456789") == "" {
			_ = os.Remove(filepath.Join(dir, n.Name()))
		}
	}
}

func (o outbox) write(es []outboxEntry) error {
	if len(es) == 0 {
		if err := os.Remove(o.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("write queue: %w", err)
		}
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, e := range es {
		if kept := cmp.Or(e.raw, e.asRead); kept != "" {
			buf.WriteString(kept + "\n")
			continue
		}
		if err := enc.Encode(e); err != nil {
			return fmt.Errorf("write queue: %w", err)
		}
	}
	return writeFileAtomic(o.path, buf.Bytes())
}

// serverURL is the API URL the command talks to, as the client has it, and
// without the network: a harness agent's client is built only when needed.
func (a *app) serverURL() (string, error) {
	if a.cl == nil && a.f.Client != nil {
		_, _ = a.client()
	}
	if a.cl != nil {
		return originOf(a.cl.BaseURL())
	}
	s, err := a.settings()
	if err != nil {
		return "", err
	}
	return originOf(s.APIURL)
}

// outboxOwner says whose entries these are, without the network: the harness
// agent's id, else the user config file the token comes from. LORVIK_TOKEN
// names nobody, so its owner is a fingerprint of the token: entries queued
// under one token are never sent under another. Eight hex digits of a hash
// tell tokens apart and give nothing of the token away.
func (a *app) outboxOwner() (string, bool) {
	s, err := a.settings()
	if err != nil {
		return "", false
	}
	if s.Agent != nil {
		id := s.Agent
		if a.agent != nil {
			id = a.agent
		}
		if id.ID == "" {
			return "", false
		}
		return "agent:" + id.ID, true
	}
	if s.TokenFromEnv {
		sum := sha256.Sum256([]byte(s.Token))
		return "env:" + hex.EncodeToString(sum[:4]), true
	}
	p, err := filepath.Abs(s.ConfigPath)
	if err != nil {
		return "", false
	}
	return "config:" + p, true
}

// unreachable reports an error that says the server could not be reached: a
// transport failure or a gateway answer.
func unreachable(err error) bool {
	if _, ok := errors.AsType[*client.ConnError](err); ok {
		return true
	}
	ae, ok := errors.AsType[*client.APIError](err)
	return ok && (ae.Status == 502 || ae.Status == 503 || ae.Status == 504)
}

// queueable is unreachable for a command that was not interrupted: Ctrl-C
// fails the command as it always did.
func queueable(ctx context.Context, err error) bool {
	return ctx.Err() == nil && unreachable(err)
}

type queuedView struct {
	Queued  bool   `json:"queued"`
	ID      string `json:"id"`
	Command string `json:"command"`
	Card    int64  `json:"card,omitempty"`
	Title   string `json:"title,omitempty"`
}

// enqueue stores e, whose command-specific fields and id the caller set, and
// reports it. cause is the unreachable error; when the entry cannot be stored
// the command fails with cause, as it did before queueing existed.
func (a *app) enqueue(e outboxEntry, cause error) error {
	if err := a.store(&e); err != nil {
		return fmt.Errorf("%w (and it could not be queued: %w)", cause, err)
	}
	a.queued, a.noHolding = true, true
	fmt.Fprintf(a.errOut(), "lorvik: cannot reach %s: queued; lorvik sync sends it\n", e.Server)
	if a.asJSON {
		return cmdutil.WriteJSON(a.out(), queuedView{Queued: true, ID: e.ID, Command: e.Command, Card: e.Card, Title: e.Title})
	}
	a.printer().Line("queued  %s  id %s", e.what(), e.ID)
	return nil
}

func (a *app) store(e *outboxEntry) error {
	slug, err := a.slug()
	if err != nil {
		return err
	}
	server, err := a.serverURL()
	if err != nil {
		return err
	}
	owner, ok := a.outboxOwner()
	if !ok {
		return errors.New("cannot tell whose entry it is: the agent has no id yet")
	}
	o, err := a.outboxAt(server, slug)
	if err != nil {
		return err
	}
	e.At, e.Server, e.Project, e.Owner, e.Session = a.f.Time(), server, slug, owner, a.session()
	return o.edit(func(es []outboxEntry) ([]outboxEntry, bool, error) {
		return append(es, *e), true, nil
	})
}

// attempt runs send on the API client and, when the server cannot be
// reached, queues e instead. queued is true when the entry was stored.
func attempt[T any](ctx context.Context, a *app, e outboxEntry, send func(c *client.Client, slug string) (T, error)) (out T, queued bool, err error) {
	c, slug, err := a.api()
	if err == nil {
		// An older queued write the server could not take is still ahead of
		// this one: sending now would overtake it, and a dead server would
		// cost a second timeout. Queue behind it.
		if a.outboxDown != nil {
			err = a.outboxDown
		} else {
			out, err = send(c, slug)
		}
	}
	if err == nil || !queueable(ctx, err) {
		return out, false, err
	}
	return out, true, a.enqueue(e, err)
}

// replay sends one entry by the code its command runs online.
func (a *app) replay(ctx context.Context, c *client.Client, slug string, e outboxEntry) (apitypes.Card, error) {
	switch e.Command {
	case cmdComment:
		_, err := a.postComment(ctx, c, slug, e.Card, e.Text, e.ID)
		return apitypes.Card{}, err
	case cmdAdd:
		return a.postCard(ctx, c, slug, e.cardInput, e.ID)
	case cmdDone:
		fold, err := foldArg(e.Into, e.NoContractChange)
		if err != nil {
			return apitypes.Card{}, err
		}
		return a.doneCard(ctx, c, slug, e.Card, e.Note, fold, e.ID, e.Session, true)
	}
	return apitypes.Card{}, cmdutil.Usagef("queued entry %s has an unknown command %q", e.ID, e.Command)
}

// clientFor is c for an entry of this chat, and a client of its own carrying
// the entry's session label for another chat's. It sends no chat name: the
// server would give the other chat this one's.
func clientFor(c *client.Client, session string) (*client.Client, error) {
	if session == c.Session {
		return c, nil
	}
	cp, err := client.New(c.BaseURL(), c.Token())
	if err != nil {
		return nil, err
	}
	cp.Session = session
	cp.OnToken = func(secret string) {
		c.SetToken(secret)
		if c.OnToken != nil {
			c.OnToken(secret)
		}
	}
	if c.Renew != nil {
		cp.Renew = func(ctx context.Context, refused string) (string, error) {
			secret, err := c.Renew(ctx, refused)
			if err == nil && secret != "" {
				c.SetToken(secret)
			}
			return secret, err
		}
	}
	return cp, nil
}

// refusalOf says whether err is the server refusing the entry for good: a 4xx
// that is not about the credential or the pace, or a usage error from
// resolving the entry's inputs. Anything else stops the send.
func (a *app) refusalOf(err error) (outboxRefusal, bool) {
	if ae, ok := errors.AsType[*client.APIError](err); ok {
		switch {
		case ae.Status < 400 || ae.Status >= 500, ae.Status == 401, ae.Status == 408, ae.Status == 425, ae.Status == 429, staleVersion(err):
			return outboxRefusal{}, false
		}
		msg := ae.Detail
		switch {
		case msg != "":
		case ae.Title != "":
			msg = ae.Title
		case strings.TrimSpace(ae.Body) != "":
			msg = strings.TrimSpace(ae.Body)
		default:
			msg = fmt.Sprintf("server returned %d", ae.Status)
		}
		return outboxRefusal{At: a.f.Time(), Status: ae.Status, Message: msg}, true
	}
	if ue, ok := errors.AsType[*cmdutil.UsageError](err); ok {
		return outboxRefusal{At: a.f.Time(), Message: ue.Msg}, true
	}
	return outboxRefusal{}, false
}

// staleVersion reports a 409 that names the card's current version: the
// write read an old one.
func staleVersion(err error) bool {
	ae, ok := errors.AsType[*client.APIError](err)
	return ok && ae.Status == http.StatusConflict && ae.CurrentVersion > 0
}

// sendOutcome is what became of one entry.
type sendOutcome struct {
	entry   outboxEntry
	number  int64
	refusal *outboxRefusal
}

// stopError is a send that stopped on an entry the server neither took nor
// refused. It names the entry so a poison one can be removed; an entry that
// only met an unreachable server is not one, so it gets no such advice.
type stopError struct {
	entry outboxEntry
	err   error
}

func (s *stopError) Error() string {
	if unreachable(s.err) {
		return fmt.Sprintf("queued %s (id %s) was not sent: %v; it stays queued", s.entry.what(), s.entry.ID, s.err)
	}
	return fmt.Sprintf("queued %s (id %s) was not sent: %v; lorvik sync --drop %s removes it", s.entry.what(), s.entry.ID, s.err, s.entry.ID)
}
func (s *stopError) Unwrap() error { return s.err }

// settle removes the entry id from the file, or marks it refused. The file is
// read again under the lock, so entries other processes appended meanwhile
// stay.
func (o outbox) settle(id string, ref *outboxRefusal) error {
	return o.edit(func(es []outboxEntry) ([]outboxEntry, bool, error) {
		for i, e := range es {
			if e.raw == "" && e.ID == id {
				if ref == nil {
					return slices.Delete(es, i, i+1), true, nil
				}
				es[i].Refused, es[i].asRead = ref, ""
				return es, true, nil
			}
		}
		return es, false, nil
	})
}

// drain sends, in file order, the entries eligible allows, one at a time: the
// lock is held to pick an entry and again to settle it, never across the
// request, and every result reaches the file before the next send. A sent
// entry leaves the file; a refused one stays, marked, and the next goes on;
// any other error stops with a *stopError. A stale card version is retried
// once, since the entry's own run read it before the card moved.
func (a *app) drain(ctx context.Context, c *client.Client, o outbox, slug string, eligible func(outboxEntry) bool, report func(sendOutcome)) error {
	for {
		var e outboxEntry
		found := false
		err := o.edit(func(es []outboxEntry) ([]outboxEntry, bool, error) {
			for _, x := range es {
				if x.raw == "" && eligible(x) {
					e, found = x, true
					break
				}
			}
			return es, false, nil
		})
		if err != nil || !found {
			return err
		}
		cl, err := clientFor(c, e.Session)
		var card apitypes.Card
		if err == nil {
			if card, err = a.replay(ctx, cl, slug, e); staleVersion(err) {
				card, err = a.replay(ctx, cl, slug, e)
			}
		}
		if err == nil {
			if err := o.settle(e.ID, nil); err != nil {
				return err
			}
			report(sendOutcome{entry: e, number: card.Number})
			continue
		}
		ref, ok := a.refusalOf(err)
		if !ok {
			return &stopError{entry: e, err: err}
		}
		if err := o.settle(e.ID, &ref); err != nil {
			return err
		}
		e.Refused = &ref
		report(sendOutcome{entry: e, refusal: &ref})
	}
}

// flushOutbox sends the chat's own queued entries before the command's own
// request, once per process. A queue file that does not exist costs one stat.
// When the server cannot be reached it remembers that, so the command's own
// write is queued behind the older ones instead of tried. Otherwise it says
// what of the caller's still waits.
func (a *app) flushOutbox(c *client.Client, slug string) {
	if a.flushed || a.noFlush {
		return
	}
	a.flushed = true
	server, err := originOf(c.BaseURL())
	if err != nil {
		return
	}
	o, err := a.outboxAt(server, slug)
	if err != nil || !o.exists() {
		return
	}
	owner, ok := a.outboxOwner()
	if !ok {
		return
	}
	session := a.session()
	eligible := func(e outboxEntry) bool {
		return e.Owner == owner && e.Session == session && e.Refused == nil && e.Project == slug && sameServer(e.Server, server)
	}
	es, err := o.read()
	if err != nil {
		return
	}
	told := map[string]bool{}
	if slices.ContainsFunc(es, func(e outboxEntry) bool { return e.raw == "" && eligible(e) }) {
		ctx := a.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		err = a.drain(ctx, c, o, slug, eligible, func(out sendOutcome) {
			if out.refusal != nil {
				told[out.entry.ID] = true
				fmt.Fprintf(a.errOut(), "lorvik: queued %s was refused: %s; see lorvik sync --list\n", out.entry.what(), out.refusal.Message)
				return
			}
			fmt.Fprintf(a.errOut(), "lorvik: sent queued %s\n", sentWhat(out))
		})
		switch {
		case err == nil:
		case unreachable(err):
			a.outboxDown = err
			return
		default:
			fmt.Fprintf(a.errOut(), "lorvik: %v\n", err)
		}
		if es, err = o.read(); err != nil {
			return
		}
	}
	var waiting, refused int
	for _, e := range es {
		switch {
		case e.raw != "" || e.Owner != owner || e.Project != slug || !sameServer(e.Server, server) || told[e.ID]:
		case e.Refused != nil:
			refused++
		default:
			waiting++
		}
	}
	if line := waitingLine(waiting, refused); line != "" {
		fmt.Fprintf(a.errOut(), "lorvik: %s\n", line)
	}
}

// waitingLine counts the caller's entries a command left in the queue:
// another chat's, which only sync sends, and refused ones. A queue outside
// the repo is seen nowhere else.
func waitingLine(waiting, refused int) string {
	var parts []string
	if waiting > 0 {
		parts = append(parts, fmt.Sprintf("%d waiting", waiting))
	}
	if refused > 0 {
		parts = append(parts, fmt.Sprintf("%d refused", refused))
	}
	if parts == nil {
		return ""
	}
	return "queued writes: " + strings.Join(parts, ", ") + "; lorvik sync sends what waits, lorvik sync --list shows them"
}

// sentWhat is the entry's line once sent; an add names the card it made.
func sentWhat(out sendOutcome) string {
	if out.entry.Command == cmdAdd {
		return fmt.Sprintf("%s as #%d", out.entry.what(), out.number)
	}
	return out.entry.what()
}

// warnBad says once per line that the queue file holds something that is not
// an entry.
func (o outbox) warnBad(w io.Writer, es []outboxEntry) {
	for _, e := range es {
		if e.raw != "" {
			fmt.Fprintf(w, "lorvik: %s: line %d is not a queue entry; fix or delete it\n", o.path, e.line)
		}
	}
}
