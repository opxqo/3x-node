package node

import (
	"encoding/json"
	"github.com/opxqo/3x-node/v3/internal/networkpath"
	"time"
)

func (n *Node) NetworkTopology() networkpath.Bundle {
	n.mu.Lock()
	defer n.mu.Unlock()
	local := networkpath.Source{GUID: n.state.GUID, Local: true, Status: "invalid", CapturedAt: time.Now().UTC().Format(time.RFC3339)}
	// NewCore constructs a config builder only; it neither starts nor probes Xray.
	cfg := NewCore(n.Config).xrayConfig(n.desired(n.state))
	ins := []any{}
	for _, ib := range n.state.Inbounds {
		ins = append(ins, map[string]any{"id": ib.ID, "tag": ib.Tag, "aliases": []string{ib.Tag}, "enable": ib.Enable, "remark": ib.Remark, "port": ib.Port, "protocol": ib.Protocol})
	}
	cfg["inbounds"] = ins
	raw, _ := json.Marshal(cfg)
	clean, err := networkpath.Sanitize(raw)
	if err == nil {
		local.Status = "ok"
		local.Config = clean
	}
	return networkpath.Bundle{Version: 1, Sources: []networkpath.Source{local}}
}
