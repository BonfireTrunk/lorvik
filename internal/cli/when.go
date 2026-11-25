package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

// parseWhen reads a point in time given on the command line: a relative
// span back from now (30m, 24h, 7d), a calendar day (2026-10-20, the start
// of that day in loc) or an RFC 3339 timestamp.
func parseWhen(s string, now time.Time, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	if n := len(s) - 1; n > 0 {
		var unit time.Duration
		switch s[n] {
		case 'm':
			unit = time.Minute
		case 'h':
			unit = time.Hour
		case 'd':
			unit = 24 * time.Hour
		}
		if unit != 0 {
			count, err := strconv.ParseUint(s[:n], 10, 31)
			if err != nil {
				return time.Time{}, fmt.Errorf("%q: want a whole number before %q", s, s[n:])
			}
			return now.Add(-time.Duration(count) * unit), nil
		}
	}
	if t, err := time.ParseInLocation(time.DateOnly, s, loc); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("%q: want YYYY-MM-DD, a relative span like 7d, 24h or 30m, or an RFC 3339 time", s)
}

// resolveTimes validates the named time flags and rewrites each one that was
// set to an absolute RFC 3339 time. The paging line repeats the flags as
// typed, so a relative span must not slide between pages.
func (a *app) resolveTimes(cmd *cobra.Command, names ...string) error {
	now := a.f.Time()
	for _, name := range names {
		if !cmd.Flags().Changed(name) {
			continue
		}
		raw, err := cmd.Flags().GetString(name)
		if err != nil {
			return err
		}
		t, err := parseWhen(raw, now, time.Local)
		if err != nil {
			return cmdutil.Usagef("--%s %v", name, err)
		}
		if err := cmd.Flags().Set(name, t.Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return nil
}

// timeFlags registers --<field>-after and --<field>-before, bound to the
// given strings.
func timeFlags(f *pflag.FlagSet, field string, after, before *string) {
	f.StringVar(after, field+"-after", "", "Only "+field+" at or after this time: YYYY-MM-DD, 7d, 24h, 30m")
	f.StringVar(before, field+"-before", "", "Only "+field+" strictly before this time: YYYY-MM-DD, 7d, 24h, 30m")
}
