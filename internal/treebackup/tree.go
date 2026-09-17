// Package treebackup streams complete directory contents to a tar backup and
// verifies a remote tree against it without writing to the remote or local paths.
package treebackup

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/nyte/TboxRclone/internal/smh"
)

// Entry identifies a directory or the exact bytes of a regular file.
type Entry struct {
	Directory bool
	Size      int64
	SHA256    string
}

func validName(name string) bool {
	return name != "" && smh.ValidatePath(name) == nil && !strings.HasPrefix(name, "/")
}

// Write streams a portable content backup, including empty directories.
// The caller must own the source subtree for the entire operation.
func Write(ctx context.Context, c *smh.Client, root string, out io.Writer) error {
	tw := tar.NewWriter(out)
	var walk func(string) error
	walk = func(relative string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		full := root
		name := "."
		if relative != "" {
			full += "/" + relative
			name = relative
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0700, Typeflag: tar.TypeDir}); err != nil {
			return err
		}
		items, err := c.List(ctx, full)
		if err != nil {
			return err
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		for _, item := range items {
			child := item.Name
			if relative != "" {
				child = relative + "/" + child
			}
			if !validName(child) {
				return errors.New("invalid backup path")
			}
			if item.Type == "dir" {
				if err = walk(child); err != nil {
					return err
				}
				continue
			}
			if item.Type != "file" && item.Type != "image" && item.Type != "video" {
				return errors.New("unsupported backup entry type")
			}
			current, err := c.Info(ctx, root+"/"+child)
			if err != nil {
				return err
			}
			if current.Type == "dir" {
				return errors.New("backup entry changed type")
			}
			reader, err := c.Open(ctx, root+"/"+child, current, 0, -1)
			if err != nil {
				return err
			}
			err = tw.WriteHeader(&tar.Header{Name: child, Mode: 0600, Typeflag: tar.TypeReg, Size: int64(current.Size)})
			if err == nil {
				_, err = io.Copy(tw, reader)
			}
			ce := reader.Close()
			if err != nil {
				return err
			}
			if ce != nil {
				return ce
			}
		}
		return nil
	}
	if err := walk(""); err != nil {
		return err
	}
	return tw.Close()
}

// Manifest validates archive structure and hashes each file. It never extracts
// paths, links or permissions from the archive onto the local filesystem.
func Manifest(in io.Reader) (map[string]Entry, error) {
	tr := tar.NewReader(in)
	out := map[string]Entry{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name := h.Name
		if name != "." && !validName(name) {
			return nil, errors.New("unsafe backup entry name")
		}
		if _, exists := out[name]; exists {
			return nil, errors.New("duplicate backup path")
		}
		e := Entry{Size: h.Size}
		switch h.Typeflag {
		case tar.TypeDir:
			if h.Size != 0 {
				return nil, errors.New("directory has payload")
			}
			e.Directory = true
		case tar.TypeReg, tar.TypeRegA:
			if name == "." {
				return nil, errors.New("backup root is not a directory")
			}
			sum := sha256.New()
			n, err := io.Copy(sum, tr)
			if err != nil {
				return nil, err
			}
			if n != h.Size {
				return nil, io.ErrUnexpectedEOF
			}
			e.SHA256 = hex.EncodeToString(sum.Sum(nil))
		default:
			return nil, errors.New("unsupported backup entry")
		}
		out[name] = e
	}
	if root, ok := out["."]; !ok || !root.Directory {
		return nil, errors.New("backup lacks root directory")
	}
	for name := range out {
		if name == "." {
			continue
		}
		parent := "."
		if i := strings.LastIndex(name, "/"); i >= 0 {
			parent = name[:i]
		}
		if e, ok := out[parent]; !ok || !e.Directory {
			return nil, errors.New("backup lacks parent directory")
		}
	}
	return out, nil
}

// Verify requires an exact path/type/content match, including empty directories.
func Verify(ctx context.Context, c *smh.Client, root string, expected map[string]Entry) error {
	info, err := c.Info(ctx, root)
	if err != nil {
		return err
	}
	if info.Type != "dir" {
		return errors.New("destination is not a directory")
	}
	seen := map[string]bool{".": true}
	var walk func(string) error
	walk = func(relative string) error {
		full := root
		if relative != "" {
			full += "/" + relative
		}
		items, err := c.List(ctx, full)
		if err != nil {
			return err
		}
		for _, item := range items {
			name := item.Name
			if relative != "" {
				name = relative + "/" + name
			}
			e, ok := expected[name]
			if !ok || seen[name] {
				return errors.New("destination has unexpected path")
			}
			seen[name] = true
			if item.Type == "dir" {
				if !e.Directory {
					return errors.New("destination type differs")
				}
				if err = walk(name); err != nil {
					return err
				}
				continue
			}
			if e.Directory || (item.Type != "file" && item.Type != "image" && item.Type != "video") {
				return errors.New("destination type differs")
			}
			current, err := c.Info(ctx, root+"/"+name)
			if err != nil {
				return err
			}
			if int64(current.Size) != e.Size {
				return errors.New("destination file size differs")
			}
			reader, err := c.Open(ctx, root+"/"+name, current, 0, -1)
			if err != nil {
				return err
			}
			sum := sha256.New()
			n, err := io.Copy(sum, reader)
			ce := reader.Close()
			if err != nil {
				return err
			}
			if ce != nil {
				return ce
			}
			if n != e.Size || hex.EncodeToString(sum.Sum(nil)) != e.SHA256 {
				return errors.New("destination file contents differ")
			}
		}
		return nil
	}
	if err = walk(""); err != nil {
		return err
	}
	if len(seen) != len(expected) {
		return fmt.Errorf("destination tree incomplete: %d of %d entries", len(seen), len(expected))
	}
	return nil
}
