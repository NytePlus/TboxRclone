package smh

import (
	"encoding/json"
	"testing"
)

func TestIdentifierPreservesExactIntegersAndStrings(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`50285957`, "50285957"}, {`9007199254740993`, "9007199254740993"}, {`"opaque-id"`, "opaque-id"},
	} {
		var id Identifier
		if err := json.Unmarshal([]byte(tc.input), &id); err != nil || string(id) != tc.want {
			t.Fatalf("%s => %s: %v", tc.input, id, err)
		}
	}
	for _, input := range []string{`-1`, `1.5`, `1e3`, `true`, `null`, `{}`, `[]`, `""`} {
		var id Identifier
		if err := json.Unmarshal([]byte(input), &id); err == nil {
			t.Fatalf("invalid identifier accepted: %s", input)
		}
	}
}
