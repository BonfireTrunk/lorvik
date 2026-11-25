// Package server wires the HTTP API, the embedded dashboard, and graceful
// shutdown for lorvikd.
package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/config"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// Deps are the collaborators the server needs. Everything is injected; the
// package has no globals.
type Deps struct {
	Config config.Server
	Logger *slog.Logger
	// UI is the dashboard build output, or nil when built with -tags noui.
	UI fs.FS
	// DBPing reports database health. Nil means no database is configured.
	// When Store is set and DBPing is nil, New wires it to Store.Ping.
	DBPing func(ctx context.Context) error
	// Store is the persistence layer. Nil serves the UI and healthz only; every
	// other API route answers 503.
	Store Store
	// IDs generates ids; nil means a generator with a random node id.
	IDs *domain.IDGen
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Counters holds the sign-in limits' counts; nil keeps them in this
	// process's memory.
	Counters Counters
}

// Server is a built lorvikd: the http.Server plus the background work that
// runs beside it.
type Server struct {
	HTTP *http.Server
	svc  *Service
	d    Deps
}

// New builds the server. It does not start listening.
func New(d Deps) (*Server, error) {
	if d.DBPing == nil && d.Store != nil {
		d.DBPing = d.Store.Ping
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	var svc *Service
	if d.Store != nil {
		svc = NewService(d.Store, d.IDs, d.Now, d.Logger)
		svc.password = d.Config.AuthPassword
		trusted, err := d.Config.Proxies()
		if err != nil {
			return nil, err
		}
		svc.limits.proxies = trusted
		if d.Counters != nil {
			svc.limits.share(d.Counters)
		}
		if d.Config.GitHubEnabled() {
			svc.github = newGitHubClient(d.Config.GitHubClientID, d.Config.GitHubClientSecret, githubWebURL, githubAPIURL, githubHTTPClient(), svc.now, svc.logger)
		}
	}
	mux := http.NewServeMux()
	newGitHubWeb(svc, d.Config, d.Logger).register(mux)
	mux.Handle("/api/", newAPI(d, svc))

	ui, err := uiHandler(d)
	if err != nil {
		return nil, err
	}
	mux.Handle("/", ui)

	var h http.Handler = mux
	h = requestLogger(d.Logger)(h)
	h = requestID(h)

	return &Server{HTTP: &http.Server{
		Addr:              d.Config.HTTPAddr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}, svc: svc, d: d}, nil
}

// Handler is the full HTTP handler, for tests.
func (s *Server) Handler() http.Handler { return s.HTTP.Handler }

// Run serves until ctx is cancelled, then shuts down within the configured
// timeout. It returns nil on a clean shutdown. With a store it also runs the
// claim sweeper and, when the store can listen for changes, the notifier that
// wakes event streams.
func (s *Server) Run(ctx context.Context) error {
	srv, d := s.HTTP, s.d
	srv.BaseContext = func(net.Listener) context.Context { return ctx }

	if s.svc != nil {
		var wg sync.WaitGroup
		bgCtx, stopBackground := context.WithCancel(ctx)
		defer wg.Wait()
		defer stopBackground()
		wg.Go(func() { s.svc.RunSweeper(bgCtx, time.Minute) })
		if n, ok := d.Store.(Notifier); ok {
			wg.Go(func() { s.svc.hub.runNotifier(bgCtx, n, d.Logger) })
		}
	}

	errc := make(chan error, 1)
	go func() {
		d.Logger.InfoContext(ctx, "listening", "addr", srv.Addr, "ui", d.UI != nil)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("listen on %s: %w", srv.Addr, err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), d.Config.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	d.Logger.InfoContext(ctx, "stopped")
	return nil
}

// uiHandler picks dev proxy, embedded SPA, or a plain notice, in that order.
func uiHandler(d Deps) (http.Handler, error) {
	if d.Config.DevUIProxy != "" {
		target, err := url.Parse(d.Config.DevUIProxy)
		if err != nil {
			return nil, fmt.Errorf("%sDEV_UI_PROXY: %w", config.Prefix, err)
		}
		rp := &httputil.ReverseProxy{
			Rewrite: func(r *httputil.ProxyRequest) {
				r.SetURL(target)
				r.Out.Host = target.Host
			},
		}
		return rp, nil
	}
	if d.UI != nil {
		if _, err := fs.Stat(d.UI, "index.html"); err == nil {
			return spaHandler(d.UI), nil
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "lorvikd is running. The dashboard is not included in this build; the API is under /api/v1/.")
	}), nil
}
