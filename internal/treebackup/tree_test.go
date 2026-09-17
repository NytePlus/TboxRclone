package treebackup

import (
	"archive/tar"
	"bytes"
	"testing"
)

func TestManifestRejectsUnsafeAndIncompleteArchives(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "a/../../escape", "missing/child", "."} {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			w := tar.NewWriter(&b)
			if err := w.WriteHeader(&tar.Header{Name: ".", Typeflag: tar.TypeDir, Mode: 0700}); err != nil {
				t.Fatal(err)
			}
			if err := w.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0600, Size: 1}); err != nil {
				t.Fatal(err)
			}
			w.Write([]byte("x"))
			w.Close()
			if _, err := Manifest(&b); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
	for _, kind := range []byte{tar.TypeSymlink, tar.TypeLink, tar.TypeFifo} {
		var b bytes.Buffer
		w := tar.NewWriter(&b)
		w.WriteHeader(&tar.Header{Name: ".", Typeflag: tar.TypeDir})
		w.WriteHeader(&tar.Header{Name: "link", Typeflag: kind, Linkname: "outside"})
		w.Close()
		if _, err := Manifest(&b); err == nil {
			t.Fatal("special entry accepted", kind)
		}
	}
}
