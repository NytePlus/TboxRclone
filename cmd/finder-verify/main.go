// finder-verify compares a mounted Finder-visible tree with a deterministic
// finder-fixture manifest. It is a fact-check only; it never changes either
// the source fixture or the mounted destination.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type entry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type fixture struct {
	Files       []entry  `json:"files"`
	Directories []string `json:"directories"`
}

type result struct {
	Status              string   `json:"status"`
	Manifest            string   `json:"manifest"`
	Target              string   `json:"target"`
	Prefix              string   `json:"prefix,omitempty"`
	VerifiedFiles       int      `json:"verified_files"`
	VerifiedDirectories int      `json:"verified_directories"`
	Errors              []string `json:"errors"`
	Method              string   `json:"method"`
}

func main() {
	manifestPath := flag.String("manifest", "", "finder-fixture manifest.json")
	target := flag.String("target", "", "mounted destination directory")
	prefix := flag.String("prefix", "", "optional fixture subtree, using slash separators")
	flag.Parse()
	r := verify(*manifestPath, *target, *prefix)
	_ = json.NewEncoder(os.Stdout).Encode(r)
	if r.Status != "PASS" {
		os.Exit(1)
	}
}

func verify(manifestPath, target, prefix string) result {
	r := result{Manifest: manifestPath, Target: target, Prefix: prefix, Method: "complete mounted-file reads with SHA-256; directory existence and type checks"}
	if manifestPath == "" || target == "" {
		r.Errors = append(r.Errors, "manifest and target are required")
		return finish(r)
	}
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
		return finish(r)
	}
	var f fixture
	if err = json.Unmarshal(b, &f); err != nil {
		r.Errors = append(r.Errors, err.Error())
		return finish(r)
	}
	prefix = strings.Trim(strings.ReplaceAll(prefix, "\\", "/"), "/")
	r.Prefix = prefix
	selected := false
	for _, want := range f.Files {
		rel, ok := beneath(want.Path, prefix)
		if !ok {
			continue
		}
		selected = true
		path, err := safeJoin(target, rel)
		if err != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("%s: %v", want.Path, err))
			continue
		}
		got, err := hashFile(path)
		if err != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("%s: %v", want.Path, err))
			continue
		}
		if got.Size != want.Size || got.SHA256 != want.SHA256 {
			r.Errors = append(r.Errors, fmt.Sprintf("%s: got size=%d sha256=%s, want size=%d sha256=%s", want.Path, got.Size, got.SHA256, want.Size, want.SHA256))
			continue
		}
		r.VerifiedFiles++
	}
	for _, dir := range f.Directories {
		rel, ok := beneath(dir, prefix)
		if !ok || rel == "" {
			continue
		}
		selected = true
		path, err := safeJoin(target, rel)
		if err != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("%s: %v", dir, err))
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			if err == nil {
				err = errors.New("not a directory")
			}
			r.Errors = append(r.Errors, fmt.Sprintf("%s: %v", dir, err))
			continue
		}
		r.VerifiedDirectories++
	}
	if !selected {
		r.Errors = append(r.Errors, "prefix selects no fixture entries")
	}
	slices.Sort(r.Errors)
	return finish(r)
}

func beneath(name, prefix string) (string, bool) {
	name = strings.Trim(strings.ReplaceAll(name, "\\", "/"), "/")
	if prefix == "" {
		return name, true
	}
	if name == prefix {
		return "", true
	}
	rel, ok := strings.CutPrefix(name, prefix+"/")
	return rel, ok
}

func safeJoin(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", errors.New("invalid relative path")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes target")
	}
	return filepath.Join(root, clean), nil
}

func hashFile(path string) (entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return entry{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return entry{}, err
	}
	if !info.Mode().IsRegular() {
		return entry{}, errors.New("not a regular file")
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return entry{}, err
	}
	return entry{Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func finish(r result) result {
	if len(r.Errors) == 0 {
		r.Status = "PASS"
	} else {
		r.Status = "FAIL"
	}
	if r.Errors == nil {
		r.Errors = []string{}
	}
	return r
}
