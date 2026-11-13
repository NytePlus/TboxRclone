package main

import (
	"github.com/nyte/TboxRclone/internal/verifyutil"
	"testing"
)

func TestValidate(t *testing.T) {
	if _, err := verifyutil.Validate("http://127.0.0.1:8686/", "system-test-length-fault"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ origin, base string }{
		{"https://127.0.0.1/", "safe"},
		{"http://example.com/", "safe"},
		{"http://127.0.0.1/", "../escape"},
	} {
		if _, err := verifyutil.Validate(tc.origin, tc.base); err == nil {
			t.Fatalf("accepted unsafe input %#v", tc)
		}
	}
}
