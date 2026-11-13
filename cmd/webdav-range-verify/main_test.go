package main

import "testing"

func TestSpans(t *testing.T) {
	for _, tc := range []struct {
		size int64
		want [][2]int64
	}{
		{1, [][2]int64{{0, 0}, {0, 0}, {0, 0}}},
		{8192, [][2]int64{{0, 4095}, {2048, 6143}, {4096, 8191}}},
	} {
		got := spans(tc.size)
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Fatalf("spans(%d)[%d] = %v, want %v", tc.size, i, got[i], tc.want[i])
			}
		}
	}
}

func TestValidatedURL(t *testing.T) {
	if _, err := validatedURL("http://127.0.0.1:8686/", "dir/file.bin"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "../escape", `dir\\file`} {
		if _, err := validatedURL("http://127.0.0.1:8686/", value); err == nil {
			t.Fatalf("accepted unsafe path %q", value)
		}
	}
}
