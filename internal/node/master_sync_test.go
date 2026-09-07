package node

import (
	"encoding/json"
	"strings"
	"testing"
)

func masterSyncRemoteInbound(clients []Client) Inbound {
	ib := testInbound()
	ib.ID = 77
	ib.Remark = "master-hk"
	ib.Tag = "master-hk"
	ib.SetClients(clients)
	return ib
}

func masterSyncMappings() []MasterSyncMapping {
	return []MasterSyncMapping{{ID: "hk", MasterInboundID: 77, LocalInboundID: 1}}
}

func clientByEmail(t *testing.T, ib Inbound, email string) Client {
	t.Helper()
	clients, err := ib.Clients()
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range clients {
		if client.Text("email") == email {
			return client
		}
	}
	t.Fatalf("client %q not found", email)
	return nil
}

func TestMasterSyncPreviewApplyAndIdempotence(t *testing.T) {
	n, e := testNode(t)
	alice := testClient("alice", "00000000-0000-4000-8000-000000000001")
	alice.Set("comment", "updated by master")
	bob := testClient("bob", "00000000-0000-4000-8000-000000000002")
	remote := []Inbound{masterSyncRemoteInbound([]Client{alice, bob})}

	before, err := json.Marshal(n.Inbounds())
	if err != nil {
		t.Fatal(err)
	}
	preview, err := n.PreviewMasterSync(remote, masterSyncMappings())
	if err != nil {
		t.Fatal(err)
	}
	row := preview.Mappings[0]
	if row.RemoteClients != 2 || row.Added != 1 || row.Updated != 1 || row.Unchanged != 0 || row.PendingRemoval != 0 {
		t.Fatalf("unexpected preview: %+v", row)
	}
	afterPreview, _ := json.Marshal(n.Inbounds())
	if string(before) != string(afterPreview) {
		t.Fatal("preview changed local state")
	}

	if _, err = n.ApplyMasterSync(remote, masterSyncMappings()); err != nil {
		t.Fatal(err)
	}
	clients, err := n.Inbounds()[0].Clients()
	if err != nil || len(clients) != 2 || clientByEmail(t, n.Inbounds()[0], "alice").Text("comment") != "updated by master" {
		t.Fatalf("master clients were not applied: clients=%+v err=%v", clients, err)
	}
	if e.restarts == 0 || len(e.live[0].Users) != 2 {
		t.Fatalf("runtime was not updated: restarts=%d live=%+v", e.restarts, e.live)
	}

	repeat, err := n.PreviewMasterSync(remote, masterSyncMappings())
	if err != nil {
		t.Fatal(err)
	}
	row = repeat.Mappings[0]
	if row.Added != 0 || row.Updated != 0 || row.Unchanged != 2 {
		t.Fatalf("sync is not idempotent: %+v", row)
	}

	rotated := testClient("alice", "00000000-0000-4000-8000-000000000003")
	rotated.Set("comment", "rotated")
	rotatedRemote := []Inbound{masterSyncRemoteInbound([]Client{rotated, bob})}
	if _, err = n.ApplyMasterSync(rotatedRemote, masterSyncMappings()); err != nil {
		t.Fatalf("managed UUID rotation was rejected: %v", err)
	}
	if got := clientByEmail(t, n.Inbounds()[0], "alice").Text("id"); got != rotated.Text("id") {
		t.Fatalf("managed UUID did not rotate: %s", got)
	}
}

func TestMasterSyncAcceptsPanelClientMetadata(t *testing.T) {
	n, _ := testNode(t)
	client := testClient("panel-user", "00000000-0000-4000-8000-000000000004")
	// The full panel may include traffic and reset metadata in the client
	// object even though those values are not part of the leaf Xray client.
	client.Set("up", int64(123))
	client.Set("down", int64(456))
	client.Set("total", int64(579))
	client.Set("lastOnline", int64(1700000000))
	client.Set("resetCount", 2)
	client.Set("lastSubFetch", int64(1700000000))
	client.Set("trafficReset", "never")
	client.Set("trafficResetDay", 0)

	remote := []Inbound{masterSyncRemoteInbound([]Client{client})}
	preview, err := n.PreviewMasterSync(remote, masterSyncMappings())
	if err != nil {
		t.Fatalf("panel client metadata blocked sync: %v", err)
	}
	if got := preview.Mappings[0].Added; got != 1 {
		t.Fatalf("added clients = %d, want 1", got)
	}
	if _, err = n.ApplyMasterSync(remote, masterSyncMappings()); err != nil {
		t.Fatalf("apply panel client metadata: %v", err)
	}

	got := clientByEmail(t, n.Inbounds()[0], "panel-user")
	for _, key := range []string{"up", "down", "total", "lastOnline", "resetCount", "lastSubFetch", "trafficResetDay"} {
		if _, ok := got[key]; ok {
			t.Fatalf("leaf client retained non-runtime metadata %q: %s", key, got[key])
		}
	}
	if got.Text("trafficReset") != "never" {
		t.Fatalf("trafficReset = %q, want never", got.Text("trafficReset"))
	}
}

func TestMasterSyncRejectsEnabledUnsupportedClientFeature(t *testing.T) {
	n, _ := testNode(t)
	client := testClient("limited-user", "00000000-0000-4000-8000-000000000005")
	client.Set("limitIp", 1)
	_, err := n.PreviewMasterSync([]Inbound{masterSyncRemoteInbound([]Client{client})}, masterSyncMappings())
	if err == nil || !strings.Contains(err.Error(), "unsupported client feature: limitIp") {
		t.Fatalf("unsupported client feature was not reported precisely: %v", err)
	}
}

func TestMasterSyncNeverDeletesMissingRemoteClients(t *testing.T) {
	n, _ := testNode(t)
	remote := []Inbound{masterSyncRemoteInbound([]Client{testClient("alice", "00000000-0000-4000-8000-000000000001")})}
	if _, err := n.ApplyMasterSync(remote, masterSyncMappings()); err != nil {
		t.Fatal(err)
	}
	empty := masterSyncRemoteInbound(nil)
	preview, err := n.PreviewMasterSync([]Inbound{empty}, masterSyncMappings())
	if err != nil {
		t.Fatal(err)
	}
	if got := preview.Mappings[0].PendingRemoval; got != 1 {
		t.Fatalf("pending removal = %d, want 1", got)
	}
	if _, err = n.ApplyMasterSync([]Inbound{empty}, masterSyncMappings()); err != nil {
		t.Fatal(err)
	}
	clients, err := n.Inbounds()[0].Clients()
	if err != nil || len(clients) != 1 || clients[0].Text("email") != "alice" {
		t.Fatalf("missing remote client was deleted: %+v, %v", clients, err)
	}
}

func TestMasterSyncRejectsIdentityConflictAndIncompletePayload(t *testing.T) {
	n, _ := testNode(t)
	conflict := testClient("alice", "00000000-0000-4000-8000-000000000099")
	before, _ := json.Marshal(n.Inbounds())
	if _, err := n.ApplyMasterSync([]Inbound{masterSyncRemoteInbound([]Client{conflict})}, masterSyncMappings()); err == nil || !strings.Contains(err.Error(), "different UUID") {
		t.Fatalf("identity conflict was accepted: %v", err)
	}
	after, _ := json.Marshal(n.Inbounds())
	if string(before) != string(after) {
		t.Fatal("identity conflict changed local state")
	}

	incomplete := masterSyncRemoteInbound(nil)
	incomplete.Settings = Object(`{"decryption":"none"}`)
	if _, err := n.PreviewMasterSync([]Inbound{incomplete}, masterSyncMappings()); err == nil || !strings.Contains(err.Error(), "clients field is missing") {
		t.Fatalf("incomplete master payload was accepted: %v", err)
	}
}

func TestLoadStateRejectsInvalidMasterSyncMetadata(t *testing.T) {
	n, _ := testNode(t)
	b, err := json.Marshal(&State{
		Schema: 1, GUID: "state", NextID: 2, NextClientID: 2,
		Inbounds: n.Inbounds(), Traffic: map[string]*Traffic{}, Globals: map[string]map[string]Global{},
		MasterSync: []MasterSyncState{{MappingID: "hk", MasterInboundID: 77, LocalInboundID: 1, Managed: []MasterManagedClient{{Email: "alice", UUID: "not-a-uuid"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := n.Config.StateFile
	if err = AtomicWrite(path, b, false); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadState(path); err == nil || !strings.Contains(err.Error(), "invalid stored master sync client") {
		t.Fatalf("invalid sync metadata was accepted: %v", err)
	}
}
