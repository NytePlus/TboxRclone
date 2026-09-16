package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRejectMissingNegativeSubcase(t *testing.T) {
	dir := t.TempDir()
	recording := filepath.Join(dir, "recording.mov")
	os.WriteFile(recording, []byte("fixture recording"), 0600)
	os.WriteFile(filepath.Join(dir, "log.txt"), []byte("fixture log"), 0600)
	test := scenario{ID: "ST-001-F", Evidence: dir, Recording: &recording}
	r := map[string]any{"run_id": "fixture", "scenario_id": test.ID, "versions": map[string]string{"client": "v", "backend": "v", "server": "v", "macos": "v", "finder": "v"}, "cases": []any{map[string]any{"id": "first", "status": "PASS", "observation": "test fixture", "artifacts": []string{"log.txt"}}}}
	b, _ := json.Marshal(r)
	os.WriteFile(filepath.Join(dir, "result.json"), b, 0600)
	if validateEvidence(test, []string{"first", "middle", "last"}) == nil {
		t.Fatal("accepted incomplete negative branches")
	}
	if e := validateEvidence(test, []string{"first"}); e != nil {
		t.Fatal(e)
	}
	os.Remove(filepath.Join(dir, "log.txt"))
	if validateEvidence(test, []string{"first"}) == nil {
		t.Fatal("accepted missing artifact")
	}
}
func TestRejectBarePass(t *testing.T) {
	if validateEvidence(scenario{ID: "ST-001-T", Evidence: t.TempDir()}, []string{"normal"}) == nil {
		t.Fatal("PASS without evidence")
	}
}
