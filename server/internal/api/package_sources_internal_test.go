package api

import (
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPackageProxyAttachment(t *testing.T) {
	for _, tc := range []struct {
		status      int
		contentType string
		boundary    string
	}{
		{http.StatusPartialContent, `multipart/byteranges; boundary="byte-ranges"`, "byte-ranges"},
		{http.StatusPartialContent, "multipart/byteranges", ""},
		{http.StatusPartialContent, "text/html", ""},
		{http.StatusOK, "multipart/byteranges; boundary=byte-ranges", ""},
	} {
		w := httptest.NewRecorder()
		resp := &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.contentType}}}
		packageProxyAttachment(w, "artifact.bin", resp)
		mediaType, params, err := mime.ParseMediaType(w.Header().Get("Content-Type"))
		wantType := "application/octet-stream"
		if tc.boundary != "" {
			wantType = "multipart/byteranges"
		}
		if err != nil || mediaType != wantType || params["boundary"] != tc.boundary {
			t.Errorf("status %d, upstream type %q: got %q, %v", tc.status, tc.contentType, mediaType, params)
		}
		if !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("missing attachment protections: %v", w.Header())
		}
	}
}
