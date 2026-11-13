package durablefile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReplacePublicationAndSyncFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "record.json")
	if err := Replace(target, ".record-", []byte("old"), SyncDir); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("directory sync failed")
	err := Replace(target, ".record-", []byte("new"), func(got string) error {
		if got != dir {
			t.Fatalf("sync dir %q", got)
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "new" {
		t.Fatalf("publication must survive uncertain durability: %q %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary file leaked: %v %v", entries, err)
	}
}

func TestReplaceRenameFailureCleansTemp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "directory")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := Replace(target, ".temp-", nil, func(string) error { t.Fatal("synced after failed rename"); return nil }); err == nil {
		t.Fatal("expected rename error")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary file leaked: %v %v", entries, err)
	}
}
