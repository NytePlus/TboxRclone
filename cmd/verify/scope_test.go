package main

import (
	"encoding/json"
	"os"
	"testing"
)

// The repository scope must remain the explicit user-approved core set.
func TestCoreAcceptanceScope(t *testing.T) {
	data, err := os.ReadFile("../../docs/test-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{}
	for _, id := range []string{"ST-001-T", "ST-001-F", "ST-002-T", "ST-003-T", "ST-003-F", "ST-004-T", "ST-005-T", "ST-006-T", "ST-007-T", "ST-007-F", "ST-008-T", "ST-008-F", "ST-009-T", "ST-010-T", "ST-011-T", "ST-011-F", "ST-012-T", "ST-013-T", "ST-013-F", "ST-014-T", "ST-014-F", "ST-015-T", "ST-015-F", "ST-017-T", "ST-018-F"} {
		wanted[id] = true
	}
	if len(m.Tests) != len(wanted) {
		t.Fatalf("want25 scenarios, got%d", len(m.Tests))
	}
	data, err = os.ReadFile("../../docs/acceptance-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases map[string][]string
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 25 {
		t.Fatalf("want25 case entries, got%d", len(cases))
	}
	for _, test := range m.Tests {
		if !wanted[test.ID] {
			t.Fatalf("unexpected or duplicate scenario%s", test.ID)
		}
		delete(wanted, test.ID)
		if len(cases[test.ID]) == 0 {
			t.Fatalf("missing subcases%s", test.ID)
		}
	}
	if len(cases["ST-011-F"]) != 1 || cases["ST-011-F"][0] != "nonempty_directory" {
		t.Fatal("directory race reintroduced into scope")
	}
	for _, id := range cases["ST-014-F"] {
		if id == "nfc_nfd" || id == "case_collision" {
			t.Fatal("excluded naming combination reintroduced")
		}
	}
	if _, ok := cases["ST-018-T"]; ok {
		t.Fatal("performance item reintroduced into system acceptance")
	}
}
