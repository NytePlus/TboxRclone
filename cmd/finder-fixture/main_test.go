package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFixtureContentAndNoClobber(t *testing.T) {
	root := filepath.Join(t.TempDir(), "fixture")
	if err := generate(root, 65537); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err = json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.Generator != "finder-fixture-v2" || len(m.Files) != 13 {
		t.Fatal("missing fixtures")
	}
	for _, e := range m.Files {
		data, err := os.ReadFile(filepath.Join(root, e.Path))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if int64(len(data)) != e.Size || hex.EncodeToString(sum[:]) != e.SHA256 {
			t.Errorf("bad manifest for %s", e.Path)
		}
		for i, v := range data {
			if v != byte(i) {
				t.Fatalf("wrong pattern at %s:%d", e.Path, i)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(root, "plain.bin"), []byte("preserve me"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := generate(root, 1); err == nil {
		t.Fatal("reused existing fixture directory")
	}
	data, _ := os.ReadFile(filepath.Join(root, "plain.bin"))
	if string(data) != "preserve me" {
		t.Fatal("overwrote previous data")
	}
}

func TestRejectSizeBeforeCreatingDirectory(t *testing.T) {
	for _, size := range []int64{-1, 1<<30 + 1} {
		root := filepath.Join(t.TempDir(), "fixture")
		if err := generate(root, size); err == nil {
			t.Fatal("accepted invalid size")
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("created invalid run directory")
		}
	}
}
