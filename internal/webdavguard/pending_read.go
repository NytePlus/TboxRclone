package webdavguard

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
)

// servePending exposes only the local creation acknowledged by the guard.
// It never forwards a request or changes the durable publication receipt.
func servePending(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "PROPFIND" {
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, path.Base(r.URL.Path), time.Time{}, bytes.NewReader(nil))
		return
	}
	var request struct {
		XMLName xml.Name
		Prop    *struct {
			Names []struct{ XMLName xml.Name } `xml:",any"`
		} `xml:"DAV: prop"`
		PropName *struct{} `xml:"DAV: propname"`
	}
	err := xml.NewDecoder(io.LimitReader(r.Body, 65537)).Decode(&request)
	if err != nil && err != io.EOF || err == nil && request.XMLName != (xml.Name{Space: "DAV:", Local: "propfind"}) {
		http.Error(w, "invalid PROPFIND", http.StatusBadRequest)
		return
	}
	values := map[string]string{"displayname": path.Base(r.URL.Path), "getcontentlength": "0", "getcontenttype": "application/octet-stream", "resourcetype": ""}
	names := []xml.Name{}
	if request.Prop != nil {
		for _, prop := range request.Prop.Names {
			names = append(names, prop.XMLName)
		}
	} else {
		for _, name := range []string{"displayname", "getcontentlength", "getcontenttype", "resourcetype"} {
			names = append(names, xml.Name{Space: "DAV:", Local: name})
		}
	}
	var body bytes.Buffer
	encoder := xml.NewEncoder(&body)
	escape := func(value string) { _ = encoder.EncodeToken(xml.CharData(value)) }
	start := func(name string) {
		_ = encoder.EncodeToken(xml.StartElement{Name: xml.Name{Space: "DAV:", Local: name}})
	}
	end := func(name string) { _ = encoder.EncodeToken(xml.EndElement{Name: xml.Name{Space: "DAV:", Local: name}}) }
	start("multistatus")
	start("response")
	start("href")
	escape(r.URL.EscapedPath())
	end("href")
	for _, found := range []bool{true, false} {
		var selected []xml.Name
		for _, name := range names {
			_, ok := values[name.Local]
			if (name.Space == "DAV:" && ok) == found {
				selected = append(selected, name)
			}
		}
		if len(selected) == 0 {
			continue
		}
		start("propstat")
		start("prop")
		for _, name := range selected {
			_ = encoder.EncodeToken(xml.StartElement{Name: name})
			if found && request.PropName == nil {
				escape(values[name.Local])
			}
			_ = encoder.EncodeToken(xml.EndElement{Name: name})
		}
		end("prop")
		start("status")
		if found {
			escape("HTTP/1.1 200 OK")
		} else {
			escape("HTTP/1.1 404 Not Found")
		}
		end("status")
		end("propstat")
	}
	end("response")
	end("multistatus")
	_ = encoder.Flush()
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = io.Copy(w, strings.NewReader(body.String()))
}
