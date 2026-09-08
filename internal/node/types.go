// Package node implements the bounded, headless leaf-node API.
package node

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"
)

const (
	Version     = "0.1.22-node"
	XrayVersion = "26.7.28"
	MaxBody     = 1 << 20
	MaxInbounds = 8
	MaxClients  = 64
)

// Object accepts both modern nested objects and legacy JSON strings.
type Object json.RawMessage

func (o *Object) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		b = []byte(s)
	}
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		b = []byte("{}")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(b, &object); err != nil {
		return errors.New("nested JSON must be an object")
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return err
	}
	*o = append((*o)[:0], canonical...)
	return nil
}

func (o Object) MarshalJSON() ([]byte, error) {
	if len(o) == 0 {
		return []byte("{}"), nil
	}
	return o, nil
}

type Client map[string]json.RawMessage

func (c Client) Text(k string) string  { var v string; _ = json.Unmarshal(c[k], &v); return v }
func (c Client) Number(k string) int64 { var v int64; _ = json.Unmarshal(c[k], &v); return v }
func (c Client) Enabled() bool         { var v bool; _ = json.Unmarshal(c["enable"], &v); return v }
func (c Client) Set(k string, v any)   { c[k], _ = json.Marshal(v) }

type Inbound struct {
	ID                int       `json:"id"`
	Up                int64     `json:"up"`
	Down              int64     `json:"down"`
	Total             int64     `json:"total"`
	Remark            string    `json:"remark"`
	SubSortIndex      int       `json:"subSortIndex"`
	Enable            bool      `json:"enable"`
	ExpiryTime        int64     `json:"expiryTime"`
	TrafficReset      string    `json:"trafficReset"`
	TrafficResetDay   int       `json:"trafficResetDay"`
	Listen            string    `json:"listen"`
	Port              int       `json:"port"`
	Protocol          string    `json:"protocol"`
	Settings          Object    `json:"settings"`
	StreamSettings    Object    `json:"streamSettings"`
	Sniffing          Object    `json:"sniffing"`
	Tag               string    `json:"tag"`
	ShareAddrStrategy string    `json:"shareAddrStrategy"`
	ShareAddr         string    `json:"shareAddr"`
	DisableFlow       bool      `json:"disableFlow"`
	ClientStats       []Traffic `json:"clientStats,omitempty"`
}

func (i Inbound) Clients() ([]Client, error) {
	clients, _, err := i.ClientList()
	return clients, err
}

// ClientList distinguishes a valid empty clients array from a malformed or
// incomplete source payload where the clients field is absent.
func (i Inbound) ClientList() ([]Client, bool, error) {
	var s struct {
		Clients *[]Client `json:"clients"`
	}
	err := json.Unmarshal(i.Settings, &s)
	if err != nil || s.Clients == nil {
		return nil, false, err
	}
	return *s.Clients, true, nil
}

func (i *Inbound) SetClients(cs []Client) {
	var s map[string]json.RawMessage
	_ = json.Unmarshal(i.Settings, &s)
	if s == nil {
		s = make(map[string]json.RawMessage)
	}
	if cs == nil {
		cs = []Client{}
	}
	s["clients"], _ = json.Marshal(cs)
	i.Settings, _ = json.Marshal(s)
}

type Traffic struct {
	Reset        int    `json:"reset,omitempty"`
	ResetDay     int    `json:"resetDay,omitempty"`
	ResetMax     int    `json:"resetMax,omitempty"`
	ResetCount   int    `json:"resetCount,omitempty"`
	LastSubFetch int64  `json:"lastSubFetch,omitempty"`
	ID           int    `json:"id"`
	InboundID    int    `json:"inboundId"`
	Email        string `json:"email"`
	UUID         string `json:"uuid"`
	SubID        string `json:"subId"`
	Enable       bool   `json:"enable"`
	Up           int64  `json:"up"`
	Down         int64  `json:"down"`
	Total        int64  `json:"total"`
	ExpiryTime   int64  `json:"expiryTime"`
	LastOnline   int64  `json:"lastOnline"`
}

type Global struct {
	Up        int64 `json:"up"`
	Down      int64 `json:"down"`
	UpdatedAt int64 `json:"updatedAt"`
}

type State struct {
	Running      bool                         `json:"running"`
	CheckpointAt int64                        `json:"checkpointAt"`
	Schema       int                          `json:"schema"`
	GUID         string                       `json:"guid"`
	NextID       int                          `json:"nextId"`
	NextClientID int                          `json:"nextClientId"`
	Inbounds     []Inbound                    `json:"inbounds"`
	Traffic      map[string]*Traffic          `json:"traffic"`
	Globals      map[string]map[string]Global `json:"globals"`
}

func (s *State) Clone() *State {
	b, _ := json.Marshal(s)
	var n State
	_ = json.Unmarshal(b, &n)
	return &n
}

func (s *State) Find(id int) *Inbound {
	for j := range s.Inbounds {
		if s.Inbounds[j].ID == id {
			return &s.Inbounds[j]
		}
	}
	return nil
}

func (s *State) SyncClients() error {
	seen := map[string]Client{}
	for _, ib := range s.Inbounds {
		cs, err := ib.Clients()
		if err != nil {
			return err
		}
		for _, c := range cs {
			email := c.Text("email")
			if prev, ok := seen[email]; ok && (prev.Text("id") != c.Text("id") || prev.Number("totalGB") != c.Number("totalGB") || prev.Number("expiryTime") != c.Number("expiryTime") || prev.Enabled() != c.Enabled()) {
				return fmt.Errorf("inconsistent shared client %q", email)
			}
			seen[email] = c
			t := s.Traffic[email]
			if t == nil {
				t = &Traffic{ID: s.NextClientID, Email: email}
				s.NextClientID++
				s.Traffic[email] = t
			}
			t.InboundID, t.UUID, t.SubID = ib.ID, c.Text("id"), c.Text("subId")
			t.Enable, t.Total, t.ExpiryTime = c.Enabled(), c.Number("totalGB"), c.Number("expiryTime")
		}
	}
	if len(seen) > MaxClients {
		return fmt.Errorf("node supports at most %d clients", MaxClients)
	}
	for email := range s.Traffic {
		if _, ok := seen[email]; !ok {
			delete(s.Traffic, email)
			s.ClearGlobals(email)
		}
	}
	return nil
}

func (s *State) ClearGlobals(email string) {
	for _, rows := range s.Globals {
		delete(rows, email)
	}
}

func (s *State) Exhausted(t *Traffic, now int64) bool {
	if t.ExpiryTime > 0 && t.ExpiryTime <= now {
		return true
	}
	if t.Total <= 0 {
		return false
	}
	if t.Up >= t.Total-t.Down {
		return true
	}
	for _, rows := range s.Globals {
		g := rows[t.Email]
		if g.UpdatedAt >= now-int64(24*time.Hour/time.Millisecond) && g.Up >= t.Total-g.Down {
			return true
		}
	}
	return false
}

func ValidateInbound(i *Inbound) error {
	if i.Protocol != "vless" || i.Port < 1 || i.Port > 65535 {
		return errors.New("only VLESS with a TCP port 1..65535 is supported")
	}
	if i.Listen != "" && net.ParseIP(i.Listen) == nil {
		return errors.New("listen must be an IP address")
	}
	if i.Tag == "" || len(i.Tag) > 128 || strings.Contains(i.Tag, ">>>") || i.Tag == "api" {
		return errors.New("invalid or reserved inbound tag")
	}
	if i.Total < 0 || i.ExpiryTime < 0 {
		return errors.New("invalid inbound quota or expiry")
	}
	if i.TrafficReset != "" && i.TrafficReset != "never" {
		return errors.New("automatic traffic reset is unsupported")
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(i.Settings, &settings); err != nil {
		return err
	}
	for k, v := range settings {
		if !slices.Contains([]string{"clients", "decryption", "encryption", "fallbacks", "testseed"}, k) && !emptyJSON(v) {
			return fmt.Errorf("unsupported VLESS setting %s", k)
		}
	}
	var decryption string
	if raw, ok := settings["encryption"]; ok {
		var encryption string
		if err := json.Unmarshal(raw, &encryption); err != nil || encryption != "none" {
			return errors.New("VLESS encryption must be none or omitted")
		}
	}
	_ = json.Unmarshal(settings["decryption"], &decryption)
	if decryption != "none" {
		return errors.New("VLESS decryption must be none")
	}
	if !emptyJSON(settings["fallbacks"]) {
		return errors.New("VLESS fallbacks are unsupported")
	}
	if err := validateIgnoredVisionTestseed(settings["testseed"]); err != nil {
		return err
	}
	var stream map[string]json.RawMessage
	if err := json.Unmarshal(i.StreamSettings, &stream); err != nil {
		return err
	}
	var network, security string
	_ = json.Unmarshal(stream["network"], &network)
	_ = json.Unmarshal(stream["security"], &security)
	if network != "tcp" && network != "raw" {
		return errors.New("only TCP/RAW transport is supported")
	}
	if security != "" && security != "none" && security != "reality" {
		return errors.New("only none or REALITY security is supported")
	}
	for k, v := range stream {
		if !slices.Contains([]string{"network", "security", "realitySettings", "tcpSettings", "rawSettings", "externalProxy"}, k) && !emptyJSON(v) {
			return fmt.Errorf("unsupported stream setting %s", k)
		}
	}
	for _, k := range []string{"tcpSettings", "rawSettings"} {
		var fields map[string]json.RawMessage
		if !emptyJSON(stream[k]) {
			if err := json.Unmarshal(stream[k], &fields); err != nil {
				return err
			}
			for name, value := range fields {
				if name != "header" && !emptyJSON(value) {
					return fmt.Errorf("unsupported TCP setting %s", name)
				}
			}
		}
		var tcp struct {
			Header struct {
				Type string `json:"type"`
			} `json:"header"`
		}
		if !emptyJSON(stream[k]) {
			if err := json.Unmarshal(stream[k], &tcp); err != nil {
				return err
			}
			if tcp.Header.Type != "" && tcp.Header.Type != "none" {
				return errors.New("TCP header camouflage is unsupported")
			}
		}
	}
	if security == "reality" {
		var reality map[string]json.RawMessage
		if err := json.Unmarshal(stream["realitySettings"], &reality); err != nil {
			return errors.New("REALITY settings required")
		}
		for name, value := range reality {
			if !slices.Contains([]string{"target", "dest", "serverNames", "privateKey", "shortIds", "minClientVer", "maxClientVer", "maxTimeDiff", "settings"}, name) && !emptyJSON(value) {
				return fmt.Errorf("unsupported REALITY setting %s", name)
			}
		}
		var key string
		_ = json.Unmarshal(reality["privateKey"], &key)
		if key == "" {
			return errors.New("REALITY privateKey required")
		}
	} else if !emptyJSON(stream["realitySettings"]) {
		return errors.New("REALITY settings require REALITY security")
	}
	var sniff map[string]json.RawMessage
	if len(i.Sniffing) > 0 {
		if err := json.Unmarshal(i.Sniffing, &sniff); err != nil {
			return err
		}
	}
	if !emptyJSON(sniff["enabled"]) {
		return errors.New("sniffing must be disabled on the lightweight node")
	}
	cs, err := i.Clients()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, c := range cs {
		if err := validateClient(c); err != nil {
			return err
		}
		if security != "reality" && c.Text("flow") != "" {
			return errors.New("XTLS Vision flow requires REALITY security")
		}
		if seen[c.Text("email")] {
			return errors.New("duplicate client email in inbound")
		}
		seen[c.Text("email")] = true
	}
	return nil
}

func validateClient(c Client) error {
	for name, value := range c {
		// The full panel always serializes trafficResetDay as 1, even when
		// trafficReset is "never". It is harmless metadata for this node: retain
		// it for a lossless round trip, while the check below still rejects every
		// periodic reset mode that the lightweight node does not implement.
		if !slices.Contains([]string{"id", "email", "enable", "flow", "security", "totalGB", "expiryTime", "subId", "tgId", "group", "comment", "created_at", "updated_at", "trafficReset", "trafficResetDay"}, name) && !emptyJSON(value) {
			return fmt.Errorf("unsupported client setting %s", name)
		}
	}
	if len(c.Text("email")) == 0 || len(c.Text("email")) > 128 || strings.Contains(c.Text("email"), ">>>") {
		return errors.New("invalid client email")
	}
	u := c.Text("id")
	if len(u) != 36 || u[8] != '-' || u[13] != '-' || u[18] != '-' || u[23] != '-' {
		return errors.New("client id must be a UUID")
	}
	for _, ch := range strings.ReplaceAll(u, "-", "") {
		if !strings.ContainsRune("0123456789abcdefABCDEF", ch) {
			return errors.New("invalid UUID")
		}
	}
	if f := c.Text("flow"); f != "" && f != "xtls-rprx-vision" {
		return errors.New("only empty or xtls-rprx-vision flow supported")
	}
	for _, k := range []string{"totalGB", "expiryTime", "limitIp", "reset", "resetDay", "resetMax", "trafficResetDay"} {
		if v, ok := c[k]; ok {
			var n int64
			if err := json.Unmarshal(v, &n); err != nil {
				return fmt.Errorf("%s must be an integer", k)
			}
			if k != "expiryTime" && n < 0 {
				return fmt.Errorf("%s must not be negative", k)
			}
			if k == "trafficResetDay" && (n < 1 || n > 31) {
				return errors.New("trafficResetDay must be 1..31")
			}
		}
	}
	if _, ok := c["enable"]; !ok {
		return errors.New("client enable is required")
	}
	var enabled bool
	if err := json.Unmarshal(c["enable"], &enabled); err != nil {
		return errors.New("enable must be boolean")
	}
	for _, k := range []string{"limitIp", "reset", "resetDay", "resetMax", "hwidLimit", "deviceLimit", "reverse", "auth", "privateKey", "publicKey", "allowedIPs", "allowedIPsByInbound", "preSharedKey", "keepAlive", "forwardedPorts", "secret", "adTag"} {
		if !emptyJSON(c[k]) {
			return fmt.Errorf("unsupported client feature: %s", k)
		}
	}
	if r := c.Text("trafficReset"); r != "" && r != "never" {
		return errors.New("automatic client traffic reset unsupported")
	}
	return nil
}

func emptyJSON(b []byte) bool {
	s := string(bytes.TrimSpace(b))
	return s == "" || s == "null" || s == "false" || s == "0" || s == `""` || s == "{}" || s == "[]"
}

// validateIgnoredVisionTestseed accepts the full panel's optional Vision
// padding metadata. It belongs to an outbound VLESS account, while this
// lightweight node only builds the server-side inbound accounts and therefore
// intentionally ignores it when generating the Xray config. Older panel rows
// may contain three values; Xray fills the fourth value with its default.
func validateIgnoredVisionTestseed(raw []byte) error {
	if emptyJSON(raw) {
		return nil
	}
	var seed []uint64
	if err := json.Unmarshal(raw, &seed); err != nil || (len(seed) != 3 && len(seed) != 4) {
		return errors.New("VLESS testseed must contain 3 or 4 positive integers")
	}
	for _, value := range seed {
		if value == 0 || value > uint64(^uint32(0)) {
			return errors.New("VLESS testseed must contain 3 or 4 positive integers")
		}
	}
	return nil
}
