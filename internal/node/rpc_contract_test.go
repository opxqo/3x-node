//go:build mastercontract

package node

import (
	command "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/proxy/vless"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestWireAdapterAgainstPinnedXrayProtos(t *testing.T) {
	account := &Account{ID: "00000000-0000-4000-8000-000000000001", Flow: "xtls-rprx-vision"}
	var request command.AlterInboundRequest
	if err := proto.Unmarshal(alterWire("inbound", "alice", account), &request); err != nil {
		t.Fatal(err)
	}
	if request.Tag != "inbound" || request.Operation.Type != "xray.app.proxyman.command.AddUserOperation" {
		t.Fatalf("request: %v", &request)
	}
	var add command.AddUserOperation
	if err := proto.Unmarshal(request.Operation.Value, &add); err != nil {
		t.Fatal(err)
	}
	if add.User.Email != "alice" || add.User.Level != 0 || add.User.Account.Type != "xray.proxy.vless.Account" {
		t.Fatal("wrong user envelope")
	}
	var got vless.Account
	if err := proto.Unmarshal(add.User.Account.Value, &got); err != nil {
		t.Fatal(err)
	}
	if got.Id != account.ID || got.Flow != account.Flow || got.Encryption != "none" {
		t.Fatalf("account: %v", &got)
	}
	var removeRequest command.AlterInboundRequest
	if err := proto.Unmarshal(alterWire("inbound", "alice", nil), &removeRequest); err != nil {
		t.Fatal(err)
	}
	var remove command.RemoveUserOperation
	if err := proto.Unmarshal(removeRequest.Operation.Value, &remove); err != nil {
		t.Fatal(err)
	}
	if remove.Email != "alice" || removeRequest.Operation.Type != "xray.app.proxyman.command.RemoveUserOperation" {
		t.Fatal("wrong remove")
	}
}
