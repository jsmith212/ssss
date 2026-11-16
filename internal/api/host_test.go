package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAssetHandlerWithoutLibrary(t *testing.T) {
	handler := AssetHandler(New())

	tests := []struct {
		name   string
		method string
		path   string
		status int
	}{
		{"audio", http.MethodGet, "/audio/0123456789abcdef0123456789abcdef.f32", http.StatusNotFound},
		{"peaks", http.MethodGet, "/peaks/0123456789abcdef0123456789abcdef", http.StatusNotFound},
		{"audio post", http.MethodPost, "/audio/x.f32", http.StatusMethodNotAllowed},
		{"other path", http.MethodGet, "/index.html", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d", rec.Code, tt.status)
			}
		})
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audio/x.f32", nil))
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error == "" {
		t.Fatalf("body = %q, want JSON with an error field (err: %v)", rec.Body.String(), err)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}
