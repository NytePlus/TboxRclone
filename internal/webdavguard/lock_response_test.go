package webdavguard

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestLockDiscoveryIncludesGrantedLock(t *testing.T) {
	r := httptest.NewRequest("LOCK", "http://example.test/a%26b", strings.NewReader(`<D:lockinfo xmlns:D="DAV:" xmlns:u="urn:test"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype><D:owner><u:name>A &amp; B</u:name></D:owner></D:lockinfo>`))
	r.Header.Set("Depth", "0")
	token, body, err := lockResponse(r)
	if err != nil {
		t.Fatal(err)
	}
	var prop struct {
		Lock struct {
			Depth string `xml:"depth"`
			Token string `xml:"locktoken>href"`
			Root  string `xml:"lockroot>href"`
			Owner struct {
				Name string `xml:"urn:test name"`
			} `xml:"owner"`
		} `xml:"lockdiscovery>activelock"`
	}
	if err := xml.Unmarshal(body, &prop); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "urn:uuid:") || prop.Lock.Token != token || prop.Lock.Depth != "0" || prop.Lock.Root != "/a%26b" || prop.Lock.Owner.Name != "A & B" {
		t.Fatalf("invalid discovery: %s", body)
	}
}

func TestLockResponseRejectsMalformedRequests(t *testing.T) {
	for _, body := range []string{"", "lock", `<lockinfo xmlns="DAV:"><locktype><write/></locktype></lockinfo>`, `<lockinfo xmlns="DAV:">`,
		`<lockinfo xmlns="DAV:"><owner><exclusive/><write/></owner></lockinfo>`,
		`<lockinfo xmlns="DAV:"><lockscope><exclusive/></lockscope><locktype><write/></locktype></lockinfo>trailing`,
		`<lockinfo xmlns="DAV:"><lockscope><exclusive/></lockscope><lockscope><exclusive/></lockscope><locktype><write/></locktype></lockinfo>`} {
		r := httptest.NewRequest("LOCK", "http://example.test/item", strings.NewReader(body))
		if _, _, err := lockResponse(r); err == nil {
			t.Fatalf("accepted %q", body)
		}
	}
}

func TestInvalidLockLeavesDurableReceiptUnchanged(t *testing.T) {
	g, err := New("", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid lock reached upstream") }))
	if err != nil {
		t.Fatal(err)
	}
	g.stageZero = true
	g.stateDir = t.TempDir()
	if err := g.stage("/item"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(g.stateFile("/item"))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	g.ServeHTTP(response, httptest.NewRequest("LOCK", "http://example.test/item", strings.NewReader("invalid")))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status %d", response.Code)
	}
	after, err := os.ReadFile(g.stateFile("/item"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || g.pending["/item"].Locks != 0 {
		t.Fatalf("invalid request changed receipt: %s", after)
	}
}

func TestGrantedLockSurvivesRestartAndRefreshes(t *testing.T) {
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("lock reached upstream") })
	g, _ := New("", next)
	g.stageZero, g.stateDir = true, t.TempDir()
	if err := g.stage("/item"); err != nil {
		t.Fatal(err)
	}
	lock := httptest.NewRecorder()
	g.ServeHTTP(lock, httptest.NewRequest("LOCK", "/item", strings.NewReader(`<lockinfo xmlns="DAV:"><lockscope><exclusive/></lockscope><locktype><write/></locktype></lockinfo>`)))
	if lock.Code != 200 {
		t.Fatal(lock.Code, lock.Body.String())
	}
	restored, _ := New("", next)
	restored.stageZero, restored.stateDir = true, g.stateDir
	if err := restored.loadPending(); err != nil {
		t.Fatal(err)
	}
	for _, valid := range []bool{false, true} {
		r := httptest.NewRequest("LOCK", "/item", nil)
		token := "<urn:uuid:wrong>"
		if valid {
			token = lock.Header().Get("Lock-Token")
		}
		r.Header.Set("If", "("+token+")")
		response := httptest.NewRecorder()
		restored.ServeHTTP(response, r)
		if valid {
			if response.Code != 200 || response.Body.String() != lock.Body.String() || response.Header().Get("Lock-Token") != "" {
				t.Fatalf("refresh: %d %s", response.Code, response.Body.String())
			}
		} else if response.Code != 412 {
			t.Fatal(response.Code)
		}
	}
	if restored.pending["/item"].Locks != 1 {
		t.Fatal("refresh counted as new lock")
	}
	duplicate := httptest.NewRecorder()
	restored.ServeHTTP(duplicate, httptest.NewRequest("LOCK", "/item", strings.NewReader(`<lockinfo xmlns="DAV:"><lockscope><exclusive/></lockscope><locktype><write/></locktype></lockinfo>`)))
	if duplicate.Code != 423 {
		t.Fatal(duplicate.Code)
	}
}

func TestContentAndUnlockRequireGrantedToken(t *testing.T) {
	puts := 0
	g, _ := New("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		puts++
		if r.Header.Get("If") != "" {
			t.Error("synthetic predicate leaked")
		}
		w.WriteHeader(201)
	}))
	g.stageZero, g.stateDir = true, t.TempDir()
	if err := g.stage("/item"); err != nil {
		t.Fatal(err)
	}
	lock := httptest.NewRecorder()
	g.ServeHTTP(lock, httptest.NewRequest("LOCK", "/item", strings.NewReader(`<lockinfo xmlns="DAV:"><lockscope><exclusive/></lockscope><locktype><write/></locktype></lockinfo>`)))
	token := lock.Header().Get("Lock-Token")
	if lock.Code != 200 || token == "" {
		t.Fatal(lock.Code)
	}
	for _, method := range []string{"PUT", "UNLOCK"} {
		r := httptest.NewRequest(method, "/item", strings.NewReader("content"))
		r.Header.Set("If", "(<urn:uuid:wrong>)")
		r.Header.Set("Lock-Token", "<urn:uuid:wrong>")
		response := httptest.NewRecorder()
		g.ServeHTTP(response, r)
		expected := 412
		if method == "UNLOCK" {
			expected = 409
		}
		if response.Code != expected || puts != 0 || !g.pendingPath("/item") {
			t.Fatalf("%s: status=%d puts=%d", method, response.Code, puts)
		}
	}
	put := httptest.NewRequest("PUT", "/item", strings.NewReader("content"))
	put.Header.Set("If", "("+token+")")
	response := httptest.NewRecorder()
	g.ServeHTTP(response, put)
	if response.Code != 201 || puts != 1 || g.pendingPath("/item") {
		t.Fatalf("valid PUT: %d puts=%d", response.Code, puts)
	}
	for _, valid := range []bool{false, true} {
		unlock := httptest.NewRequest("UNLOCK", "/item", nil)
		value := "<urn:uuid:wrong>"
		if valid {
			value = token
		}
		unlock.Header.Set("Lock-Token", value)
		response = httptest.NewRecorder()
		g.ServeHTTP(response, unlock)
		expected := 409
		if valid {
			expected = 204
		}
		if response.Code != expected {
			t.Fatalf("post-publish unlock: %d", response.Code)
		}
	}
}

func TestExplicitEmptyPutWithLockPublishes(t *testing.T) {
	puts := 0
	g, _ := New("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		puts++
		if r.Method != "PUT" || r.ContentLength != 0 {
			t.Error("not an empty PUT")
		}
		w.WriteHeader(201)
	}))
	g.stageZero, g.stateDir = true, t.TempDir()
	if err := g.stage("/empty"); err != nil {
		t.Fatal(err)
	}
	if err := g.grantLock("/empty", "urn:uuid:test", "unused"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("PUT", "/empty", nil)
	r.Header.Set("If", "(<urn:uuid:test>)")
	response := httptest.NewRecorder()
	g.ServeHTTP(response, r)
	if response.Code != 201 || puts != 1 || g.pendingPath("/empty") {
		t.Fatalf("status=%d puts=%d", response.Code, puts)
	}
}
