// Finder-list-fixture creates stable, non-destructive WebDAV directory-count fixtures.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

var counts = []int{0, 1, 50, 51, 1000}

type caseResult struct {
	Path     string `json:"path"`
	Expected int    `json:"expected"`
	Observed int    `json:"observed"`
	Pass     bool   `json:"pass"`
}

type report struct {
	At          string       `json:"at"`
	Base        string       `json:"base"`
	Concurrency int          `json:"concurrency"`
	Cases       []caseResult `json:"cases"`
	Pass        bool         `json:"pass"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	origin := flag.String("url", "http://127.0.0.1:8686/", "loopback WebDAV origin")
	base := flag.String("base", "", "fresh fixture directory below the WebDAV origin")
	workers := flag.Int("concurrency", 12, "maximum concurrent child MKCOL requests")
	writes := flag.Bool("allow-lab-writes", false, "create the isolated fixture")
	flag.Parse()
	u, err := validateOrigin(*origin)
	if err != nil {
		return err
	}
	if !*writes || *base == "" || path.Clean(*base) != *base || strings.Contains(*base, "..") || strings.Contains(*base, "\\") {
		return errors.New("a clean fresh -base and -allow-lab-writes are required")
	}
	if *workers < 1 || *workers > 32 {
		return errors.New("concurrency must be between 1 and 32")
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	mkcol := func(name string) error {
		target := *u
		target.Path = strings.TrimRight(u.Path, "/") + "/" + strings.Trim(name, "/") + "/"
		var last int
		for attempt := 0; attempt < 4; attempt++ {
			req, _ := http.NewRequestWithContext(ctx, "MKCOL", target.String(), nil)
			resp, requestErr := client.Do(req)
			if requestErr == nil {
				last = resp.StatusCode
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if last == http.StatusCreated || last == http.StatusMethodNotAllowed && collectionExists(ctx, client, &target) {
					return nil
				}
			}
			if last != http.StatusTooManyRequests && last < 500 && requestErr == nil {
				break
			}
			time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
		}
		return fmt.Errorf("MKCOL %s returned %d", name, last)
	}
	if err = mkcol(*base); err != nil {
		return err
	}
	for _, count := range counts {
		if err = mkcol(fmt.Sprintf("%s/list-%04d", *base, count)); err != nil {
			return err
		}
	}
	type job struct {
		parent string
		index  int
	}
	jobs := make(chan job)
	errs := make(chan error, 1)
	var wg sync.WaitGroup
	for range *workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				if e := mkcol(fmt.Sprintf("%s/dir-%04d", item.parent, item.index)); e != nil {
					select {
					case errs <- e:
					default:
					}
					return
				}
			}
		}()
	}
	for _, count := range counts {
		parent := fmt.Sprintf("%s/list-%04d", *base, count)
		existing, listErr := listNames(ctx, client, u, parent)
		if listErr != nil {
			close(jobs)
			wg.Wait()
			return listErr
		}
		seen := make(map[string]bool, len(existing))
		for _, name := range existing {
			seen[name] = true
		}
		for index := 0; index < count; index++ {
			if seen[fmt.Sprintf("dir-%04d", index)] {
				continue
			}
			select {
			case jobs <- job{parent, index}:
			case err = <-errs:
				close(jobs)
				wg.Wait()
				return err
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				return ctx.Err()
			}
		}
	}
	close(jobs)
	wg.Wait()
	select {
	case err = <-errs:
		return err
	default:
	}
	out := report{At: time.Now().UTC().Format(time.RFC3339), Base: *base, Concurrency: *workers, Pass: true}
	for _, count := range counts {
		name := fmt.Sprintf("%s/list-%04d", *base, count)
		names, listErr := listNames(ctx, client, u, name)
		observed := len(names)
		item := caseResult{Path: name, Expected: count, Observed: observed, Pass: listErr == nil && observed == count}
		out.Cases = append(out.Cases, item)
		out.Pass = out.Pass && item.Pass
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(out); err != nil {
		return err
	}
	if !out.Pass {
		return errors.New("fixture verification failed")
	}
	return nil
}

func validateOrigin(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("url must be a plain loopback HTTP origin")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("only loopback origins are accepted")
	}
	return u, nil
}

func collectionExists(ctx context.Context, client *http.Client, target *url.URL) bool {
	req, _ := http.NewRequestWithContext(ctx, "PROPFIND", target.String(), strings.NewReader(`<?xml version="1.0"?><propfind xmlns="DAV:"><prop><resourcetype/></prop></propfind>`))
	req.Header.Set("Depth", "0")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return err == nil && resp.StatusCode == 207 && bytes.Contains(bytes.ToLower(body), []byte("collection"))
}

func listNames(ctx context.Context, client *http.Client, origin *url.URL, name string) ([]string, error) {
	target := *origin
	target.Path = strings.TrimRight(origin.Path, "/") + "/" + strings.Trim(name, "/") + "/"
	req, _ := http.NewRequestWithContext(ctx, "PROPFIND", target.String(), strings.NewReader(`<?xml version="1.0"?><propfind xmlns="DAV:"><prop><displayname/></prop></propfind>`))
	req.Header.Set("Depth", "1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 207 {
		return nil, fmt.Errorf("PROPFIND returned %d", resp.StatusCode)
	}
	var body struct {
		Responses []struct {
			Href string `xml:"href"`
		} `xml:"response"`
	}
	if err = xml.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(body.Responses))
	for _, item := range body.Responses {
		if strings.TrimRight(item.Href, "/") != strings.TrimRight(target.EscapedPath(), "/") {
			decoded, decodeErr := url.PathUnescape(item.Href)
			if decodeErr != nil {
				return nil, decodeErr
			}
			names = append(names, path.Base(strings.TrimRight(decoded, "/")))
		}
	}
	sort.Strings(names)
	return names, nil
}
