package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestHandler(t *testing.T) {
	h := handler(fstest.MapFS{
		"index.html":      {Data: []byte("<html>app</html>")},
		"assets/app-1.js": {Data: []byte("js")},
		"favicon.svg":     {Data: []byte("<svg/>")},
	})
	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{"/", "<html>app</html>", 200},
		{"/rooms/abc", "<html>app</html>", 200},
		{"/assets/app-1.js", "js", 200},
		{"/favicon.svg", "<svg/>", 200},
		{"/assets/missing.js", "", 404},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.status || (tc.body != "" && rec.Body.String() != tc.body) {
			t.Errorf("GET %s = %d %q, want %d %q", tc.path, rec.Code, rec.Body.String(), tc.status, tc.body)
		}
	}
}

func TestHandlerNotBuilt(t *testing.T) {
	rec := httptest.NewRecorder()
	handler(fstest.MapFS{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", rec.Code)
	}
}
