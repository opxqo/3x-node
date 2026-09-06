//go:build mastercontract

package node_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/node"
	master "github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

type contractEngine struct{}

func (*contractEngine) Apply([]node.LiveInbound) error   { return nil }
func (*contractEngine) Stats() (map[string]int64, error) { return map[string]int64{}, nil }
func (*contractEngine) Running() bool                    { return true }
func (*contractEngine) Stop() error                      { return nil }
func (*contractEngine) PID() int                         { return 0 }

// Calls the unmodified master's real wire client, not a recreated HTTP payload.
func TestUnmodifiedMasterLeafContract(t *testing.T) {
	c := node.Config{Token: strings.Repeat("a", 64), BasePath: "/leaf/", StateFile: filepath.Join(t.TempDir(), "state.json"), APIPort: 62789}
	leaf, err := node.New(c, &contractEngine{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(leaf.Handler())
	defer server.Close()
	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())
	sum := sha256.Sum256(server.Certificate().Raw)
	r := master.NewRemote(&model.Node{Id: 1, Name: "leaf", Scheme: "https", Address: u.Hostname(), Port: port, BasePath: c.BasePath, ApiToken: c.Token, Enable: true, AllowPrivateAddress: true, TlsVerifyMode: "pin", PinnedCertSha256: base64.StdEncoding.EncodeToString(sum[:])}, nil)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	ib := &model.Inbound{Tag: "n1-in-18443", Port: 18443, Protocol: "vless", Enable: true, Settings: `{"decryption":"none","encryption":"none","fallbacks":[],"clients":[]}`, StreamSettings: `{"network":"tcp","security":"reality","realitySettings":{"privateKey":"test","target":"example.org:443","serverNames":["example.org"],"shortIds":["ab"]}}`, Sniffing: `{"enabled":false}`}
	must(r.AddInbound(ctx, ib))
	ib.Id = 1
	options, err := r.ListInboundOptions(ctx)
	must(err)
	if len(options) != 1 {
		t.Fatalf("options: %+v", options)
	}
	must(r.SetInboundSubSortIndex(ctx, ib, 2))
	alice := model.Client{ID: "00000000-0000-4000-8000-000000000001", Email: "alice", Enable: true, Flow: "xtls-rprx-vision", SubID: "share-id"}
	must(r.AddClient(ctx, ib, alice))
	alice.Comment = "updated"
	must(r.UpdateUser(ctx, ib, "alice", alice))
	must(r.PushGlobalClientTraffics(ctx, "master", []*xray.ClientTraffic{{Email: "alice", Up: 1000}}))
	snapshot, err := r.FetchTrafficSnapshot(ctx)
	must(err)
	if snapshot == nil {
		t.Fatal("missing snapshot")
	}
	must(r.ResetClientTraffic(ctx, ib, "alice"))
	must(r.ResetInboundTraffic(ctx, ib))
	must(r.ResetAllTraffics(ctx))
	_, err = r.GetDescendants(ctx)
	must(err)
	_, err = r.FetchHostGroups(ctx)
	must(err)
	_, err = r.FetchAllClientIps(ctx)
	must(err)
	_, err = r.FetchClientIpsByGuid(ctx)
	must(err)
	must(r.PushAllClientIps(ctx, nil))
	must(r.DeleteUser(ctx, ib, "alice"))
	must(r.AddClient(ctx, ib, alice))
	must(r.DeleteClient(ctx, "alice"))
	must(r.RestartXray(ctx))
	if r.UpdatePanel(ctx, false) == nil {
		t.Fatal("full updater accepted")
	}
	must(r.DelInbound(ctx, ib))
	plain := &model.Inbound{Tag: "n1-in-19090", Port: 19090, Protocol: "vless", Enable: true, Settings: `{"decryption":"none","encryption":"none","fallbacks":[],"clients":[]}`, StreamSettings: `{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}`, Sniffing: `{"enabled":false}`}
	must(r.AddInbound(ctx, plain))
	options, err = r.ListInboundOptions(ctx)
	must(err)
	if len(options) != 1 || options[0].Port != plain.Port || options[0].Protocol != plain.Protocol {
		t.Fatalf("plain VLESS option missing: %+v", options)
	}
	must(r.DelInbound(ctx, plain))
}
