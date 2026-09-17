package sjtu

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nyte/TboxRclone/internal/faultproxy"
	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/recovery"
	"github.com/nyte/TboxRclone/internal/smh"
	"github.com/nyte/TboxRclone/internal/treebackup"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/object"
)

// observedHosts records only authorities from a real direct transfer, never URLs.
type observedHosts struct {
	sync.Mutex
	base  http.RoundTripper
	hosts map[string]bool
}

func (o *observedHosts) RoundTrip(r *http.Request) (*http.Response, error) {
	host := r.URL.Host
	if r.URL.Port() == "" {
		host += ":443"
	}
	o.Lock()
	o.hosts[host] = true
	o.Unlock()
	return o.base.RoundTrip(r)
}

// TestLiveMoveResponseLoss proves post-origin response loss against the real
// cloud. Context cancellation is not claimed as a process-kill or Finder test.
func TestLiveMoveResponseLoss(t *testing.T) {
	if os.Getenv("TBOX_LIVE_MOVE_FAULT") != "1" {
		t.Skip("opt-in live isolated MOVE fault experiment; stop service first")
	}
	root := os.Getenv("TBOX_AUTH_PATH")
	if smh.ValidatePath(root) != nil || !strings.HasPrefix(root, "codex-api-lab/") {
		t.Fatal("isolated lab root required")
	}
	var ids struct {
		Library string `json:"libraryId"`
		Space   string `json:"spaceId"`
	}
	b, err := os.ReadFile(os.Getenv("TBOX_SPACE_FILE"))
	if err != nil || json.Unmarshal(b, &ids) != nil {
		t.Fatal("private identity unavailable")
	}
	state := os.Getenv("TBOX_STATE_DIR")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	fsys, err := NewFs(ctx, "move-fault", root, configmap.Simple{
		"library_id": ids.Library, "space_id": ids.Space, "user_token_file": os.Getenv("TBOX_USER_TOKEN_FILE"),
		"state_dir": state, "ownership_dir": os.Getenv("TBOX_OWNERSHIP_DIR"), "lab_writes": "true", "lab_move": "true", "lab_overwrite": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	f := fsys.(*Fs)
	direct := http.DefaultTransport.(*http.Transport).Clone()
	direct.Proxy = nil
	defer direct.CloseIdleConnections()
	observed := &observedHosts{base: direct, hosts: map[string]bool{}}
	f.c.HTTP.Transport = observed
	independent, err := smh.New(f.c.Endpoint, ids.Library, ids.Space, "")
	if err != nil {
		t.Fatal(err)
	}
	independent.HTTP.Transport = direct
	if err = independent.UseUserToken(os.Getenv("TBOX_USER_TOKEN_FILE"), "1"); err != nil {
		t.Fatal(err)
	}
	directory := os.Getenv("TBOX_MOVE_FAULT_DIRECTORY") == "1"
	kind, operation := "file", "move"
	if directory {
		kind, operation = "directory", "dirmove"
	}
	type result struct {
		Directory           bool               `json:"directory"`
		Fixture             string             `json:"fixture"`
		Overwrite           bool               `json:"overwrite"`
		Cancel              bool               `json:"context_cancelled"`
		OriginStatus        int                `json:"origin_http"`
		VerifiedBeforeDrop  bool               `json:"independent_verified_before_drop"`
		StateAfterCall      string             `json:"state_after_call"`
		StateAfterReconcile string             `json:"state_after_reconcile"`
		PendingBoth         bool               `json:"pending_both_before_reconcile"`
		BackupMatches       bool               `json:"backup_matches"`
		Mutations           int                `json:"move_requests"`
		Pass                bool               `json:"pass"`
		Events              []faultproxy.Event `json:"events"`
	}
	report := struct {
		At      string    `json:"at"`
		Scope   string    `json:"scope"`
		Results []*result `json:"results"`
	}{At: time.Now().UTC().Format(time.RFC3339), Scope: "real backend MOVE response loss and context cancellation; not process kill, mount or Finder acceptance"}
	defer func() {
		data, _ := json.MarshalIndent(report, "", "  ")
		if p := os.Getenv("TBOX_MOVE_FAULT_REPORT"); p != "" {
			if os.WriteFile(p, append(data, '\n'), 0600) != nil {
				t.Error("cannot write fault report")
			}
		}
	}()
	for _, overwrite := range []bool{false, true} {
		if directory && overwrite {
			continue
		}
		for _, cancelCall := range []bool{false, true} {
			name := "new"
			if directory {
				name = "directory"
			}
			if overwrite {
				name = "overwrite"
			}
			if cancelCall {
				name += "-cancel"
			}
			if !t.Run(name, func(t *testing.T) {
				var id [8]byte
				if _, err := rand.Read(id[:]); err != nil {
					t.Fatal(err)
				}
				prefix := "move-fault-" + hex.EncodeToString(id[:])
				source, target := prefix+"-source", prefix+"-target"
				out := &result{Fixture: prefix, Overwrite: overwrite, Cancel: cancelCall, Directory: directory}
				report.Results = append(report.Results, out)
				payload := []byte("retained source for real post-origin MOVE fault\n")
				put := func(remote string, data []byte) {
					t.Helper()
					info := object.NewStaticObjectInfo(remote, time.Now(), int64(len(data)), true, nil, f)
					if _, err := f.Put(ctx, bytes.NewReader(data), info); err != nil {
						t.Fatal(err)
					}
				}
				f.c.HTTP.Transport = observed
				var move func(context.Context) error
				if directory {
					if err := f.Mkdir(ctx, source+"/empty"); err != nil {
						t.Fatal(err)
					}
					put(source+"/child", payload)
					put(source+"/zero", nil)
					move = func(ctx context.Context) error { return f.DirMove(ctx, f, source, target) }
				} else {
					put(source, payload)
					if overwrite {
						put(target, []byte("old target\n"))
					}
					src, err := f.NewObject(ctx, source)
					if err != nil {
						t.Fatal(err)
					}
					move = func(ctx context.Context) error { _, err := f.Move(ctx, src, target); return err }
				}
				observed.Lock()
				hosts := []string{}
				for h := range observed.hosts {
					hosts = append(hosts, h)
				}
				observed.Unlock()
				proxy, err := faultproxy.New(hosts, direct)
				if err != nil {
					t.Fatal(err)
				}
				server := httptest.NewServer(proxy)
				defer server.Close()
				defer proxy.Close()
				defer func() { out.Events = proxy.Events(0) }()
				rule := faultproxy.Rule{ID: "move-after-origin", Host: "pan.sjtu.edu.cn:443", Method: "PUT", PathPrefix: "/api/v1/" + kind + "/" + ids.Library + "/" + ids.Space + "/" + root + "/" + target, Nth: 1, Action: "hold_response"}
				if err = proxy.SetRules([]faultproxy.Rule{rule}); err != nil {
					t.Fatal(err)
				}
				pool, err := x509.SystemCertPool()
				if err != nil {
					t.Fatal(err)
				}
				if !pool.AppendCertsFromPEM(proxy.CAPEM()) {
					t.Fatal("invalid proxy CA")
				}
				proxyURL, _ := url.Parse(server.URL)
				transport := direct.Clone()
				transport.Proxy = http.ProxyURL(proxyURL)
				transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
				defer transport.CloseIdleConnections()
				f.c.HTTP.Transport = transport
				callCtx, callCancel := context.WithCancel(ctx)
				defer callCancel()
				done := make(chan error, 1)
				go func() { done <- move(callCtx) }()
				// Ensure no backend goroutine can outlive its test and transport.
				finished := false
				defer func() {
					if !finished {
						callCancel()
						proxy.Close()
						select {
						case <-done:
						case <-time.After(10 * time.Second):
							t.Error("move did not stop")
						}
					}
					f.c.HTTP.Transport = observed
				}()
				deadline := time.NewTimer(30 * time.Second)
				defer deadline.Stop()
				ticker := time.NewTicker(25 * time.Millisecond)
				defer ticker.Stop()
				var held uint64
			waiting:
				for {
					select {
					case err := <-done:
						finished = true
						t.Fatalf("move ended before held response: %v", err)
					case <-deadline.C:
						t.Fatal("no held response observed")
					case <-ticker.C:
						for _, event := range proxy.Events(0) {
							if event.Rule == rule.ID && event.Phase == "held" {
								held = event.Request
								out.OriginStatus = event.Status
								break waiting
							}
						}
					}
				}
				if out.OriginStatus < 200 || out.OriginStatus >= 300 {
					t.Fatal("origin move did not succeed", out.OriginStatus)
				}
				if err = verifyLiveMoveOutcome(ctx, independent, root+"/"+source, root+"/"+target, payload, directory); err != nil {
					t.Fatal(err)
				}
				out.VerifiedBeforeDrop = true
				if ready := os.Getenv("TBOX_MOVE_KILL_READY"); ready != "" && os.Getenv("TBOX_LIVE_MOVE_DEATH") == "1" {
					out.Events = proxy.Events(0)
					data, err := json.Marshal(out)
					if err != nil || os.WriteFile(ready, data, 0600) != nil {
						t.Fatal("cannot publish child readiness")
					}
					// Parent independently verifies the committed cloud bytes, then
					// kills this process while the response is still withheld.
					<-ctx.Done()
					t.Fatal("parent did not terminate the child")
				}
				if cancelCall {
					callCancel()
				}
				if err = proxy.Release(held, false); err != nil {
					t.Fatal(err)
				}
				select {
				case err = <-done:
					finished = true
				case <-time.After(30 * time.Second):
					t.Fatal("move did not finish after response loss")
				}
				if cancelCall {
					if err == nil {
						t.Fatal("cancelled operation reported success")
					}
				} else if err != nil {
					t.Fatal(err)
				}
				s, err := journal.Open(state)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				records, err := s.Records()
				if err != nil {
					t.Fatal(err)
				}
				var record *journal.Record
				for i := range records {
					if records[i].Kind == operation && records[i].Path == root+"/"+target {
						if record != nil {
							t.Fatal("duplicate move record")
						}
						record = &records[i]
					}
				}
				if record == nil {
					t.Fatal("move record missing")
				}
				out.StateAfterCall = record.State
				backup, err := s.Data(record)
				if err != nil {
					t.Fatal(err)
				}
				out.BackupMatches = liveMoveBackupMatches(backup, payload, directory)
				backup.Close()
				if !out.BackupMatches {
					t.Fatal("backup lost")
				}
				if cancelCall {
					out.PendingBoth = errors.Is(s.Pending(record.Scope, record.SourcePath), journal.ErrPending) && errors.Is(s.Pending(record.Scope, record.Path), journal.ErrPending)
					if directory {
						out.PendingBoth = out.PendingBoth && errors.Is(s.Pending(record.Scope, record.SourcePath+"/child"), journal.ErrPending) && errors.Is(s.Pending(record.Scope, record.Path+"/child"), journal.ErrPending)
					}
					if !out.PendingBoth || record.State != "MoveUnknown" {
						t.Fatal("unresolved reservations lost", record.State)
					}
				} else if record.State != "Committed" {
					t.Fatal(record.State)
				}
				if err = recovery.Reconcile(ctx, s, independent, record); err != nil {
					t.Fatal(err)
				}
				out.StateAfterReconcile = record.State
				if record.State != "Committed" {
					t.Fatal(record.State)
				}
				for _, path := range []string{record.SourcePath, record.Path} {
					if err = s.Pending(record.Scope, path); err != nil {
						t.Fatal(err)
					}
				}
				for _, event := range proxy.Events(0) {
					if event.Method == "PUT" && event.Phase == "received" {
						out.Mutations++
					}
				}
				if out.Mutations != 1 {
					t.Fatal("move replayed", out.Mutations)
				}
				out.Pass = true
			}) {
				return
			}
		}
	}
}

func liveMoveTree(payload []byte) map[string]treebackup.Entry {
	return map[string]treebackup.Entry{
		".": {Directory: true}, "empty": {Directory: true},
		"child": {Size: int64(len(payload)), SHA256: hashLiveMove(payload)},
		"zero":  {SHA256: hashLiveMove(nil)},
	}
}

func verifyLiveMoveOutcome(ctx context.Context, c *smh.Client, source, target string, payload []byte, directory bool) error {
	if _, err := c.Info(ctx, source); !smh.IsStatus(err, 404) {
		return errors.New("source absence not established")
	}
	if directory {
		return treebackup.Verify(ctx, c, target, liveMoveTree(payload))
	}
	item, err := c.Info(ctx, target)
	if err != nil {
		return err
	}
	reader, err := c.Open(ctx, target, item, 0, -1)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(reader)
	ce := reader.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	if !bytes.Equal(data, payload) {
		return errors.New("independent target mismatch")
	}
	return nil
}

func liveMoveBackupMatches(reader io.Reader, payload []byte, directory bool) bool {
	if !directory {
		data, err := io.ReadAll(reader)
		return err == nil && bytes.Equal(data, payload)
	}
	actual, err := treebackup.Manifest(reader)
	expected := liveMoveTree(payload)
	if err != nil || len(actual) != len(expected) {
		return false
	}
	for name, want := range expected {
		if got, ok := actual[name]; !ok || got != want {
			return false
		}
	}
	return true
}
