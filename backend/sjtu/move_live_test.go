package sjtu

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/smh"
)

// TestLiveWebDAVFileMove is a protocol integration experiment, not Finder acceptance.
// It leaves generated fixtures and durable backups intact for inspection.
func TestLiveWebDAVFileMove(t *testing.T) {
	if os.Getenv("TBOX_LIVE_DAV_MOVE") != "1" {
		t.Skip("opt-in isolated live WebDAV move experiment")
	}
	root := os.Getenv("TBOX_AUTH_PATH")
	if smh.ValidatePath(root) != nil || !strings.HasPrefix(root, "codex-api-lab/") {
		t.Fatal("isolated lab root required")
	}
	var ids struct {
		Library string `json:"libraryId"`
		Space   string `json:"spaceId"`
	}
	raw, err := os.ReadFile(os.Getenv("TBOX_SPACE_FILE"))
	if err != nil || json.Unmarshal(raw, &ids) != nil {
		t.Fatal("private space identity unavailable")
	}
	c, err := smh.New("https://pan.sjtu.edu.cn", ids.Library, ids.Space, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.UseUserToken(os.Getenv("TBOX_USER_TOKEN_FILE"), "1"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	var id [8]byte
	if _, err = rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	prefix := "move-" + hex.EncodeToString(id[:])
	type event struct {
		Name string `json:"name"`
		Pass bool   `json:"pass"`
		HTTP int    `json:"http,omitempty"`
	}
	report := struct {
		At      string  `json:"at"`
		Scope   string  `json:"scope"`
		Root    string  `json:"root"`
		Fixture string  `json:"fixture"`
		Events  []event `json:"events"`
		Moves   int     `json:"committed_moves"`
	}{At: time.Now().UTC().Format(time.RFC3339), Scope: "real WebDAV and independent cloud reads; not Finder acceptance", Root: root, Fixture: prefix}
	defer func() {
		b, _ := json.MarshalIndent(report, "", "  ")
		if path := os.Getenv("TBOX_MOVE_REPORT"); path != "" {
			if err := os.WriteFile(path, append(b, '\n'), 0600); err != nil {
				t.Error("cannot write report")
			}
		}
	}()
	check := func(name string, ok bool) {
		t.Helper()
		report.Events = append(report.Events, event{Name: name, Pass: ok})
		if !ok {
			t.Fatal(name)
		}
	}
	client := &http.Client{Timeout: time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	const base = "http://127.0.0.1:8686/"
	request := func(name, method, path string, data []byte, destination, overwrite string, want int) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, base+prefix+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal("invalid fixture request")
		}
		if destination != "" {
			req.Header.Set("Destination", base+prefix+destination)
		}
		if overwrite != "" {
			req.Header.Set("Overwrite", overwrite)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal("loopback request failed")
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		report.Events = append(report.Events, event{Name: name, Pass: res.StatusCode == want, HTTP: res.StatusCode})
		if res.StatusCode != want {
			t.Fatalf("%s: HTTP %d, want %d", name, res.StatusCode, want)
		}
	}
	verify := func(path string, want []byte) {
		t.Helper()
		p := root + "/" + prefix + path
		item, err := c.Info(ctx, p)
		if want == nil {
			check("independent absence "+path, smh.IsStatus(err, 404))
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		reader, err := c.Open(ctx, p, item, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(reader)
		ce := reader.Close()
		check("independent content "+path, err == nil && ce == nil && bytes.Equal(got, want))
	}
	fresh, old := []byte("source content for journaled MOVE\n"), []byte("old target must survive rejected MOVE\n")
	request("create source", "PUT", "-source", fresh, "", "", 201)
	request("create target", "PUT", "-target", old, "", "", 201)
	request("overwrite F preserves both", "MOVE", "-source", nil, "-target", "F", 412)
	verify("-source", fresh)
	verify("-target", old)
	request("move overwrite existing", "MOVE", "-source", nil, "-target", "T", 204)
	verify("-source", nil)
	verify("-target", fresh)
	request("move to absent target", "MOVE", "-target", nil, "-final", "", 201)
	verify("-target", nil)
	verify("-final", fresh)
	request("create directory", "MKCOL", "-dir", nil, "", "", 201)
	request("create directory child", "PUT", "-dir/child", fresh, "", "", 201)
	request("directory fallback rejected", "MOVE", "-dir", nil, "-dir-next", "", 403)
	verify("-dir/child", fresh)
	verify("-dir-next", nil)
	records, err := (&journal.Store{Dir: os.Getenv("TBOX_STATE_DIR")}).Records()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if r.Kind == "move" && strings.HasPrefix(r.Path, root+"/"+prefix) {
			check("move committed with source identity", r.State == "Committed" && r.SourcePath != "")
			check("move backup hash", r.SHA256 == hashLiveMove(fresh))
			report.Moves++
		}
	}
	check("exactly two move records", report.Moves == 2)
}

func hashLiveMove(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
