package main

import "testing"

func TestValidateOrigin(t *testing.T) {
	if _, err := validateOrigin("http://127.0.0.1:8686/"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"https://127.0.0.1/", "http://example.com/", "http://127.0.0.1/?x=1"} {
		if _, err := validateOrigin(value); err == nil {
			t.Fatalf("accepted unsafe origin %q", value)
		}
	}
}
