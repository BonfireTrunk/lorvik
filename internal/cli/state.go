package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

// The inbox cursor and a rotated token live in the user config file next to
// api_url and token. The cursor is keyed by the token prefix and, when the
// session label is known, by the label too: two chats sharing one config file
// (and so one token) keep separate read positions.

const (
	cursorKeyPrefix = "cursor_"
	// sessionCursorTTL bounds the config file: every chat adds a key, and a
	// chat idle this long has lost its claims, so its key is not worth keeping.
	sessionCursorTTL = 7 * 24 * time.Hour
	// cursorStampSep separates the event id from the unix seconds of the last
	// write in a session cursor value.
	cursorStampSep = "@"
)

// splitCursor reads a stored cursor value: the event id and, when the value
// carries one, the unix seconds it was written at. A plain id (written before
// stamps existed) has none.
func splitCursor(v string) (id string, at int64, stamped bool) {
	id, rest, found := strings.Cut(v, cursorStampSep)
	if !found {
		return v, 0, false
	}
	at, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		return id, 0, false
	}
	return id, at, true
}

func stampCursor(id string, now time.Time) string {
	return id + cursorStampSep + strconv.FormatInt(now.Unix(), 10)
}

// newerCursor returns the cursor that reads further along the feed. A position
// (it contains "-") beats a bare event id saved by an older client; two
// positions compare as strings, which is feed order; two ids compare as
// numbers. A value that is neither loses.
func newerCursor(cur, next string) string {
	cr, nr := cursorRank(cur), cursorRank(next)
	switch {
	case nr > cr:
		return next
	case nr < cr:
		return cur
	}
	switch nr {
	case cursorPosition:
		if next > cur {
			return next
		}
	case cursorLegacyID:
		c, _ := strconv.ParseUint(cur, 10, 64)
		n, _ := strconv.ParseUint(next, 10, 64)
		if n > c {
			return next
		}
	default:
		return next
	}
	return cur
}

const (
	cursorOther = iota
	cursorLegacyID
	cursorPosition
)

// isPosition reports whether v has the shape of an event position: digits, a
// hyphen, digits. The server owns the format; this only tells it apart from
// an id or a corrupted value.
func isPosition(v string) bool {
	xid, id, ok := strings.Cut(v, "-")
	return ok && isID(xid) && isID(id)
}

func cursorRank(v string) int {
	switch {
	case isPosition(v):
		return cursorPosition
	case isID(v):
		return cursorLegacyID
	}
	return cursorOther
}

func tokenPrefix(secret string) string { return secret[:min(apitypes.TokenPrefixLen, len(secret))] }

// cursorKey is the config key holding the cursor for a token, or "" when
// there is no token to key it by.
func cursorKey(secret string) string {
	if secret == "" {
		return ""
	}
	return cursorKeyPrefix + tokenPrefix(secret)
}

// sessionCursorKey is cursorKey narrowed to one session label; it equals
// cursorKey when the label is empty.
func sessionCursorKey(secret, label string) string {
	key := cursorKey(secret)
	if key == "" || label == "" {
		return key
	}
	return key + "_" + label
}

// storedCursor returns the saved inbox cursor for the token and session, or
// "" when the caller has never read the inbox (or the config cannot be
// read). A session without a key of its own starts from the token-only
// cursor, so introducing labels never re-reads history.
func (a *app) storedCursor(secret, label string) string {
	key := sessionCursorKey(secret, label)
	if key == "" {
		return ""
	}
	s, err := a.settings()
	if err != nil {
		return ""
	}
	m, err := readTOMLFile(s.ConfigPath)
	if err != nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		v = m[cursorKey(secret)]
	}
	id, _, _ := splitCursor(v)
	return id
}

// saveCursor remembers the cursor for the client's current token and session.
// A session write also advances the token-only cursor (never backwards), where
// a chat without a key of its own starts, and drops the keys of sessions idle
// for longer than sessionCursorTTL. Keys written before stamps existed are
// stamped now instead of dropped.
func (a *app) saveCursor(cursor string) error {
	token := a.cl.Token()
	if sessionCursorKey(token, a.cl.Session) == "" {
		return fmt.Errorf("no token to store the inbox cursor under; run `lorvik login` or set LORVIK_TOKEN")
	}
	s, err := a.settings()
	if err != nil {
		return err
	}
	err = editConfig(s.ConfigPath, func(m map[string]string) (map[string]string, error) {
		token := token
		// Another chat renewed the file's token since this one read it; its
		// cursors moved to the new token, so this one follows them there.
		if !s.TokenFromEnv && m["token"] != "" && m["token"] != token {
			token = m["token"]
		}
		key := sessionCursorKey(token, a.cl.Session)
		updates := map[string]string{key: cursor}
		agentKey := cursorKey(token)
		if agentKey == key {
			return updates, nil
		}
		now := a.f.Time()
		updates[key] = stampCursor(cursor, now)
		current, _, _ := splitCursor(m[agentKey])
		updates[agentKey] = newerCursor(current, cursor)
		for k, v := range m {
			if k == key || !strings.HasPrefix(k, agentKey+"_") {
				continue
			}
			id, at, stamped := splitCursor(v)
			switch {
			case !stamped:
				updates[k] = stampCursor(id, now)
			case now.Sub(time.Unix(at, 0)) > sessionCursorTTL:
				updates[k] = ""
			}
		}
		return updates, nil
	})
	if err != nil {
		return err
	}
	a.cl.Cursor = cursor
	return nil
}

// tokenRotated returns the client's OnToken hook. old is the secret in use
// before rotation. A token from the user config is replaced there, taking the
// cursor along; one from LORVIK_TOKEN can only be announced, because the
// environment belongs to the caller. The notice never carries the secret.
func (a *app) tokenRotated(old string) func(string) {
	return func(secret string) {
		s, err := a.settings()
		if err != nil {
			fmt.Fprintf(a.errOut(), "lorvik: the server rotated your token but it could not be saved: %v\n", err)
			return
		}
		if s.TokenFromEnv {
			fmt.Fprintln(a.errOut(), "lorvik: the server rotated your token; LORVIK_TOKEN stops working in 24h; store the token in the config file or mint a new one with lorvik token create")
			return
		}
		err = editConfig(s.ConfigPath, func(m map[string]string) (map[string]string, error) {
			return rotatedCursors(m, old, secret, a.cl.Session, a.cl.Cursor, a.f.Time()), nil
		})
		if err != nil {
			fmt.Fprintf(a.errOut(), "lorvik: the server rotated your token but it could not be saved: %v\n", err)
			return
		}
		fmt.Fprintln(a.errOut(), "lorvik: token rotated and saved to the config file")
	}
}

// rotatedCursors returns the config updates for a rotation from old to secret:
// the new token, and every cursor key of the old token moved under the new
// one, so the other chats keep their positions and the agent cursor stays
// the newest any chat read. The rotating chat's own key takes its cursor.
func rotatedCursors(m map[string]string, old, secret, label, cursor string, now time.Time) map[string]string {
	updates := map[string]string{"token": secret}
	oldKey, newKey := cursorKey(old), cursorKey(secret)
	if oldKey == "" || oldKey == newKey {
		return updates
	}
	for k, v := range m {
		if k == oldKey || strings.HasPrefix(k, oldKey+"_") {
			updates[newKey+strings.TrimPrefix(k, oldKey)] = v
			updates[k] = ""
		}
	}
	stored, _, _ := splitCursor(m[oldKey])
	updates[newKey] = newerCursor(stored, cursor)
	if newS := sessionCursorKey(secret, label); newS != newKey {
		updates[newS] = stampCursor(cursor, now)
	}
	return updates
}

// nextCommand returns a function that renders the command line which fetches
// the next page of cmd's list: the same command, args and flags as this run,
// with --cursor replaced. Output and connection flags are left out.
func (a *app) nextCommand(cmd *cobra.Command) func(cursor string) string {
	skip := map[string]bool{"cursor": true, "json": true, "quiet": true, "api-url": true}
	var parts []string
	for _, arg := range cmd.Flags().Args() {
		parts = append(parts, shellQuote(arg))
	}
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if skip[f.Name] {
			return
		}
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			for _, v := range sv.GetSlice() {
				parts = append(parts, "--"+f.Name, shellQuote(v))
			}
			return
		}
		if f.Value.Type() == "bool" && f.Value.String() == "true" {
			parts = append(parts, "--"+f.Name)
			return
		}
		parts = append(parts, "--"+f.Name, shellQuote(f.Value.String()))
	})
	return func(cursor string) string {
		return strings.Join(append(append([]string{cmd.CommandPath()}, parts...), "--cursor", cursor), " ")
	}
}

// shellQuote quotes s for a POSIX shell when it needs it.
func shellQuote(s string) string {
	safe := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@,+", r)
	}
	if s != "" && !strings.ContainsFunc(s, func(r rune) bool { return !safe(r) }) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// unreadNotice words the unread hint, or returns "" when there is nothing
// unread. Counts above apitypes.UnreadCap print as "100+".
func unreadNotice(unread int) string {
	if unread <= 0 {
		return ""
	}
	count := strconv.Itoa(unread)
	if unread > apitypes.UnreadCap {
		count = strconv.Itoa(apitypes.UnreadCap) + "+"
	}
	return count + " unread, run lorvik inbox"
}
