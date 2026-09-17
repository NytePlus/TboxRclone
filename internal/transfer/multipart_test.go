package transfer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/smh"
)

type remote struct {
	mu                                       sync.Mutex
	parts                                    map[int][]byte
	puts                                     map[int]int
	init, confirm, renew                     int
	published                                bool
	dropPart, dropConfirm, badRenew, badPart bool
	server                                   *httptest.Server
}

func fixture(t *testing.T, contents ...[]byte) (*remote, *smh.Client, *journal.Store, *journal.Record, []byte) {
	t.Helper()
	m := &remote{parts: map[int][]byte{}, puts: map[int]int{}}
	payload := make([]byte, 2*PartSize+13)
	for i := range payload {
		payload[i] = byte(i*17 + i/97)
	}
	if len(contents) > 0 {
		payload = contents[0]
	}
	m.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		q := req.URL.Query()
		switch {
		case req.URL.Path == "/data" && req.Method == "PUT":
			if q.Get("access_token") != "" || q.Get("uploadId") != "upload" || req.Header.Get("Authorization") != "signed" {
				t.Error("incorrect data plane identity")
			}
			n, _ := strconv.Atoi(q.Get("partNumber"))
			m.puts[n]++
			b, e := io.ReadAll(req.Body)
			if e != nil {
				w.WriteHeader(400)
				return
			}
			m.parts[n] = b
			if m.dropPart && n == 2 {
				m.dropPart = false
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			}
			w.Header().Set("ETag", fmt.Sprintf(`"part%d"`, n))
		case q.Has("multipart") || q.Has("renew"):
			if q.Has("multipart") {
				m.init++
			} else {
				m.renew++
			}
			var b struct {
				Range []int `json:"partNumberRange"`
			}
			if json.NewDecoder(req.Body).Decode(&b) != nil || len(b.Range) == 0 {
				t.Error("missing part range")
				w.WriteHeader(400)
				return
			}
			u := smh.Upload{Key: "K", UploadID: "upload", Domain: m.server.URL, Path: "/data", Parts: map[string]smh.PartSignature{}}
			for _, n := range b.Range {
				u.Parts[strconv.Itoa(n)] = smh.PartSignature{Headers: map[string]string{"Authorization": "signed"}}
			}
			if q.Has("renew") && m.badRenew {
				u.UploadID = "different"
			}
			json.NewEncoder(w).Encode(u)
		case q.Has("upload"):
			if q.Get("no_upload_part_info") != "1" {
				t.Error("unsupported status query")
			}
			status := smh.UploadStatus{UploadID: "upload", Path: []string{"codex-api-lab", "run", "file"}, Confirmed: m.published}
			if m.published {
				status.UploadID = ""
			} // Deployed API clears uploadId after confirmation.
			for n, b := range m.parts {
				tag := fmt.Sprintf(`"part%d"`, n)
				if m.badPart {
					tag = "changed"
				}
				status.Parts = append(status.Parts, smh.UploadedPart{Number: n, Size: smh.Int64(len(b)), ETag: tag})
			}
			json.NewEncoder(w).Encode(status)
		case q.Has("confirm"):
			m.confirm++
			var actual []byte
			for n := 1; n <= 3; n++ {
				actual = append(actual, m.parts[n]...)
			}
			if !bytes.Equal(actual, payload) {
				t.Error("published incomplete or incorrect file")
				w.WriteHeader(400)
				return
			}
			m.published = true
			if m.dropConfirm {
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			}
			json.NewEncoder(w).Encode(smh.Item{Path: []string{"codex-api-lab", "run", "file"}, Size: smh.Int64(len(payload))})
		case strings.Contains(req.URL.Path, "/directory/"):
			if !m.published {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(smh.Item{Type: "file", ETag: "opaque-multipart", Size: smh.Int64(len(payload))})
		case req.Method == "GET":
			w.Header().Set("ETag", `"opaque-multipart"`)
			for n := 1; n <= 3; n++ {
				w.Write(m.parts[n])
			}
		default:
			t.Errorf("unexpected request %s", req.Method)
			w.WriteHeader(400)
		}
	}))
	t.Cleanup(m.server.Close)
	c := &smh.Client{Endpoint: m.server.URL, Library: "l", Space: "s", HTTP: m.server.Client(), Token: func(context.Context) (string, error) { return "secret", nil }}
	s, e := journal.Open(filepath.Join(t.TempDir(), "state"))
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.Prepare(context.Background(), c.Endpoint+"/l/s", "codex-api-lab/run/file", bytes.NewReader(payload), int64(len(payload)), 16<<20)
	if e != nil {
		t.Fatal(e)
	}
	return m, c, s, r, payload
}
func TestMultipartRoundTrip(t *testing.T) {
	m, c, s, r, _ := fixture(t)
	defer s.Close()
	if e := Start(context.Background(), s, c, r); e != nil {
		t.Fatal(e)
	}
	if r.State != "Committed" || m.init != 1 || m.confirm != 1 || m.renew != 0 {
		t.Fatalf("%s init=%d confirm=%d", r.State, m.init, m.confirm)
	}
	for _, p := range r.Parts {
		if p.ETag == "" || p.SHA256 == "" {
			t.Fatal("missing durable part identity")
		}
	}
	b, e := os.ReadFile(filepath.Join(s.Dir, r.ID+".json"))
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(b, []byte("signed")) || bytes.Contains(b, []byte("secret")) {
		t.Fatal("credential persisted")
	}
}
func TestMultipartLostPartAckSurvivesRestart(t *testing.T) {
	m, c, s, r, payload := fixture(t)
	m.dropPart = true
	if e := Start(context.Background(), s, c, r); e == nil {
		t.Fatal("lost acknowledgement accepted")
	}
	if r.State != "Uploading" || m.confirm != 0 {
		t.Fatal("premature confirmation")
	}
	dir := s.Dir
	s.Close()
	s, e := journal.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	records, e := s.Records()
	if e != nil || len(records) != 1 {
		t.Fatal(e)
	}
	r = &records[0]
	m.mu.Lock()
	acknowledged := map[int]int{}
	for _, part := range r.Parts {
		if part.ETag != "" {
			acknowledged[part.Number] = m.puts[part.Number]
		}
	}
	m.mu.Unlock()
	if e = Resume(context.Background(), s, c, r); e != nil {
		t.Fatal(e)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for n, before := range acknowledged {
		if m.puts[n] != before {
			t.Errorf("acknowledged part %d was retransmitted", n)
		}
	}
	if r.State != "Committed" || m.init != 1 || m.confirm != 1 || m.puts[2] != 2 {
		t.Fatal("incorrect session replay", m.init, m.confirm, m.puts)
	}
	data, e := s.Data(r)
	if e != nil {
		t.Fatal(e)
	}
	defer data.Close()
	b, _ := io.ReadAll(data)
	if !bytes.Equal(b, payload) {
		t.Fatal("spool changed")
	}
}
func TestMultipartLostConfirmReconcilesWithoutReplay(t *testing.T) {
	m, c, s, r, _ := fixture(t)
	defer s.Close()
	m.dropConfirm = true
	if e := Start(context.Background(), s, c, r); e == nil || r.State != "Unknown" {
		t.Fatal("lost confirm accepted", e, r.State)
	}
	if e := Resume(context.Background(), s, c, r); e != nil {
		t.Fatal(e)
	}
	if r.State != "Committed" || m.init != 1 || m.confirm != 1 {
		t.Fatal("replayed confirmation")
	}
}
func TestMultipartRejectsChangedRenewal(t *testing.T) {
	m, c, s, r, _ := fixture(t)
	defer s.Close()
	m.dropPart = true
	if e := Start(context.Background(), s, c, r); e == nil {
		t.Fatal("expected interrupted upload")
	}
	m.mu.Lock()
	m.badRenew = true
	before := 0
	for _, n := range m.puts {
		before += n
	}
	m.mu.Unlock()
	if e := Resume(context.Background(), s, c, r); e == nil {
		t.Fatal("changed session accepted")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	after := 0
	for _, n := range m.puts {
		after += n
	}
	if after != before || m.confirm != 0 {
		t.Fatal("mutated changed session")
	}
}

func TestResumableUploadSizeBoundaries(t *testing.T) {
	for _, payload := range [][]byte{{}, {1}, []byte("tiny"), make([]byte, PartSize-1), make([]byte, PartSize), make([]byte, PartSize+1)} {
		t.Run(fmt.Sprint(len(payload)), func(t *testing.T) {
			m, c, s, r, _ := fixture(t, payload)
			defer s.Close()
			if e := Start(context.Background(), s, c, r); e != nil {
				t.Fatal(e)
			}
			if r.State != "Committed" || len(r.Parts) != max(1, (len(payload)+int(PartSize)-1)/int(PartSize)) || r.UploadID == "" || m.init != 1 || m.confirm != 1 || m.renew != 0 {
				t.Fatal("small file did not use resumable session")
			}
		})
	}
}

func TestMultipartRecoveryFailsClosed(t *testing.T) {
	for _, mode := range []string{"scope", "spool", "part hash", "remote part", "missing session"} {
		t.Run(mode, func(t *testing.T) {
			m, c, s, r, _ := fixture(t)
			defer s.Close()
			m.dropPart = true
			if e := Start(context.Background(), s, c, r); e == nil {
				t.Fatal("expected interruption")
			}
			switch mode {
			case "scope":
				r.Scope = "other"
			case "spool":
				if e := os.WriteFile(filepath.Join(s.Dir, r.ID+".data"), []byte("corrupt"), 0600); e != nil {
					t.Fatal(e)
				}
			case "part hash":
				r.Parts[0].SHA256 = "wrong"
			case "remote part":
				m.mu.Lock()
				m.badPart = true
				for n := range m.parts {
					r.Parts[n-1].ETag = fmt.Sprintf(`"part%d"`, n)
				}
				m.mu.Unlock()
			case "missing session":
				r.UploadID = ""
			}
			if e := Resume(context.Background(), s, c, r); e == nil {
				t.Fatal("unsafe recovery accepted")
			}
			if m.confirm != 0 || m.init != 1 {
				t.Fatal("reinitialized or confirmed unsafe upload")
			}
		})
	}
}
