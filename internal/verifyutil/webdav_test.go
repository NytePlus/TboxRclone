package verifyutil

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestLabOrigin(t *testing.T) {
	for _, tc := range []struct {
		origin, base string
		allow, valid bool
	}{
		{"http://127.0.0.1/", "fixture", true, true},
		{"http://[::1]/", "fixture/sub", true, true},
		{"http://127.0.0.1/", "fixture", false, false},
		{"https://127.0.0.1/", "fixture", true, false},
		{"http://example.com/", "fixture", true, false},
		{"http://user@127.0.0.1/", "fixture", true, false},
		{"http://127.0.0.1/?q=x", "fixture", true, false},
		{"http://127.0.0.1/", "../escape", true, false},
		{"http://127.0.0.1/", "", true, false},
	} {
		if _, err := LabOrigin(tc.origin, tc.base, tc.allow); (err == nil) != tc.valid {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
}

func TestFreshCollectionAndSettled(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusCreated)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/base/fixture/" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.WriteHeader(int(status.Load()))
	}))
	defer server.Close()
	origin, _ := url.Parse(server.URL + "/base")
	if err := FreshCollection(server.Client(), origin, "fixture"); err != nil {
		t.Fatal(err)
	}
	status.Store(http.StatusMethodNotAllowed)
	if err := FreshCollection(server.Client(), origin, "fixture"); err == nil {
		t.Fatal("accepted existing collection")
	}
	for _, code := range []int{200, 404} {
		status.Store(int32(code))
		got, published, err := WaitSettled(server.Client(), origin, "fixture/")
		if err != nil || got != code || published != (code == 200) {
			t.Fatalf("%d %v %v", got, published, err)
		}
	}
}
