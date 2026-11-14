package transfer

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/nyte/TboxRclone/internal/sjtu/journal"
)

func TestSpoolChangedDuringRemoteVerification(t *testing.T) {
	for _, source := range []bool{false, true} {
		for _, lostConfirm := range []bool{false, true} {
			t.Run(fmt.Sprintf("source=%t/lostConfirm=%t", source, lostConfirm), func(t *testing.T) {
				m, c, s, r, payload := spoolFixture(t, source)
				defer s.Close()
				local := filepath.Join(s.Dir, r.ID+".data")
				if source {
					local = r.Source.Path
				}
				injected := false
				m.onContentRead = func() {
					injected = true
					changed := append([]byte(nil), payload...)
					changed[0] ^= 0xff
					if err := os.WriteFile(local, changed, 0600); err != nil {
						t.Error(err)
					}
					if source {
						if err := os.Chtimes(local, r.Source.Mtime, r.Source.Mtime); err != nil {
							t.Error(err)
						}
					}
				}
				m.dropConfirm = lostConfirm
				err := Start(context.Background(), s, c, r)
				if lostConfirm {
					if err == nil {
						t.Fatal("confirm response was not lost")
					}
					err = Resume(context.Background(), s, c, r)
				}
				if !injected || err == nil || r.State == "Committed" {
					t.Fatalf("changed spool accepted: injected=%t state=%s err=%v", injected, r.State, err)
				}
				if m.confirm != 1 {
					t.Fatal("confirmation replayed", m.confirm)
				}
				if err := s.PruneCommitted(context.Background(), r.ID); err == nil {
					t.Fatal("changed spool released")
				}
				if _, err := os.Stat(local); err != nil {
					t.Fatal("local content lost", err)
				}
			})
		}
	}
}

func TestSourceResumeRejectsChangedSource(t *testing.T) {
	for _, confirmLost := range []bool{false, true} {
		for _, change := range []string{"size", "missing", "replacement", "digest"} {
			t.Run(fmt.Sprintf("confirmLost=%t/%s", confirmLost, change), func(t *testing.T) {
				m, c, s, r, payload := spoolFixture(t, true)
				m.dropPart, m.dropConfirm = !confirmLost, confirmLost
				if err := Start(context.Background(), s, c, r); err == nil {
					t.Fatal("fault not injected")
				}
				source := r.Source.Path
				if err := s.PruneCommitted(context.Background(), r.ID); err == nil {
					t.Fatal("ambiguous source released")
				}
				if _, err := os.Stat(source); err != nil {
					t.Fatal("source lost", err)
				}
				dir := s.Dir
				s.Close()
				var err error
				s, err = journal.Open(dir)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				records, err := s.Records()
				if err != nil {
					t.Fatal(err)
				}
				r = &records[0]
				switch change {
				case "size":
					err = os.WriteFile(source, []byte("changed"), 0600)
				case "missing":
					err = os.Remove(source)
				case "replacement":
					if err = os.Rename(source, source+".original"); err != nil {
						t.Fatal(err)
					}
					err = os.WriteFile(source, payload, 0600)
				case "digest":
					payload[0] ^= 0xff
					err = os.WriteFile(source, payload, 0600)
				}
				if err == nil && (change == "replacement" || change == "digest") {
					err = os.Chtimes(source, r.Source.Mtime, r.Source.Mtime)
				}
				if err != nil {
					t.Fatal(err)
				}
				before, initBefore, renewBefore := m.confirm, m.init, m.renew
				putsBefore := maps.Clone(m.puts)
				stateBefore := r.State
				if err = Resume(context.Background(), s, c, r); err == nil {
					t.Fatal("changed source resumed")
				}
				if m.confirm != before || m.init != initBefore || m.renew != renewBefore || !maps.Equal(m.puts, putsBefore) || r.State != stateBefore {
					t.Fatal("invalid source caused network mutation or state transition")
				}
				reloaded, err := s.Records()
				if err != nil || len(reloaded) != 1 || reloaded[0].State != stateBefore {
					t.Fatal("failed recovery changed persisted state", err)
				}
				if err := s.PruneCommitted(context.Background(), r.ID); err == nil {
					t.Fatal("failed recovery permitted source release")
				}
			})
		}
	}
}

func TestSpoolRemoteMismatchRetainsContent(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(fmt.Sprintf("source=%t", source), func(t *testing.T) {
			m, c, s, r, _ := spoolFixture(t, source)
			defer s.Close()
			m.dropConfirm = true
			if err := Start(context.Background(), s, c, r); err == nil {
				t.Fatal("fault not injected")
			}
			m.mu.Lock()
			m.parts[1][0] ^= 0xff
			m.mu.Unlock()
			if err := Resume(context.Background(), s, c, r); err == nil {
				t.Fatal("remote corruption accepted")
			}
			if r.State == "Committed" {
				t.Fatal("unverified commit")
			}
			if err := s.PruneCommitted(context.Background(), r.ID); err == nil {
				t.Fatal("unverified content released")
			}
			f, err := s.Data(r)
			if err != nil {
				t.Fatal("spool lost", err)
			}
			f.Close()
		})
	}
}

func TestSpoolMissingPartResume(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(fmt.Sprintf("source=%t", source), func(t *testing.T) {
			m, c, s, r, _ := spoolFixture(t, source)
			m.dropPart = true
			if err := Start(context.Background(), s, c, r); err == nil {
				t.Fatal("fault not injected")
			}
			// Model a request that never reached storage, instead of a lost receipt.
			m.mu.Lock()
			delete(m.parts, 2)
			m.mu.Unlock()
			dir := s.Dir
			s.Close()
			var err error
			s, err = journal.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			records, err := s.Records()
			if err != nil {
				t.Fatal(err)
			}
			r = &records[0]
			if err = Resume(context.Background(), s, c, r); err != nil {
				t.Fatal(err)
			}
			if r.State != "Committed" || m.init != 1 || m.confirm != 1 || m.puts[1] != 1 || m.puts[2] != 2 || m.puts[3] != 1 {
				t.Fatal("incorrect missing-part recovery", r.State, m.puts)
			}
		})
	}
}
