package sjtu

import (
	"path/filepath"
	"testing"
)

func TestWriteRootBindsStateToConfiguredRemote(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	root, err := claimWriteRoot(dir, "mac_cloud")
	if err != nil || root != "mac_cloud" {
		t.Fatal(root, err)
	}
	for _, candidate := range []string{"mac_cloud/child", ""} {
		got, err := claimWriteRoot(dir, candidate)
		if err != nil || got != root {
			t.Fatalf("candidate %q changed boundary: %q %v", candidate, got, err)
		}
	}
	if _, err = claimWriteRoot(dir, "other"); err == nil {
		t.Fatal("disjoint remote root reused the state directory")
	}
}

func TestWriteRootRequiresExplicitInitialRemote(t *testing.T) {
	if _, err := claimWriteRoot(filepath.Join(t.TempDir(), "state"), ""); err == nil {
		t.Fatal("account root established a write boundary")
	}
}
