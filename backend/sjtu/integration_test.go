package sjtu_test

import (
	"os"
	"strings"
	"testing"

	"github.com/nyte/TboxRclone/backend/sjtu"
	"github.com/rclone/rclone/fs/config"
	"github.com/rclone/rclone/fstest"
	"github.com/rclone/rclone/fstest/fstests"
)

// TestIntegration uses the complete upstream fixture lifecycle, never a flattened subtest regex.
// An explicit opt-in is necessary because fstests performs real remote mutations.
func TestIntegration(t *testing.T) {
	remote := os.Getenv("TBOX_LIVE_REMOTE")
	if remote == "" {
		t.Skip("BLOCKED: no authenticated isolated TBOX_LIVE_REMOTE; not a passing backend integration run")
	}
	_, root, ok := strings.Cut(remote, ":")
	if !ok || !strings.HasPrefix(root, "codex-api-lab/") {
		t.Fatal("live remote must be a named sjtu remote rooted at codex-api-lab/<run-id>")
	}
	if *fstest.RemoteName != "" {
		t.Fatal("use TBOX_LIVE_REMOTE, not -remote, to preserve isolation checks")
	}
	name, _, _ := strings.Cut(remote, ":")
	// Upstream fstests discovers testserver fixtures relative to the rclone
	// source tree, even when the selected remote needs no local test server.
	t.Chdir("../../third_party/rclone")
	fstest.Initialise()
	typ, found := config.FileGetValue(name, "type")
	if !found || typ != "sjtu" {
		t.Fatal("named remote must exist and have type sjtu; missing config is a failure")
	}
	fstests.Run(t, &fstests.Opt{RemoteName: remote, NilObject: (*sjtu.Object)(nil)})
}
