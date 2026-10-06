package networkpath

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSanitizeAndForward(t *testing.T) {
	raw := []byte(`{"inbounds":[{"id":1,"tag":"in","aliases":["in"],"settings":{"clients":[{"id":"SECRET"}]},"streamSettings":{"privateKey":"SECRET"}}],"outbounds":[{"tag":"proxy","protocol":"socks","settings":{"servers":[{"address":"proxy.example","port":1080,"users":[{"user":"SECRET","pass":"SECRET"}]}]},"proxySettings":{"tag":"direct"}}],"routing":{"rules":[{"inboundTag":["in"],"user":["SECRET"],"outboundTag":"proxy"}],"balancers":[{"tag":"b","selector":["proxy"],"strategy":{"type":"random","secret":"SECRET"}}]}}`)
	c, err := Sanitize(raw)
	if err != nil {
		t.Fatal(err)
	}
	wire, _ := json.Marshal(Bundle{Version: 1, Sources: []Source{{GUID: "g", Local: true, Status: "ok", Config: c}}})
	if strings.Contains(string(wire), "SECRET") {
		t.Fatal("credential leaked")
	}
	b, err := DecodeBundle(wire)
	if err != nil {
		t.Fatal(err)
	}
	PrefixAliases(&b, 2)
	wire, _ = json.Marshal(b)
	if !strings.Contains(string(wire), "n2-in") {
		t.Fatal(string(wire))
	}
	// Re-sanitize hostile child responses and reject ambiguous origins/version.
	if _, err = DecodeBundle([]byte(`{"version":2,"sources":[]}`)); err == nil {
		t.Fatal("version accepted")
	}
}
