package node

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

var panelClientSupportedFields = map[string]struct{}{
	"id": {}, "email": {}, "enable": {}, "flow": {}, "security": {},
	"totalGB": {}, "expiryTime": {}, "subId": {}, "tgId": {}, "group": {},
	"comment": {}, "created_at": {}, "updated_at": {}, "trafficReset": {},
	"trafficResetDay": {},
}

// These fields may be present in a full-panel client payload, but are either
// traffic snapshots or bookkeeping values. They are not part of the leaf
// Xray client and must never overwrite the leaf's own counters.
var panelClientMetadataFields = map[string]struct{}{
	"up": {}, "down": {}, "total": {}, "lastOnline": {},
	"resetCount": {}, "lastSubFetch": {}, "clientStats": {},
}

var panelClientUnsupportedFields = map[string]struct{}{
	"limitIp": {}, "reset": {}, "resetDay": {}, "resetMax": {}, "hwidLimit": {},
	"deviceLimit": {}, "reverse": {}, "auth": {}, "privateKey": {}, "publicKey": {},
	"allowedIPs": {}, "allowedIPsByInbound": {}, "preSharedKey": {}, "keepAlive": {},
	"forwardedPorts": {}, "secret": {}, "adTag": {},
}

// normalizeVLESSClient projects the full panel's shared identity onto this
// VLESS-only node. Other protocol credentials are not enabled VLESS features.
// Keep validation strict for real limits and unknown options, and never alter
// the caller's shared client (it may also be attached to other protocols).
func normalizeVLESSClient(source Client) (Client, error) {
	normalized := Client{}
	for _, name := range slices.Sorted(maps.Keys(source)) {
		value := source[name]
		if name == "password" || name == "auth" || name == "secret" {
			continue
		}
		if _, ok := panelClientSupportedFields[name]; ok {
			// trafficResetDay=0 is emitted by some panel versions together with
			// trafficReset=never; 0 means the default, not a monthly day.
			if name == "trafficResetDay" && source.Text("trafficReset") != "" && source.Text("trafficReset") != "never" {
				normalized[name] = append(json.RawMessage(nil), value...)
				continue
			}
			if name == "trafficResetDay" && strings.TrimSpace(string(value)) == "0" && (source.Text("trafficReset") == "" || source.Text("trafficReset") == "never") {
				continue
			}
			normalized[name] = append(json.RawMessage(nil), value...)
			continue
		}
		if _, ok := panelClientMetadataFields[name]; ok || emptyJSON(value) {
			continue
		}
		if _, ok := panelClientUnsupportedFields[name]; ok {
			return nil, fmt.Errorf("unsupported client feature: %s", name)
		}
		return nil, fmt.Errorf("unsupported client setting %s", name)
	}
	if err := validateClient(normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}
