package faultproxy

import (
	"encoding/json"
	"net/http"
)

// ControlHandler serves rule setup, redacted event retrieval and response release.
// Bind this handler to a separate loopback-only listener, never the public network.
func (p *Proxy) ControlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /rules", func(w http.ResponseWriter, r *http.Request) {
		var rules []Rule
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
		dec.DisallowUnknownFields()
		if dec.Decode(&rules) != nil {
			http.Error(w, "invalid rules", 400)
			return
		}
		if e := p.SetRules(rules); e != nil {
			http.Error(w, e.Error(), 409)
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(p.Events(0))
	})
	mux.HandleFunc("POST /release", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Request uint64 `json:"request"`
			Forward *bool  `json:"forward"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input) != nil || input.Forward == nil {
			http.Error(w, "invalid release", 400)
			return
		}
		if e := p.Release(input.Request, *input.Forward); e != nil {
			http.Error(w, e.Error(), 404)
			return
		}
		w.WriteHeader(204)
	})
	return mux
}
