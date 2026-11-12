package api

import (
	"net/http/httptest"
	"testing"
)

func TestRegistryHandlersReturn503WithoutRegistry(t *testing.T) {
	s := &Server{}
	for name, h := range map[string]func(*httptest.ResponseRecorder){
		"review": func(rec *httptest.ResponseRecorder) {
			s.handleReview(rec, httptest.NewRequest("POST", "/api/admin/review", nil))
		},
		"inspect": func(rec *httptest.ResponseRecorder) {
			s.handleInspectPackage(rec, httptest.NewRequest("GET", "/api/admin/review/inspect", nil))
		},
		"upstream": func(rec *httptest.ResponseRecorder) {
			s.handleUpstreamFeed(rec, httptest.NewRequest("GET", "/api/admin/upstream/feed", nil))
		},
	} {
		rec := httptest.NewRecorder()
		h(rec)
		if rec.Code != 503 {
			t.Errorf("%s: 应返回 503，得到 %d：%s", name, rec.Code, rec.Body.String())
		}
	}
}
