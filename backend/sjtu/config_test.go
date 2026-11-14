package sjtu

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rclone/rclone/fs/config/configmap"
)

func TestRuntimePolicyComesFromBackendConfig(t *testing.T) {
	dir := t.TempDir()
	fsys, err := NewFs(context.Background(), "configured", "", configmap.Simple{
		"library_id": "library", "space_id": "space", "token_file": filepath.Join(dir, "token"),
		"state_dir": filepath.Join(dir, "state"), "ownership_dir": filepath.Join(dir, "owners"),
		"request_timeout": "17s", "read_retries": "1", "read_retry_delay": "350ms",
		"transfer_retries": "2", "transfer_retry_delay": "3s", "multipart_part_size": "8M",
		"multipart_concurrency": "7", "multipart_batch_size": "23", "max_active_mutations": "6",
	})
	if err != nil {
		t.Fatal(err)
	}
	f := fsys.(*Fs)
	if f.Client.HTTP.Timeout != 17*time.Second || f.Client.ReadRetries != 1 || f.Client.ReadRetryDelay != 350*time.Millisecond {
		t.Fatalf("read policy not applied: %+v", f.Client)
	}
	if f.Client.TransferRetries != 2 || f.Client.TransferRetryDelay != 3*time.Second {
		t.Fatalf("transfer retry policy not applied: %+v", f.Client)
	}
	if f.Client.MultipartPartSize != 8<<20 || f.Client.MultipartWorkers != 7 || f.Client.MultipartBatchSize != 23 || f.Opt.MaxActiveMutations != 6 {
		t.Fatalf("multipart/mutation policy not applied: client=%+v options=%+v", f.Client, f.Opt)
	}
}

func TestRuntimePolicyRejectsUnsafeRanges(t *testing.T) {
	for key, value := range map[string]string{
		"read_retries": "11", "transfer_retries": "11", "multipart_part_size": "512K",
		"multipart_concurrency": "33", "multipart_batch_size": "51", "max_active_mutations": "65",
	} {
		t.Run(key, func(t *testing.T) {
			dir := t.TempDir()
			values := configmap.Simple{
				"library_id": "library", "space_id": "space", "token_file": filepath.Join(dir, "token"),
				"state_dir": filepath.Join(dir, "state"), "ownership_dir": filepath.Join(dir, "owners"), key: value,
			}
			if _, err := NewFs(context.Background(), "invalid", "", values); err == nil {
				t.Fatal("unsafe value accepted")
			}
		})
	}
}
