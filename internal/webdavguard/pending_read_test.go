package webdavguard

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPendingReadsStayLocal(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("pending read reached upstream")
		w.WriteHeader(500)
	}))
	defer upstream.Close()
	origin, _ := url.Parse(upstream.URL)
	g, err := NewStagingProxy("", origin, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := g.stage("/a & b"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, body string
		status       int
	}{
		{"GET", "", 200}, {"HEAD", "", 200},
		{"PROPFIND", `<propfind xmlns="DAV:"><prop><getcontentlength/><resourcetype/><missing/></prop></propfind>`, 207},
	} {
		r := httptest.NewRequest(tc.method, "/a%20&%20b", strings.NewReader(tc.body))
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.method, w.Code, w.Body.String())
		}
		if tc.method != "PROPFIND" && w.Body.Len() != 0 {
			t.Fatal("nonempty placeholder")
		}
		if tc.method == "PROPFIND" && (!strings.Contains(w.Body.String(), ">0<") || !strings.Contains(w.Body.String(), "404 Not Found")) {
			t.Fatal(w.Body.String())
		}
		if !g.pendingPath("/a & b") {
			t.Fatal("read removed receipt")
		}
	}
}
