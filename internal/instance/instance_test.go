package instance

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestOwnerSubprocess(t *testing.T) {
	dir := os.Getenv("TBOX_INSTANCE_TEST_DIR")
	if dir == "" {
		return
	}
	claim := func() error {
		if scope := os.Getenv("TBOX_INSTANCE_TEST_SCOPE"); scope != "" {
			return ClaimScope(os.Getenv("TBOX_INSTANCE_TEST_REGISTRY"), dir, scope)
		}
		return Claim(dir)
	}
	if err := claim(); err != nil {
		fmt.Fprintln(os.Stdout, "rejected")
		os.Exit(23)
	}
	fmt.Fprintln(os.Stdout, "owned")
	io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestProcessExclusionAndExitRelease(t *testing.T) {
	for _, kill := range []bool{false, true} {
		t.Run(fmt.Sprintf("kill=%t", kill), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "state")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOwnerSubprocess$")
			child.Env = append(os.Environ(), "TBOX_INSTANCE_TEST_DIR="+dir)
			input, err := child.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = child.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { input.Close(); child.Process.Kill(); child.Wait() }()
			line, err := bufio.NewReader(output).ReadString('\n')
			if err != nil || line != "owned\n" {
				t.Fatalf("owner startup %q: %v", line, err)
			}
			if err = Claim(dir); !errors.Is(err, ErrInUse) {
				t.Fatalf("second process admitted: %v", err)
			}
			// Mimic an unresolved operation: acquiring/releasing the instance lease
			// must neither erase it nor replace the lock inode.
			record := filepath.Join(dir, "pending.fixture")
			if err = os.WriteFile(record, []byte("unresolved"), 0600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(filepath.Join(dir, "instance.lock"))
			if err != nil {
				t.Fatal(err)
			}
			if kill {
				if err = child.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			} else {
				input.Close()
			}
			err = child.Wait()
			if !kill && err != nil {
				t.Fatal(err)
			}
			if err = Claim(dir); err != nil {
				t.Fatalf("cannot take ownership after exit: %v", err)
			}
			if err = Claim(filepath.Join(dir, ".")); err != nil {
				t.Fatalf("same-process Fs reuse: %v", err)
			}
			after, err := os.Stat(filepath.Join(dir, "instance.lock"))
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, after) {
				t.Fatal("lock inode replaced")
			}
			b, err := os.ReadFile(record)
			if err != nil || string(b) != "unresolved" {
				t.Fatal("recovery data changed")
			}
			probe := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOwnerSubprocess$")
			probe.Env = append(os.Environ(), "TBOX_INSTANCE_TEST_DIR="+dir)
			b, err = probe.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 23 || string(b) != "rejected\n" {
				t.Fatalf("child bypassed parent owner: %q %v", b, err)
			}
		})
	}
}

func TestUnsafeLockRejected(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "instance.lock")); err != nil {
		t.Fatal(err)
	}
	if err := Claim(dir); err == nil {
		t.Fatal("symlink lock accepted")
	}
	b, err := os.ReadFile(target)
	if err != nil || string(b) != "keep" {
		t.Fatal("symlink target changed")
	}
}

func TestScopeExcludesDifferentStateAndPersistsAfterExit(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "owners")
	state := filepath.Join(t.TempDir(), "state")
	scope := "https://fixture.invalid/library/space"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOwnerSubprocess$")
	child.Env = append(os.Environ(), "TBOX_INSTANCE_TEST_DIR="+state, "TBOX_INSTANCE_TEST_SCOPE="+scope, "TBOX_INSTANCE_TEST_REGISTRY="+registry)
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { input.Close(); child.Process.Kill(); child.Wait() }()
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || line != "owned\n" {
		t.Fatalf("owner %q: %v", line, err)
	}
	other := filepath.Join(t.TempDir(), "other-state")
	if err = ClaimScope(registry, other, scope); !errors.Is(err, ErrInUse) {
		t.Fatalf("different state bypassed live owner: %v", err)
	}
	input.Close()
	if err = child.Wait(); err != nil {
		t.Fatal(err)
	}
	if err = ClaimScope(registry, other, scope); err == nil {
		t.Fatal("new state hid old recovery directory after exit")
	}
	if err = ClaimScope(registry, state, scope); err != nil {
		t.Fatalf("original state cannot recover: %v", err)
	}
	if err = ClaimScope(registry, other, "https://fixture.invalid/library/other-space"); err != nil {
		t.Fatalf("unrelated space blocked: %v", err)
	}
}
