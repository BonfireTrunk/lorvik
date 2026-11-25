package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestSPAHandler(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
		"favicon.ico":   {Data: []byte("icon")},
	}
	h := spaHandler(fsys)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantBody   string
	}{
		{"root serves index", http.MethodGet, "/", 200, "<html>app</html>"},
		{"existing asset is served as itself", http.MethodGet, "/assets/app.js", 200, "console.log(1)"},
		{"unknown client route falls back to index", http.MethodGet, "/cards/42", 200, "<html>app</html>"},
		{"nested unknown route falls back to index", http.MethodGet, "/p/x/decisions/7", 200, "<html>app</html>"},
		{"post is rejected", http.MethodPost, "/", 405, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			if rec.Code != tt.wantStatus {
				t.Fatalf("%s %s status = %d, want %d", tt.method, tt.path, rec.Code, tt.wantStatus)
			}
			if tt.wantBody == "" {
				return
			}
			body, _ := io.ReadAll(rec.Body)
			if string(body) != tt.wantBody {
				t.Errorf("%s %s body = %q, want %q", tt.method, tt.path, body, tt.wantBody)
			}
		})
	}
}
