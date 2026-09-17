package sjtu

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyte/TboxRclone/internal/faultproxy"
	"github.com/nyte/TboxRclone/internal/instance"
	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/recovery"
	"github.com/nyte/TboxRclone/internal/smh"
)

type readOnlyRecoveryTransport struct {
	base     http.RoundTripper
	rejected int
}

func (r *readOnlyRecoveryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.Path, "/api/") && req.Method != "GET" {
		r.rejected++
		return nil, errors.New("recovery attempted a cloud mutation")
	}
	return r.base.RoundTrip(req)
}

// TestLiveMoveProcessDeath kills a separate backend owner with SIGKILL after
// independent confirmation of the side effect, before it can receive a response.
func TestLiveMoveProcessDeath(t *testing.T) {
	if os.Getenv("TBOX_LIVE_MOVE_DEATH") != "1" {
		t.Skip("opt-in real process death experiment; stop service first")
	}
	root := os.Getenv("TBOX_AUTH_PATH")
	state := os.Getenv("TBOX_STATE_DIR")
	if smh.ValidatePath(root) != nil || !strings.HasPrefix(root, "codex-api-lab/") || !filepath.IsAbs(state) {
		t.Fatal("isolated root and absolute state required")
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(filepath.Dir(state), "move-kill-"+hex.EncodeToString(nonce[:])+".ready")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pattern := "^TestLiveMoveResponseLoss$/^new$"
	overwrite := os.Getenv("TBOX_MOVE_DEATH_OVERWRITE") == "1"
	directory := os.Getenv("TBOX_MOVE_FAULT_DIRECTORY") == "1"
	operation := "move"
	if directory {
		if overwrite {
			t.Fatal("directory overwrite is not supported")
		}
		pattern, operation = "^TestLiveMoveResponseLoss$/^directory$", "dirmove"
	}
	if overwrite {
		pattern = "^TestLiveMoveResponseLoss$/^overwrite$"
	}
	cmd := exec.Command(exe, "-test.run="+pattern, "-test.v", "-test.timeout=3m")
	cmd.Env = append(os.Environ(), "TBOX_LIVE_MOVE_FAULT=1", "TBOX_MOVE_KILL_READY="+ready)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	finished := false
	defer func() {
		if !finished {
			_ = cmd.Process.Kill()
			<-done
		}
	}()
	report := struct {
		Directory         bool               `json:"directory"`
		Overwrite         bool               `json:"overwrite"`
		At                string             `json:"at"`
		Scope             string             `json:"scope"`
		Fixture           string             `json:"fixture"`
		ReadyVerified     bool               `json:"child_verified_before_kill"`
		ParentVerified    bool               `json:"parent_verified_before_kill"`
		Signal            string             `json:"signal"`
		StateBefore       string             `json:"state_after_kill"`
		StateAfter        string             `json:"state_after_reconcile"`
		PendingBoth       bool               `json:"pending_both_after_kill"`
		BackupMatches     bool               `json:"backup_matches"`
		NewOwner          bool               `json:"new_process_claimed_ownership"`
		MutationsBefore   int                `json:"move_requests_before_kill"`
		MutationsRecovery int                `json:"mutation_attempts_during_recovery"`
		Pass              bool               `json:"pass"`
		Events            []faultproxy.Event `json:"proxy_events_before_kill"`
	}{Directory: directory, Overwrite: overwrite, At: time.Now().UTC().Format(time.RFC3339), Scope: "real cloud MOVE with SIGKILL of separate backend process; not Finder or host power loss"}
	defer func() {
		b, _ := json.MarshalIndent(report, "", "  ")
		if p := os.Getenv("TBOX_MOVE_DEATH_REPORT"); p != "" {
			if os.WriteFile(p, append(b, '\n'), 0600) != nil {
				t.Error("cannot save process report")
			}
		}
	}()
	var child struct {
		Fixture  string             `json:"fixture"`
		Verified bool               `json:"independent_verified_before_drop"`
		Events   []faultproxy.Event `json:"events"`
	}
	deadline := time.NewTimer(40 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
waiting:
	for {
		select {
		case <-done:
			finished = true
			t.Fatal("child exited before readiness; inspect retained test state")
		case <-deadline.C:
			t.Fatal("child did not reach held response")
		case <-ticker.C:
			b, err := os.ReadFile(ready)
			if err == nil && json.Unmarshal(b, &child) == nil {
				break waiting
			}
		}
	}
	if !strings.HasPrefix(child.Fixture, "move-fault-") || !child.Verified {
		t.Fatal("invalid child fact check")
	}
	report.Fixture = child.Fixture
	report.ReadyVerified = child.Verified
	report.Events = child.Events
	held := false
	for _, e := range child.Events {
		if e.Phase == "received" && e.Method == "PUT" {
			report.MutationsBefore++
		}
		if e.Phase == "held" && e.Rule == "move-after-origin" {
			held = true
		}
	}
	if report.MutationsBefore != 1 || !held {
		t.Fatal("no unique held move")
	}
	var ids struct {
		Library string `json:"libraryId"`
		Space   string `json:"spaceId"`
	}
	b, err := os.ReadFile(os.Getenv("TBOX_SPACE_FILE"))
	if err != nil || json.Unmarshal(b, &ids) != nil {
		t.Fatal("identity unavailable")
	}
	direct := http.DefaultTransport.(*http.Transport).Clone()
	direct.Proxy = nil
	defer direct.CloseIdleConnections()
	guard := &readOnlyRecoveryTransport{base: direct}
	c, err := smh.New("https://pan.sjtu.edu.cn", ids.Library, ids.Space, "")
	if err != nil {
		t.Fatal(err)
	}
	c.HTTP.Transport = guard
	if err = c.UseUserToken(os.Getenv("TBOX_USER_TOKEN_FILE"), "1"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	source, target := root+"/"+child.Fixture+"-source", root+"/"+child.Fixture+"-target"
	payload := []byte("retained source for real post-origin MOVE fault\n")
	if err = verifyLiveMoveOutcome(ctx, c, source, target, payload, directory); err != nil {
		t.Fatal(err)
	}
	report.ParentVerified = true
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		finished = true
	case <-time.After(10 * time.Second):
		t.Fatal("child did not terminate")
	}
	if err == nil {
		t.Fatal("child unexpectedly exited normally")
	}
	report.Signal = "SIGKILL"
	if err = instance.ClaimScope(os.Getenv("TBOX_OWNERSHIP_DIR"), state, c.Endpoint+"/"+c.Library+"/"+c.Space); err != nil {
		t.Fatal(err)
	}
	report.NewOwner = true
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
		if records[i].Kind == operation && records[i].Path == target {
			if record != nil {
				t.Fatal("duplicate move record")
			}
			record = &records[i]
		}
	}
	if record == nil {
		t.Fatal("missing move intent")
	}
	report.StateBefore = record.State
	if record.State != "MoveSent" {
		t.Fatal("wrong crash boundary", record.State)
	}
	report.PendingBoth = errors.Is(s.Pending(record.Scope, source), journal.ErrPending) && errors.Is(s.Pending(record.Scope, target), journal.ErrPending)
	if directory {
		report.PendingBoth = report.PendingBoth && errors.Is(s.Pending(record.Scope, source+"/child"), journal.ErrPending) && errors.Is(s.Pending(record.Scope, target+"/child"), journal.ErrPending)
	}
	if !report.PendingBoth {
		t.Fatal("reservations lost")
	}
	backup, err := s.Data(record)
	if err != nil {
		t.Fatal(err)
	}
	report.BackupMatches = liveMoveBackupMatches(backup, payload, directory)
	backup.Close()
	if !report.BackupMatches {
		t.Fatal("backup lost")
	}
	if err = recovery.Reconcile(ctx, s, c, record); err != nil {
		t.Fatal(err)
	}
	report.StateAfter = record.State
	report.MutationsRecovery = guard.rejected
	if record.State != "Committed" || guard.rejected != 0 {
		t.Fatal("recovery not read-only committed")
	}
	for _, p := range []string{source, target} {
		if err = s.Pending(record.Scope, p); err != nil {
			t.Fatal(err)
		}
	}
	report.Pass = true
}
