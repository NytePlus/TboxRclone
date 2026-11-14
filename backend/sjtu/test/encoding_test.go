package test

import (
	"context"
	"github.com/rclone/rclone/fs/object"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nyte/TboxRclone/internal/sjtu/smh"
	"github.com/rclone/rclone/lib/encoder"
)

func TestCloudNameEncoding(t *testing.T) {
	names := []string{"plain", "中文", `?"<>:*|\`, "invalid\xfe", "？", "＼", "‛", " leading", "trailing ", "' % + &", ".", ".."}
	seen := map[string]string{}
	f := &Fs{BackendRoot: "codex-api-lab/" + encoder.Standard.Encode("root?"), Opt: Options{Enc: DefaultEncoding}}
	for _, raw := range names {
		standard := encoder.Standard.Encode(raw)
		cloud, err := f.FullPath(standard)
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		name := cloud[strings.LastIndex(cloud, "/")+1:]
		if !utf8.ValidString(cloud) || strings.ContainsAny(name, `?"<>:*|\`) {
			t.Fatalf("unsafe cloud name %q", name)
		}
		if old, ok := seen[name]; ok {
			t.Fatalf("collision: %q and %q", old, raw)
		}
		seen[name] = raw
		if got := f.Opt.Enc.ToStandardName(name); got != standard {
			t.Fatalf("roundtrip %q: %q != %q", raw, got, standard)
		}
		if !strings.Contains(cloud, "root？/") {
			t.Fatalf("root not encoded: %q", cloud)
		}
	}
	for _, invalid := range []string{"/x", "x/", "x//y", "../x", "x/../y", "./x"} {
		if _, err := f.FullPath(invalid); err == nil {
			t.Fatalf("accepted traversal/ambiguous path %q", invalid)
		}
	}
	if smh.ValidatePath("invalid\xfe") == nil {
		t.Fatal("raw SDK accepted invalid UTF-8")
	}
}

func TestNilObjectString(t *testing.T) {
	var o *Object
	if got := o.String(); got != "<nil>" {
		t.Fatal(got)
	}
}

// Account-root construction is allowed for reads, but cannot establish a write
// boundary even with every experimental switch enabled.
func TestAccountRootRefusesMutations(t *testing.T) {
	f := &Fs{Opt: Options{Enc: DefaultEncoding, LabWrites: true, LabMove: true, LabDelete: true, LabOverwrite: true}}
	ctx := context.Background()
	if err := f.Mkdir(ctx, "personal/new"); err == nil {
		t.Fatal("Mkdir allowed")
	}
	if err := f.Rmdir(ctx, "personal/old"); err == nil {
		t.Fatal("Rmdir allowed")
	}
	o := &Object{Backend: f, RemotePath: "personal/file"}
	if err := o.Remove(ctx); err == nil {
		t.Fatal("Remove allowed")
	}
	if _, err := f.Move(ctx, o, "personal/new"); err == nil {
		t.Fatal("Move allowed")
	}
	if err := f.DirMove(ctx, f, "personal/old", "personal/new"); err == nil {
		t.Fatal("DirMove allowed")
	}
	src := object.NewStaticObjectInfo(o.RemotePath, time.Now(), 1, true, nil, f)
	if _, err := f.Put(ctx, strings.NewReader("x"), src); err == nil {
		t.Fatal("Put allowed")
	}
}

func TestWriteScopeUsesResolvedPath(t *testing.T) {
	for _, root := range []string{"mac_cloud", "team/managed"} {
		f := &Fs{BackendRoot: root, Opt: Options{Enc: DefaultEncoding, LabWrites: true}}
		if err := f.WriteAllowed("file"); err != nil {
			t.Fatal(err)
		}
	}
	for _, remote := range []string{"", "personal/file", "codex-api-lab", "codex-api-lab/../personal/file", "codex-api-lab//file"} {
		f := &Fs{Opt: Options{Enc: DefaultEncoding, LabWrites: true}}
		if f.WriteAllowed(remote) == nil {
			t.Fatalf("allowed %q", remote)
		}
	}
}
