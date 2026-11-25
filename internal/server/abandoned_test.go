package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/domain"
)

type failingStore struct {
	*fakeStore
	tokenErr    error
	projectsErr error
}

func (f *failingStore) GetTokenByHash(ctx context.Context, hash []byte) (domain.Token, error) {
	if f.tokenErr != nil {
		return domain.Token{}, f.tokenErr
	}
	return f.fakeStore.GetTokenByHash(ctx, hash)
}

func (f *failingStore) ListProjects(ctx context.Context, orgID domain.ID) ([]domain.Project, error) {
	if f.projectsErr != nil {
		return nil, f.projectsErr
	}
	return f.fakeStore.ListProjects(ctx, orgID)
}

func TestAbandonedRequestIsNotAnError(t *testing.T) {
	t.Parallel()
	canceled := fmt.Errorf("query: %w", context.Canceled)
	tests := []struct {
		name       string
		tokenErr   error
		projErr    error
		cancel     bool
		wantStatus int
		wantError  bool
	}{
		{"token lookup canceled with the request", canceled, nil, true, statusClientClosed, false},
		{"handler canceled with the request", nil, canceled, true, statusClientClosed, false},
		{"token lookup fails for another reason", errors.New("connection refused"), nil, true, http.StatusInternalServerError, true},
		{"handler store error", nil, errors.New("connection refused"), false, http.StatusInternalServerError, true},
		{"canceled error while the request is alive", nil, canceled, false, http.StatusInternalServerError, true},
		{"deadline of our own", nil, fmt.Errorf("query: %w", context.DeadlineExceeded), false, http.StatusInternalServerError, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			fs := &failingStore{fakeStore: e.fs, tokenErr: tc.tokenErr, projectsErr: tc.projErr}
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			svc := NewService(fs, e.ids, e.clock, logger)
			h := requestLogger(logger)(newAPI(Deps{Logger: logger, Store: fs, DBPing: fs.Ping, Now: e.clock}, svc))

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/projects", nil)
			req.Header.Set("Authorization", "Bearer "+userSecret)
			h.ServeHTTP(httptest.NewRecorder(), req)

			out := logs.String()
			if hasError := strings.Contains(out, "level=ERROR"); hasError != tc.wantError {
				t.Errorf("ERROR logged = %v, want %v:\n%s", hasError, tc.wantError, out)
			}
			if want := fmt.Sprintf("status=%d", tc.wantStatus); !strings.Contains(out, want) {
				t.Errorf("access line lacks %s:\n%s", want, out)
			}
		})
	}
}
