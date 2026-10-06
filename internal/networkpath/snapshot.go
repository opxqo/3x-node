// Package networkpath defines the read-only, credential-free topology wire contract.
package networkpath

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type Source struct {
	ParentGUID string         `json:"parentGuid,omitempty"`
	GUID       string         `json:"guid"`
	Name       string         `json:"name,omitempty"`
	Local      bool           `json:"local"`
	Status     string         `json:"status"`
	CapturedAt string         `json:"capturedAt"`
	Config     map[string]any `json:"config,omitempty"`
}
type Bundle struct {
	Version int      `json:"version"`
	Sources []Source `json:"sources"`
}

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func array(v any) []any        { a, _ := v.([]any); return a }
func copyFields(from map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		switch v := from[k].(type) {
		case string:
			if len(v) > 8192 {
				continue
			}
			out[k] = v
		case bool:
			out[k] = v
		case float64:
			out[k] = v
		case []any:
			a := []any{}
			for _, x := range v {
				if t, ok := x.(string); ok && len(t) <= 8192 {
					a = append(a, t)
				}
			}
			out[k] = a
		}
	}
	return out
}

// Sanitize uses an allowlist at EVERY level, including forwarded responses.
// No inbound settings, UUIDs, user emails, passwords or TLS keys leave the source.
func Sanitize(raw []byte) (map[string]any, error) {
	if len(raw) > 4<<20 {
		return nil, errors.New("configuration exceeds limit")
	}
	var c map[string]any
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	if c == nil {
		return nil, errors.New("invalid configuration")
	}
	ins, ok := c["inbounds"].([]any)
	if !ok || len(ins) > 2000 {
		return nil, errors.New("invalid inbounds")
	}
	outs, ok := c["outbounds"].([]any)
	if !ok || len(outs) > 1000 {
		return nil, errors.New("invalid outbounds")
	}
	routing := obj(c["routing"])
	if routing == nil {
		return nil, errors.New("invalid routing")
	}
	for _, key := range []string{"rules", "balancers"} {
		if v, exists := routing[key]; exists && v != nil {
			if _, ok := v.([]any); !ok {
				return nil, errors.New("invalid routing array")
			}
		}
	}
	rules := array(routing["rules"])
	bals := array(routing["balancers"])
	if len(rules) > 2000 || len(bals) > 1000 {
		return nil, errors.New("routing exceeds limit")
	}
	cleanIns, cleanOuts, cleanRules, cleanBals := []any{}, []any{}, []any{}, []any{}
	for _, v := range ins {
		cleanIns = append(cleanIns, copyFields(obj(v), "id", "tag", "aliases", "remark", "enable", "port", "protocol"))
	}
	for _, v := range outs {
		m := obj(v)
		o := copyFields(m, "tag", "protocol")
		settings := obj(m["settings"])
		safe := map[string]any{}
		for _, kind := range []string{"servers", "vnext"} {
			if a := array(settings[kind]); a != nil {
				entries := []any{}
				for _, v := range a {
					entries = append(entries, copyFields(obj(v), "address", "port"))
				}
				safe[kind] = entries
			}
		}
		for k, v := range copyFields(settings, "address", "port") {
			safe[k] = v
		}
		o["settings"] = safe
		o["proxySettings"] = copyFields(obj(m["proxySettings"]), "tag")
		o["streamSettings"] = map[string]any{"sockopt": copyFields(obj(obj(m["streamSettings"])["sockopt"]), "dialerProxy")}
		cleanOuts = append(cleanOuts, o)
	}
	allowed := []string{"type", "inboundTag", "outboundTag", "balancerTag", "ruleTag", "enabled", "enable", "disabled", "domain", "ip", "port", "sourcePort", "network", "protocol", "source", "localIP", "localPort", "vlessRoute"}
	for _, v := range rules {
		m := obj(v)
		for _, field := range []string{"inboundTag", "domain", "ip", "protocol", "source", "user"} {
			if value, exists := m[field]; exists && value != nil {
				if _, ok := value.([]any); !ok {
					return nil, errors.New("invalid routing match")
				}
			}
		}
		r := copyFields(m, allowed...)
		if u := m["user"]; u != nil {
			r["user"] = []string{"redacted"}
		}
		for k := range m {
			found := k == "user"
			for _, a := range allowed {
				if a == k {
					found = true
					break
				}
			}
			if !found {
				r["unparsedCondition"] = true
			}
		}
		cleanRules = append(cleanRules, r)
	}
	for _, v := range bals {
		m := obj(v)
		b := copyFields(m, "tag", "selector", "fallbackTag")
		b["strategy"] = copyFields(obj(m["strategy"]), "type")
		cleanBals = append(cleanBals, b)
	}
	return map[string]any{"inbounds": cleanIns, "outbounds": cleanOuts, "routing": map[string]any{"rules": cleanRules, "balancers": cleanBals}}, nil
}
func DecodeBundle(raw []byte) (Bundle, error) {
	var b Bundle
	if len(raw) > 4<<20 {
		return b, errors.New("topology exceeds limit")
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return b, err
	}
	if b.Version != 1 || len(b.Sources) == 0 || len(b.Sources) > 500 {
		return b, errors.New("invalid topology version or sources")
	}
	locals := 0
	for i := range b.Sources {
		s := &b.Sources[i]
		if s.Local {
			locals++
		}
		if len(s.GUID) > 128 || len(s.Name) > 512 {
			return b, errors.New("invalid source")
		}
		switch s.Status {
		case "ok", "permission", "unsupported", "timeout", "invalid", "failed":
		default:
			return b, errors.New("invalid status")
		}
		if s.Status == "ok" {
			raw, _ := json.Marshal(s.Config)
			c, err := Sanitize(raw)
			if err != nil {
				return b, err
			}
			s.Config = c
		} else {
			s.Config = nil
		}
	}
	if locals != 1 {
		return b, errors.New("invalid local source")
	}
	return b, nil
}

// PrefixAliases records the actual main-to-node tag namespace at each hop.
func PrefixAliases(b *Bundle, nodeID int) {
	prefix := fmt.Sprintf("n%d-", nodeID)
	for i := range b.Sources {
		s := &b.Sources[i]
		s.Local = false
		for _, v := range array(s.Config["inbounds"]) {
			m := obj(v)
			a := array(m["aliases"])
			if len(a) == 0 {
				a = []any{m["tag"]}
			}
			out := []string{}
			for _, v := range a {
				if t, ok := v.(string); ok {
					out = append(out, prefix+t)
				}
			}
			m["aliases"] = out
		}
	}
}
func ValidTrail(s string) bool { return len(s) <= 1200 && !strings.ContainsAny(s, "\r\n") }
