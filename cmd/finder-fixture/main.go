// finder-fixture prepares deterministic local inputs; it never performs an upload.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type entry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type manifest struct {
	Generator   string   `json:"generator"`
	Files       []entry  `json:"files"`
	Directories []string `json:"directories"`
}

func generate(root string, largeSize int64) error {
	if largeSize < 0 || largeSize > 1<<30 {
		return fmt.Errorf("large-size must be between 0 and 1073741824 bytes")
	}
	// Never reuse a run directory: partial failures are retained for inspection.
	if err := os.Mkdir(root, 0755); err != nil {
		return err
	}
	m := manifest{Generator: "finder-fixture-v2", Directories: []string{
		"CopyTree", "CopyTree/nested", "CopyTree/empty-directory",
		"SpecialNames", "SpecialNames/nfc", "SpecialNames/nfd",
		"SpecialNames/case-upper", "SpecialNames/case-lower",
	}}
	for _, dir := range m.Directories {
		if err := os.Mkdir(filepath.Join(root, filepath.FromSlash(dir)), 0755); err != nil {
			return err
		}
	}
	specs := []struct {
		name string
		size int64
	}{
		{"empty.bin", 0}, {"plain.bin", 65536}, {"large.bin", largeSize},
		{"CopyTree/first.bin", 33}, {"CopyTree/nested/second.bin", 704},
		{"SpecialNames/中文文件.bin", 101},
		{"SpecialNames/emoji-😀.bin", 102},
		{"SpecialNames/quotes-'\".bin", 103},
		{"SpecialNames/percent%hash#question?plus+.bin", 104},
		{"SpecialNames/nfc/é.bin", 105},
		{"SpecialNames/nfd/é.bin", 106},
		{"SpecialNames/case-upper/Case.bin", 107},
		{"SpecialNames/case-lower/case.bin", 108},
	}
	for _, spec := range specs {
		p := filepath.Join(root, filepath.FromSlash(spec.name))
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return err
		}
		h := sha256.New()
		block := make([]byte, 64*1024)
		for i := range block {
			block[i] = byte(i)
		}
		left := spec.size
		for left > 0 {
			n := min(left, int64(len(block)))
			if _, err = io.CopyN(io.MultiWriter(f, h), bytes.NewReader(block[:n]), n); err != nil {
				break
			}
			left -= n
		}
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		m.Files = append(m.Files, entry{spec.name, spec.size, hex.EncodeToString(h.Sum(nil))})
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "fixture.json"), append(b, '\n'), 0644)
}

func main() {
	root := flag.String("output", "", "new local directory (parent must exist)")
	size := flag.Int64("large-size", 20*1024*1024, "large fixture bytes, max 1 GiB")
	flag.Parse()
	if *root == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: finder-fixture --output NEW_DIRECTORY [--large-size BYTES]")
		os.Exit(2)
	}
	if err := generate(*root, *size); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(filepath.Join(*root, "fixture.json"))
}
