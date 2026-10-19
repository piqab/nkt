package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Хост помнит хабы по заголовку; мусор в заголовке не запоминается.
func TestNoteHub(t *testing.T) {
	s := &Server{}
	h := s.noteHub(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, id := range []string{"0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210", "not-a-hub-id", ""} {
		r := httptest.NewRequest("GET", "/api/overview", nil)
		if id != "" {
			r.Header.Set(HubIDHeader, id)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	if got := s.hubsSeenList(); len(got) != 2 {
		t.Fatalf("seen: %+v", got)
	}
}
