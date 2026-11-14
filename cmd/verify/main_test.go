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

func TestFinalStateWithoutRecording(t *testing.T) {
	dir := t.TempDir()
	test := scenario{ID: "ST-001-T", Evidence: dir}
	if err := os.WriteFile(filepath.Join(dir, "final.png"), []byte("fixture image"), 0600); err != nil {
		t.Fatal(err)
	}
	c := map[string]any{"id": "normal", "status": "PASS", "observation": "native final state and independent content reviewed", "artifacts": []string{"final.png"}, "final_state_image": "final.png", "final_state_reviewed": true}
	r := map[string]any{"run_id": "fixture", "scenario_id": test.ID, "versions": map[string]string{"client": "v", "backend": "v", "server": "v", "macos": "v", "finder": "v"}, "cases": []any{c}}
	write := func() {
		b, e := json.Marshal(r)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, "result.json"), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	write()
	if err := validateEvidence(test, []string{"normal"}); err != nil {
		t.Fatal(err)
	}
	c["final_state_reviewed"] = false
	write()
	if validateEvidence(test, []string{"normal"}) == nil {
		t.Fatal("unreviewed image accepted")
	}
	c["final_state_reviewed"] = true
	c["final_state_image"] = "missing.png"
	write()
	if validateEvidence(test, []string{"normal"}) == nil {
		t.Fatal("missing image accepted")
	}
}
