package sjtu

import (
	"context"
	"github.com/rclone/rclone/fs/object"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nyte/TboxRclone/internal/smh"
	"github.com/rclone/rclone/lib/encoder"
)

func TestCloudNameEncoding(t *testing.T) {
	names := []string{"plain", "中文", `?"<>:*|\`, "invalid\xfe", "？", "＼", "‛", " leading", "trailing ", "' % + &", ".", ".."}
	seen := map[string]string{}
	f := &Fs{root: "codex-api-lab/" + encoder.Standard.Encode("root?"), opt: Options{Enc: defaultEncoding}}
	for _, raw := range names {
		standard := encoder.Standard.Encode(raw)
		cloud, err := f.full(standard)
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
		if got := f.opt.Enc.ToStandardName(name); got != standard {
			t.Fatalf("roundtrip %q: %q != %q", raw, got, standard)
		}
		if !strings.Contains(cloud, "root？/") {
			t.Fatalf("root not encoded: %q", cloud)
		}
	}
	for _, invalid := range []string{"/x", "x/", "x//y", "../x", "x/../y", "./x"} {
		if _, err := f.full(invalid); err == nil {
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

// Account-root construction is allowed; every mutation outside the lab is rejected
// before using a client or journal, even with every experimental switch enabled.
func TestAccountRootRefusesNonLabMutations(t *testing.T) {
	f := &Fs{opt: Options{Enc: defaultEncoding, LabWrites: true, LabMove: true, LabDelete: true, LabOverwrite: true}}
	ctx := context.Background()
	if err := f.Mkdir(ctx, "personal/new"); err == nil {
		t.Fatal("Mkdir allowed")
	}
	if err := f.Rmdir(ctx, "personal/old"); err == nil {
		t.Fatal("Rmdir allowed")
	}
	o := &Object{f: f, remote: "personal/file"}
	if err := o.Remove(ctx); err == nil {
		t.Fatal("Remove allowed")
	}
	if _, err := f.Move(ctx, o, "personal/new"); err == nil {
		t.Fatal("Move allowed")
	}
	if err := f.DirMove(ctx, f, "personal/old", "personal/new"); err == nil {
		t.Fatal("DirMove allowed")
	}
	src := object.NewStaticObjectInfo(o.remote, time.Now(), 1, true, nil, f)
	if _, err := f.Put(ctx, strings.NewReader("x"), src); err == nil {
		t.Fatal("Put allowed")
	}
}

func TestWriteScopeUsesResolvedPath(t *testing.T) {
	for _, root := range []string{"", "codex-api-lab/run"} {
		f := &Fs{root: root, opt: Options{Enc: defaultEncoding, LabWrites: true}}
		remote := "file"
		if root == "" {
			remote = "codex-api-lab/run/file"
		}
		if err := f.writeAllowed(remote); err != nil {
			t.Fatal(err)
		}
	}
	for _, remote := range []string{"", "personal/file", "codex-api-lab", "codex-api-lab/../personal/file", "codex-api-lab//file"} {
		f := &Fs{opt: Options{Enc: defaultEncoding, LabWrites: true}}
		if f.writeAllowed(remote) == nil {
			t.Fatalf("allowed %q", remote)
		}
	}
}
