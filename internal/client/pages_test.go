package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func TestGetPageRoutesAndHTMLIsNotEscaped(t *testing.T) {
	t.Parallel()
	const html = `<p class="a">1 < 2 && 3 > 2</p>`
	tests := []struct {
		name     string
		wantPath string
		get      func(c *Client) (apitypes.HTMLPage, error)
	}{
		{"card", "/projects/demo/cards/42/page", func(c *Client) (apitypes.HTMLPage, error) {
			return c.GetCardPage(t.Context(), "demo", 42)
		}},
		{"note by slug", "/notes/arch/page?project=demo", func(c *Client) (apitypes.HTMLPage, error) {
			return c.GetNotePage(t.Context(), "arch", "demo")
		}},
		{"note by id", "/notes/106666050296328877/page", func(c *Client) (apitypes.HTMLPage, error) {
			return c.GetNotePage(t.Context(), "106666050296328877", "")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got string
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.RequestURI()
				enc := json.NewEncoder(w)
				enc.SetEscapeHTML(false)
				_ = enc.Encode(apitypes.HTMLPage{HTML: html, Bytes: len(html), Version: 3})
			}, "tok")
			page, err := tt.get(c)
			if err != nil {
				t.Fatal(err)
			}
			if got != "/api/v1"+tt.wantPath {
				t.Errorf("request = %q, want /api/v1%s", got, tt.wantPath)
			}
			if page.HTML != html || page.Bytes != len(html) || page.Version != 3 {
				t.Errorf("page = %+v", page)
			}
		})
	}
}

func TestGetPageWithoutPageIsAPIError404(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"title":"Not Found","status":404}`))
	}, "tok")
	_, err := c.GetCardPage(t.Context(), "demo", 42)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 404 {
		t.Fatalf("err = %v, want APIError 404", err)
	}
}
