package faultproxy

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// SetRules replaces the fault plan. Pending responses must first be released.
func (p *Proxy) SetRules(rules []Rule) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || len(p.pending) > 0 {
		return errors.New("proxy closed or held responses pending")
	}
	if len(rules) > 100 {
		return errors.New("too many fault rules")
	}
	seen := map[string]bool{}
	for _, r := range rules {
		if r.ID == "" || len(r.ID) > 64 || strings.ContainsAny(r.ID, "\r\n?&=") || seen[r.ID] || !p.allowed[strings.ToLower(r.Host)] || r.Nth < 1 || r.Method == "" || !strings.HasPrefix(r.PathPrefix, "/") || r.Bytes < 0 || r.MinContentLength < 0 || r.PartNumber < 0 || r.PartNumber > 10000 || r.PartNumber != 0 && r.Method != http.MethodPut {
			return errors.New("invalid fault rule")
		}
		slow := r.Action == "delay_request" || r.Action == "throttle_upload"
		if (!slow && (r.Repeat || r.DelayMS != 0 || r.BytesPerSecond != 0)) ||
			(slow && (r.Status != 0 || r.Code != "" || r.Bytes != 0)) {
			return errors.New("slow-network options require a slow-network action without response overrides")
		}
		seen[r.ID] = true
		switch r.Action {
		case "delay_request":
			if r.DelayMS < 1 || r.DelayMS > 10000 || r.BytesPerSecond != 0 {
				return errors.New("delay_request requires 1..10000 delay_ms and no byte rate")
			}
		case "throttle_upload":
			if r.Method != http.MethodPut || r.BytesPerSecond < 1024 || r.BytesPerSecond > 1<<30 || r.DelayMS != 0 {
				return errors.New("throttle_upload requires PUT, 1KiB..1GiB per second, and no delay")
			}
		case "observe", "drop_request", "drop_response", "hold_response", "cut_upload", "cut_download":
			if r.Status != 0 || r.Code != "" {
				return errors.New("status and code are only valid for respond_status")
			}
		case "respond_status":
			if r.Status < 400 || r.Status > 599 || r.Bytes != 0 || r.Code != "" && r.Code != "QuotaLimitReached" {
				return errors.New("respond_status requires a 400..599 status and no byte limit")
			}
		case "replay_part":
			if r.Method != http.MethodPut || r.PartNumber == 0 || r.Status != 0 || r.Code != "" || r.Bytes != 0 {
				return errors.New("replay_part requires a numbered PUT and no response override")
			}
		case "ignore_range":
			if r.Method != http.MethodGet || r.Status != 0 || r.Code != "" || r.Bytes != 0 {
				return errors.New("ignore_range requires GET and no response override")
			}
		default:
			return errors.New("unknown fault action")
		}
	}
	for _, r := range rules {
		for _, prerequisite := range r.AfterAny {
			if !seen[prerequisite] || prerequisite == r.ID {
				return errors.New("invalid prerequisite rule")
			}
		}
	}
	p.rules = append([]Rule(nil), rules...)
	p.hits = map[string]int{}
	return nil
}

func (p *Proxy) match(host string, r *http.Request) *Rule {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, rule := range p.rules {
		if len(rule.AfterAny) > 0 {
			ready := false
			for _, prerequisite := range rule.AfterAny {
				for _, prior := range p.rules {
					if prior.ID == prerequisite && p.hits[prior.ID] >= prior.Nth {
						ready = true
					}
				}
			}
			if !ready {
				continue
			}
		}
		if !strings.EqualFold(rule.Host, host) || rule.Method != r.Method || !strings.HasPrefix(r.URL.Path, rule.PathPrefix) || (rule.ActionKey != "" && !r.URL.Query().Has(rule.ActionKey)) {
			continue
		}
		// Ignore placeholders/metadata without consuming the one-shot fault.
		if rule.Action == "ignore_range" && r.Header.Get("Range") == "" {
			continue
		}
		if rule.MinContentLength > 0 && r.ContentLength < rule.MinContentLength {
			continue
		}
		if rule.PartNumber != 0 {
			number, err := strconv.Atoi(r.URL.Query().Get("partNumber"))
			if err != nil || number != rule.PartNumber {
				continue
			}
		}
		p.hits[rule.ID]++
		if p.hits[rule.ID] == rule.Nth || rule.Repeat && p.hits[rule.ID] > rule.Nth {
			c := rule
			return &c
		}
	}
	return nil
}
