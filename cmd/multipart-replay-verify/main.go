// Multipart-replay-verify repeats one immutable part in a fresh real-cloud
// upload session, then confirms and independently reads the published bytes.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/nyte/TboxRclone/internal/sjtu/defaults"
	"github.com/nyte/TboxRclone/internal/sjtu/smh"
	"github.com/nyte/TboxRclone/internal/verifyutil"
)

type report struct {
	At                 string `json:"at"`
	Root               string `json:"root"`
	Path               string `json:"path"`
	Bytes              int    `json:"bytes"`
	SHA256             string `json:"sha256"`
	FirstETag          string `json:"first_etag"`
	SecondETag         string `json:"second_etag"`
	SameETag           bool   `json:"same_etag"`
	ServerPartCount    int    `json:"server_part_count"`
	IndependentReadSHA string `json:"independent_read_sha256"`
	Pass               bool   `json:"pass"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	spaceFile := flag.String("space-file", "/secrets/space.json", "SMH library and space JSON")
	tokenFile := flag.String("token-file", "/secrets/user-token", "SMH user token file")
	root := flag.String("root", "", "fresh directory below codex-api-lab")
	output := flag.String("output", "", "optional new JSON evidence file below evidence")
	flag.Parse()
	if !strings.HasPrefix(*root, "codex-api-lab/") || smh.ValidatePath(*root) != nil {
		return errors.New("a fresh isolated -root is required")
	}
	var ids struct {
		Library string `json:"libraryId"`
		Space   string `json:"spaceId"`
	}
	b, err := os.ReadFile(*spaceFile)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &ids); err != nil {
		return err
	}
	c, err := smh.New(defaults.Endpoint, ids.Library, ids.Space, "")
	if err != nil {
		return err
	}
	if err = c.UseUserToken(*tokenFile, "1"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err = c.Info(ctx, *root); !smh.IsStatus(err, http.StatusNotFound) {
		if err == nil {
			return errors.New("root already exists")
		}
		return err
	}
	if err = c.JSON(ctx, http.MethodPut, "directory", *root, url.Values{"conflict_resolution_strategy": {"ask"}}, struct{}{}, nil); err != nil {
		return err
	}
	payload := make([]byte, 1<<20)
	for i := range payload {
		payload[i] = byte(i*31 + i/251)
	}
	hash := verifyutil.SHA256(payload)
	p := *root + "/replay.bin"
	u, err := c.Multipart(ctx, p, int64(len(payload)), 1)
	if err != nil {
		return err
	}
	first, err := c.PutPart(ctx, u, 1, bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return err
	}
	second, err := c.PutPart(ctx, u, 1, bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return err
	}
	status, err := c.UploadState(ctx, u.Key)
	if err != nil {
		return err
	}
	var confirmed smh.Item
	if err = c.JSON(ctx, http.MethodPost, "file", u.Key, url.Values{"confirm": {"1"}, "conflict_resolution_strategy": {"ask"}}, struct{}{}, &confirmed); err != nil {
		return err
	}
	item, err := c.Info(ctx, p)
	if err != nil {
		return err
	}
	r, err := c.Open(ctx, p, item, 0, -1)
	if err != nil {
		return err
	}
	readHash := sha256.New()
	readBytes, readErr := io.Copy(readHash, r)
	closeErr := r.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	out := report{
		At: time.Now().UTC().Format(time.RFC3339), Root: *root, Path: p, Bytes: len(payload),
		SHA256: hash, FirstETag: first, SecondETag: second,
		SameETag: first != "" && first == second, ServerPartCount: len(status.Parts),
		IndependentReadSHA: hex.EncodeToString(readHash.Sum(nil)),
	}
	out.Pass = out.SameETag && out.ServerPartCount == 1 && len(status.Parts) == 1 && status.Parts[0].Number == 1 &&
		int64(status.Parts[0].Size) == int64(len(payload)) && strings.Join(confirmed.Path, "/") == p &&
		int64(confirmed.Size) == int64(len(payload)) && readBytes == int64(len(payload)) && out.IndependentReadSHA == out.SHA256
	b, err = json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if *output != "" {
		if err = verifyutil.WriteEvidence(*output, b); err != nil {
			return err
		}
	}
	os.Stdout.Write(b)
	if !out.Pass {
		return errors.New("identical part replay verification failed")
	}
	return nil
}
