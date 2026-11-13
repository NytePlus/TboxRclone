package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifySubtree(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(filepath.Join(target, "nested", "empty"), 0755); err != nil {
		t.Fatal(err)
	}
	content := []byte("finder content")
	if err := os.WriteFile(filepath.Join(target, "nested", "file.bin"), content, 0644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(content)
	f := fixture{
		Files:       []entry{{Path: "Tree/nested/file.bin", Size: int64(len(content)), SHA256: hex.EncodeToString(h[:])}},
		Directories: []string{"Tree", "Tree/nested", "Tree/nested/empty", "Other"},
	}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "fixture.json")
	if err = os.WriteFile(manifest, b, 0644); err != nil {
		t.Fatal(err)
	}
	r := verify(manifest, target, "Tree")
	if r.Status != "PASS" || r.VerifiedFiles != 1 || r.VerifiedDirectories != 2 {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestVerifyReportsContentMismatchAndUnsafePath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "file.bin"), []byte("wrong"), 0644); err != nil {
		t.Fatal(err)
	}
	f := fixture{Files: []entry{
		{Path: "Tree/file.bin", Size: 5, SHA256: "not-the-real-hash"},
		{Path: "Tree/../../escape.bin", Size: 0, SHA256: "irrelevant"},
	}}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "fixture.json")
	if err = os.WriteFile(manifest, b, 0644); err != nil {
		t.Fatal(err)
	}
	r := verify(manifest, target, "Tree")
	if r.Status != "FAIL" || len(r.Errors) != 2 {
		t.Fatalf("unexpected result: %+v", r)
	}
}
