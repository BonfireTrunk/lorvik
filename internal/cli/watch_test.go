package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

func sseEvent(w http.ResponseWriter, e apitypes.Event) {
	raw, _ := json.Marshal(e)
	fmt.Fprintf(w, "id: %s\ndata: %s\n\n", e.ID, raw)
	w.(http.Flusher).Flush()
}

func TestWatchPrintsFirstEventOnTheCardAndSkipsOthers(t *testing.T) {
	t.Parallel()
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/events/stream": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			sseEvent(w, apitypes.Event{ID: "1", Type: "card.comment", Actor: claude, Subject: apitypes.Subject{Type: "card", Number: 7}})
			sseEvent(w, apitypes.Event{ID: "2", Type: "card.comment", Actor: claude, Subject: apitypes.Subject{Type: "card", Number: 42}})
			<-r.Context().Done()
		},
		"GET /api/v1/actors": reply(200, apitypes.Actors{}),
	})
	h := newHarness(t, srv)
	if err := h.run(t, "watch", "42", "-p", "demo"); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	if !strings.Contains(out, "commented on #42") || strings.Contains(out, "#7") {
		t.Errorf("stdout = %q, want only the sentence for #42", out)
	}
}

func TestWatchTimesOutWithExitOne(t *testing.T) {
	t.Parallel()
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/events/stream": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		},
	})
	err := newHarness(t, srv).run(t, "watch", "42", "-p", "demo", "--timeout", "100ms")
	if cmdutil.ExitCode(err) != 1 || err == nil || err.Error() != "timed out waiting on #42" {
		t.Errorf("err = %v (exit %d), want exit 1 timed out waiting on #42", err, cmdutil.ExitCode(err))
	}
}
