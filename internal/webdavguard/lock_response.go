package webdavguard

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// lockResponse supplies the discovery document required by RFC 4918 9.10.1.
// Tokens are re-encoded so owner namespaces declared on lockinfo remain valid.
func lockResponse(r *http.Request) (string, []byte, error) {
	d := xml.NewDecoder(io.LimitReader(r.Body, 65537))
	var owner bytes.Buffer
	e := xml.NewEncoder(&owner)
	level, ownerLevel := 0, 0
	root, exclusive, write := false, false, false
	var stack []xml.Name
	scopeCount, typeCount, ownerCount := 0, 0, 0
	for {
		t, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, err
		}
		switch v := t.(type) {
		case xml.StartElement:
			level++
			if level == 1 {
				if root || v.Name != (xml.Name{Space: "DAV:", Local: "lockinfo"}) {
					return "", nil, fmt.Errorf("expected DAV:lockinfo")
				}
				root = true
			}
			if level == 2 && v.Name == (xml.Name{Space: "DAV:", Local: "lockscope"}) {
				scopeCount++
			}
			if level == 2 && v.Name == (xml.Name{Space: "DAV:", Local: "locktype"}) {
				typeCount++
			}
			if level == 3 && stack[1] == (xml.Name{Space: "DAV:", Local: "lockscope"}) && v.Name == (xml.Name{Space: "DAV:", Local: "exclusive"}) {
				exclusive = true
			}
			if level == 3 && stack[1] == (xml.Name{Space: "DAV:", Local: "locktype"}) && v.Name == (xml.Name{Space: "DAV:", Local: "write"}) {
				write = true
			}
			if level == 2 && v.Name == (xml.Name{Space: "DAV:", Local: "owner"}) {
				ownerLevel = level
				ownerCount++
			}
			stack = append(stack, v.Name)
		case xml.CharData:
			if level == 0 && strings.TrimSpace(string(v)) != "" {
				return "", nil, fmt.Errorf("text outside lockinfo")
			}
		}
		if ownerLevel != 0 {
			if err := e.EncodeToken(t); err != nil {
				return "", nil, err
			}
		}
		if _, ok := t.(xml.EndElement); ok {
			if level == ownerLevel {
				ownerLevel = 0
			}
			stack = stack[:len(stack)-1]
			level--
		}
	}
	if !root || level != 0 || !exclusive || !write || scopeCount != 1 || typeCount != 1 || ownerCount > 1 || d.InputOffset() > 65536 {
		return "", nil, fmt.Errorf("expected bounded exclusive write lock request")
	}
	if err := e.Flush(); err != nil {
		return "", nil, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", nil, err
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	token := "urn:uuid:" + hex.EncodeToString(id[:4]) + "-" + hex.EncodeToString(id[4:6]) + "-" + hex.EncodeToString(id[6:8]) + "-" + hex.EncodeToString(id[8:10]) + "-" + hex.EncodeToString(id[10:])
	var href bytes.Buffer
	if err := xml.EscapeText(&href, []byte(r.URL.EscapedPath())); err != nil {
		return "", nil, err
	}
	depth := r.Header.Get("Depth")
	if depth == "" {
		depth = "infinity"
	}
	if depth != "0" && depth != "infinity" {
		return "", nil, fmt.Errorf("invalid lock depth")
	}
	body := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?><prop xmlns="DAV:"><lockdiscovery><activelock><locktype><write/></locktype><lockscope><exclusive/></lockscope><depth>%s</depth>%s<timeout>Infinite</timeout><locktoken><href>%s</href></locktoken><lockroot><href>%s</href></lockroot></activelock></lockdiscovery></prop>`, depth, owner.String(), token, href.String())
	return token, []byte(body), nil
}
