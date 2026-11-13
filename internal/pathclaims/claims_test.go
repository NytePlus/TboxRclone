package pathclaims

import "testing"

func TestConflict(t *testing.T) {
	for _, tc := range []struct {
		name, a, b            string
		treeA, treeB, overlap bool
	}{
		{"same", "/a", "/a", false, false, true},
		{"siblings", "/a", "/b", true, true, false},
		{"prefix is not ancestor", "/a", "/ab", true, true, false},
		{"file is not subtree", "/a", "/a/b", false, false, false},
		{"ancestor", "/a", "/a/b", true, false, true},
		{"descendant", "/a/b", "/a", false, true, true},
		{"root", "/", "/a", true, false, true},
		{"relative", "a", "a/b", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, wa := range []bool{false, true} {
				for _, wb := range []bool{false, true} {
					want := tc.overlap && (wa || wb)
					if got := Conflict(tc.a, wa, tc.treeA, tc.b, wb, tc.treeB); got != want {
						t.Fatalf("writers %v/%v: got %v want %v", wa, wb, got, want)
					}
					if got := Conflict(tc.b, wb, tc.treeB, tc.a, wa, tc.treeA); got != want {
						t.Fatal("asymmetric conflict")
					}
				}
			}
		})
	}
}
