package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

const (
	defaultLockTTL   = 10 * time.Minute
	lockPollEvery    = 5 * time.Second
	lockWaitLimit    = 30 * time.Minute
	lockNoticeEvery  = time.Minute
	lockCallTimeout  = 10 * time.Second
	lockWaitingLabel = "waiting for "
)

// lockFlags are the flags shared by `lock` and `lock run`.
type lockFlags struct {
	purpose string
	ttl     time.Duration
	card    int64
	wait    bool
}

func (lf *lockFlags) bind(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVarP(&lf.purpose, "message", "m", "", "What the lock protects (required)")
	f.DurationVar(&lf.ttl, "for", defaultLockTTL, "Lease length; the server accepts 1m to 1h")
	f.Int64Var(&lf.card, "card", 0, "Number of the card the work is for, in the current project")
	f.BoolVar(&lf.wait, "wait", false, "Poll every 5 seconds until the lock is free, for up to 30 minutes")
}

func (lf *lockFlags) body(a *app) (apitypes.AcquireLock, error) {
	if strings.TrimSpace(lf.purpose) == "" {
		return apitypes.AcquireLock{}, cmdutil.Usagef("-m <purpose> is required: say what the lock protects")
	}
	if lf.ttl <= 0 {
		return apitypes.AcquireLock{}, cmdutil.Usagef("--for %s: want a positive duration such as 10m", lf.ttl)
	}
	in := apitypes.AcquireLock{Purpose: lf.purpose, TTLSeconds: ttlSeconds(lf.ttl)}
	if lf.card > 0 {
		slug, err := a.slug()
		if err != nil {
			return in, err
		}
		in.Card, in.Project = &lf.card, slug
	}
	return in, nil
}

// ttlSeconds rounds up so a sub-second value never becomes 0, which the
// server would read as "use the default".
func ttlSeconds(d time.Duration) int { return int((d + time.Second - 1) / time.Second) }

func newLock(a *app) *cobra.Command {
	var lf lockFlags
	cmd := &cobra.Command{
		Use:   "lock <name>",
		Short: "Take a named lock on a shared resource",
		Long: `Take an org-wide lock on a resource two agents must not touch at once (a
generated file, a schema, a port), with -m saying why. The lease lasts 10 minutes
unless --for says otherwise; renew it by running lock again from the same
session, and release it with 'lorvik unlock'. Exit 4 if someone else holds it,
unless --wait polls until it is free. A lock named like a card lane holds back
the ready cards in that lane. To hold a lock around one command, use 'lock run'.

Example:
  lorvik lock schema -m "regenerating sqlc" --for 15m --card 42`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			in, err := lf.body(a)
			if err != nil {
				return err
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			l, err := a.acquireLock(cmd.Context(), c, args[0], in, lf.wait)
			if err != nil {
				return err
			}
			return a.emit(l, func(p *cmdutil.Printer) {
				p.Line("lock %s held until %s (%s) for %q", l.Name, l.ExpiresAt.In(a.f.Time().Location()).Format("15:04:05"), ttlText(lf.ttl), l.Purpose)
			})
		},
	}
	lf.bind(cmd)
	cmd.AddCommand(newLockRun(a))
	return cmd
}

func newUnlock(a *app) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "unlock <name>",
		Short: "Release a named lock",
		Long: `Release a lock you hold so others can take it. --force releases another
actor's lock and needs the reviewer role.

Example:
  lorvik unlock schema`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client()
			if err != nil {
				return err
			}
			if err := c.ReleaseLock(cmd.Context(), args[0], force); err != nil {
				return err
			}
			return a.emit(releasedView{Name: args[0], Released: true}, func(p *cmdutil.Printer) { p.Line("released %s", args[0]) })
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Release a lock held by someone else (reviewer role)")
	return cmd
}

type releasedView struct {
	Name     string `json:"name"`
	Released bool   `json:"released"`
}

func newLocks(a *app) *cobra.Command {
	var pg client.PageParams
	cmd := &cobra.Command{
		Use:   "locks",
		Short: "List the live locks of the org",
		Long: `List every live lock, whoever holds it: name, holder, session, time left,
purpose and the card when one was named. Check it before touching a shared
resource.

Example:
  lorvik locks`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client()
			if err != nil {
				return err
			}
			list, err := lockPage(cmd.Context(), c, pg)
			if err != nil {
				return err
			}
			a.resolve(cmd.Context(), lockActors(list.Items)...)
			return a.emit(list, func(p *cmdutil.Printer) {
				if len(list.Items) == 0 {
					p.Line("no locks")
				}
				for _, l := range list.Items {
					p.Line("%s", lockLine(p, l))
				}
				p.Paging(list.Page, a.nextCommand(cmd))
			})
		},
	}
	pageFlags(cmd, &pg, "Maximum locks to print")
	return cmd
}

func newLockRun(a *app) *cobra.Command {
	var lf lockFlags
	cmd := &cobra.Command{
		Use:   "run <name> -m <purpose> -- <command> [args...]",
		Short: "Run a command while holding a lock",
		Long: `Take the lock, run the command, release the lock. The lock is held for the
command's life and renewed automatically; if a renewal fails the command is
killed (its whole process group), lorvik says so on stderr and exits 4, because
other agents may already think the lock is free. Otherwise lorvik exits with
the command's exit code. The command keeps stdin, stdout and stderr, and
nothing else is printed on stdout. A SIGINT or SIGTERM to lorvik is forwarded
to the command, then the lock is released. The -- before the command is
required.

Example:
  lorvik lock run schema -m "regenerating sqlc" -- task generate`,
		Args: usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			dash := cmd.ArgsLenAtDash()
			if dash < 0 || len(args) == dash {
				return cmdutil.Usagef("lock run needs `-- <command> [args...]` after the flags (see `%s --help`)", cmd.CommandPath())
			}
			if dash != 1 {
				return cmdutil.Usagef("lock run takes one lock name before `--` (see `%s --help`)", cmd.CommandPath())
			}
			in, err := lf.body(a)
			if err != nil {
				return err
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			return a.runLocked(cmd.Context(), c, args[0], in, lf.ttl, lf.wait, args[dash:])
		},
		Annotations: map[string]string{annotationNoNotice: "1"},
	}
	lf.bind(cmd)
	return cmd
}

// acquireLock takes the lock; with wait it polls on a 409 until the lock is
// free or lockWaitLimit has passed.
func (a *app) acquireLock(ctx context.Context, c *client.Client, name string, in apitypes.AcquireLock, wait bool) (apitypes.Lock, error) {
	poll := cmp0(a.f.LockPoll, lockPollEvery)
	start := time.Now()
	var lastNotice time.Time
	for {
		l, err := c.AcquireLock(ctx, name, in)
		ae, held := errors.AsType[*client.APIError](err)
		if err == nil || !wait || !held || ae.Status != 409 {
			return l, err
		}
		if time.Since(start) >= lockWaitLimit {
			return l, &cmdutil.ExitError{Code: cmdutil.ExitConflict, Err: fmt.Errorf("gave up waiting for %s after %s: %w", name, lockWaitLimit, err)}
		}
		if lastNotice.IsZero() || time.Since(lastNotice) >= lockNoticeEvery {
			lastNotice = time.Now()
			detail := strings.TrimPrefix(ae.Detail, "lock held: "+name+" is ")
			fmt.Fprintf(a.errOut(), "%s%s: %s\n", lockWaitingLabel, name, detail)
		}
		t := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			t.Stop()
			return l, ctx.Err()
		case <-t.C:
		}
	}
}

func cmp0(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// runLocked holds the lock around argv. Renewals use a context that outlives
// a SIGINT to lorvik, so an interrupt is forwarded to the command and the lock
// is released afterwards instead of being treated as lost.
func (a *app) runLocked(ctx context.Context, c *client.Client, name string, in apitypes.AcquireLock, ttl time.Duration, wait bool, argv []string) error {
	if _, err := a.acquireLock(ctx, c, name, in, wait); err != nil {
		return err
	}
	base := context.WithoutCancel(ctx)
	runCtx, killRun := context.WithCancel(base)
	defer killRun()

	release := func() error {
		rctx, cancel := context.WithTimeout(base, lockCallTimeout)
		defer cancel()
		return c.ReleaseLock(rctx, name, false)
	}

	proc := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	setProcessGroup(proc)
	proc.Cancel = func() error { return killProcessGroup(proc) }
	s := a.f.IOStreams
	proc.WaitDelay = lockCallTimeout
	proc.Stdin, proc.Stdout, proc.Stderr = s.In, s.Out, s.ErrOut

	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)

	if err := proc.Start(); err != nil {
		if rerr := release(); rerr != nil {
			fmt.Fprintf(a.errOut(), "lorvik: warning: release %s: %v\n", name, rerr)
		}
		return fmt.Errorf("start %s: %w", argv[0], err)
	}

	var (
		mu   sync.Mutex
		lost error
	)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		tick := time.NewTicker(cmp0(a.f.LockRenew, ttl/2))
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case sig := <-sigs:
				_ = signalProcessGroup(proc, sig)
			case <-tick.C:
				rctx, cancel := context.WithTimeout(base, lockCallTimeout)
				_, err := c.RenewLock(rctx, name, apitypes.RenewLock{TTLSeconds: in.TTLSeconds})
				cancel()
				if err != nil {
					mu.Lock()
					lost = err
					mu.Unlock()
					killRun()
					return
				}
			}
		}
	})

	waitErr := proc.Wait()
	close(done)
	wg.Wait()

	mu.Lock()
	lostErr := lost
	mu.Unlock()
	if lostErr != nil {
		return &cmdutil.ExitError{Code: cmdutil.ExitConflict, Err: fmt.Errorf("lock %s lost: %w; command killed", name, lostErr)}
	}
	if rerr := release(); rerr != nil {
		fmt.Fprintf(a.errOut(), "lorvik: warning: release %s: %v\n", name, rerr)
	}
	if waitErr == nil {
		return nil
	}
	code := cmdutil.ExitGeneric
	if ee, ok := errors.AsType[*exec.ExitError](waitErr); ok {
		if n := ee.ExitCode(); n > 0 {
			code = n
		}
	} else {
		fmt.Fprintf(a.errOut(), "lorvik: run %s: %v\n", argv[0], waitErr)
	}
	return &cmdutil.ExitError{Code: code, Err: cmdutil.ErrSilent}
}

func ttlText(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	return fmt.Sprintf("%ds", ttlSeconds(d))
}

func lockActors(locks []apitypes.Lock) []apitypes.Actor {
	return actorsOf(locks, func(l apitypes.Lock) apitypes.Actor { return l.Holder })
}

// leaseLeft is the time until expires in whole minutes, rounded up, as "Nm left".
func leaseLeft(p *cmdutil.Printer, expires time.Time) string {
	left := max(expires.Sub(p.Now()), 0)
	return fmt.Sprintf("%dm left", int((left+time.Minute-1)/time.Minute))
}

// lockLine is one lock as `name  holder  session  Nm left  "purpose"  #card`.
func lockLine(p *cmdutil.Printer, l apitypes.Lock) string {
	sess := cmdutil.ChatLabel(l.Session, l.SessionName)
	if sess == "" {
		sess = "-"
	}
	line := fmt.Sprintf("%s  %s  %s  %s  %q", l.Name, p.Author(l.Holder), sess, leaseLeft(p, l.ExpiresAt), l.Purpose)
	if l.Card != nil {
		line += fmt.Sprintf("  #%d", *l.Card)
	}
	return line
}

func printLocks(p *cmdutil.Printer, locks []apitypes.Lock) {
	if len(locks) == 0 {
		return
	}
	p.Line("locks:")
	for _, l := range locks {
		p.Line("  %s", lockLine(p, l))
	}
}

// lockPage tolerates servers without the locks endpoint and keeps JSON arrays non-null.
func lockPage(ctx context.Context, c *client.Client, pg client.PageParams) (apitypes.LockList, error) {
	list, err := c.ListLocks(ctx, pg)
	if err != nil && !isNotFound(err) {
		return apitypes.LockList{}, err
	}
	if list.Items == nil {
		list.Items = []apitypes.Lock{}
	}
	return list, nil
}

// listLocks reads every live lock, for a report that must not hide one.
func listLocks(ctx context.Context, c *client.Client) ([]apitypes.Lock, error) {
	locks, err := allPages(func(pg client.PageParams) ([]apitypes.Lock, apitypes.Page, error) {
		list, err := lockPage(ctx, c, pg)
		return list.Items, list.Page, err
	})
	if locks == nil && err == nil {
		locks = []apitypes.Lock{}
	}
	return locks, err
}
