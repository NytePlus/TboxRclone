// Verify renders every acceptance branch and fails unless all have evidence-backed PASS status.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type scenario struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Conditions []string `json:"condition_ids"`
	Branch     string   `json:"branch"`
	Expected   string   `json:"expected"`
	Status     string   `json:"status"`
	Evidence   string   `json:"evidence_directory"`
	Recording  *string  `json:"recording_path"`
}
type manifest struct {
	Tests []scenario `json:"tests"`
}
type result struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type receipt struct {
	RunID      string            `json:"run_id"`
	ScenarioID string            `json:"scenario_id"`
	Versions   map[string]string `json:"versions"`
	Cases      []struct {
		ID          string   `json:"id"`
		Status      string   `json:"status"`
		Observation string   `json:"observation"`
		Artifacts   []string `json:"artifacts"`
	} `json:"cases"`
}

func validateEvidence(test scenario, required []string) error {
	if test.Recording == nil || !nonempty(*test.Recording) {
		return errors.New("missing native macOS recording")
	}
	b, err := os.ReadFile(filepath.Join(test.Evidence, "result.json"))
	if err != nil {
		return errors.New("missing result.json")
	}
	var r receipt
	if json.Unmarshal(b, &r) != nil || r.RunID == "" || r.ScenarioID != test.ID {
		return errors.New("invalid evidence identity")
	}
	for _, key := range []string{"client", "backend", "server", "macos", "finder"} {
		if r.Versions[key] == "" {
			return fmt.Errorf("missing version: %s", key)
		}
	}
	seen := map[string]bool{}
	for _, c := range r.Cases {
		if seen[c.ID] || c.Status != "PASS" || c.Observation == "" || len(c.Artifacts) == 0 {
			return errors.New("duplicate, incomplete or non-passing subcase")
		}
		seen[c.ID] = true
		for _, p := range c.Artifacts {
			if !filepath.IsLocal(p) || !nonempty(filepath.Join(test.Evidence, p)) {
				return errors.New("missing subcase artifact")
			}
		}
	}
	for _, id := range required {
		if !seen[id] {
			return fmt.Errorf("missing required subcase: %s", id)
		}
	}
	return nil
}

func run() int {
	source := flag.String("manifest", "test-manifest.json", "acceptance manifest")
	casesFile := flag.String("cases", "docs/acceptance-cases.json", "required subcase catalog")
	output := flag.String("output", "reports", "generated reports directory")
	flag.Parse()
	b, e := os.ReadFile(*source)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 2
	}
	var m manifest
	if e = json.Unmarshal(b, &m); e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 2
	}
	if len(m.Tests) != 36 {
		fmt.Fprintln(os.Stderr, "expected all 36 acceptance branches")
		return 2
	}
	caseData, e := os.ReadFile(*casesFile)
	var required map[string][]string
	if e != nil || json.Unmarshal(caseData, &required) != nil || len(required) != 36 {
		fmt.Fprintln(os.Stderr, "invalid subcase catalog")
		return 2
	}
	seen := map[string]bool{}
	results := []result{}
	passed := 0
	var matrix strings.Builder
	matrix.WriteString("# Generated acceptance coverage\n\nThis report does not substitute offline tests for live evidence. Negative subscenarios listed in each branch all remain required.\n\n| Condition | Scenario | Branch | Status |\n|---|---|---|---|\n")
	for _, test := range m.Tests {
		if seen[test.ID] || len(test.Conditions) != 1 || test.Expected == "" || test.Branch == "" {
			fmt.Fprintln(os.Stderr, "invalid or duplicate manifest entry", test.ID)
			return 2
		}
		seen[test.ID] = true
		if len(required[test.ID]) == 0 {
			fmt.Fprintln(os.Stderr, "missing subcases", test.ID)
			return 2
		}
		status := strings.ToUpper(test.Status)
		reason := ""
		switch status {
		case "PASS":
			// The manifest is an index of reviewed evidence, not an executable test result.
			// Require a recording and a detailed run record instead of accepting a bare PASS.
			if err := validateEvidence(test, required[test.ID]); err != nil {
				status = "BLOCKED"
				reason = err.Error()
			} else {
				passed++
			}
		case "FAIL", "BLOCKED", "NOT_RUN":
		default:
			fmt.Fprintln(os.Stderr, "invalid status", test.ID, status)
			return 2
		}
		results = append(results, result{test.ID, status, reason})
		fmt.Fprintf(&matrix, "| %s | %s | %s | %s |\n", test.Conditions[0], test.ID, strings.ReplaceAll(test.Branch, "|", "\\|"), status)
		for _, subcase := range required[test.ID] {
			fmt.Fprintf(&matrix, "| %s | %s/%s | required subcase | %s |\n", test.Conditions[0], test.ID, subcase, status)
		}
	}
	for i := 1; i <= 18; i++ {
		for _, branch := range []string{"T", "F"} {
			id := fmt.Sprintf("ST-%03d-%s", i, branch)
			if !seen[id] {
				fmt.Fprintln(os.Stderr, "missing", id)
				return 2
			}
		}
	}
	if e = os.MkdirAll(*output, 0755); e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 2
	}
	data, _ := json.MarshalIndent(results, "", "  ")
	for name, contents := range map[string][]byte{"acceptance.json": data, "coverage.md": []byte(matrix.String())} {
		if e = os.WriteFile(filepath.Join(*output, name), contents, 0644); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 2
		}
	}
	fmt.Printf("Acceptance: %d/%d PASS; report: %s\n", passed, len(results), *output)
	if passed != len(results) {
		return 1
	}
	return 0
}
func nonempty(p string) bool {
	s, e := os.Stat(p)
	return e == nil && s.Mode().IsRegular() && s.Size() > 0
}
func main() { os.Exit(run()) }
