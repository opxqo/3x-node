package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type MasterManagedClient struct {
	Email string `json:"email"`
	UUID  string `json:"uuid"`
}

type MasterSyncState struct {
	MappingID       string                `json:"mappingId"`
	MasterInboundID int                   `json:"masterInboundId"`
	LocalInboundID  int                   `json:"localInboundId"`
	Managed         []MasterManagedClient `json:"managed,omitempty"`
}

type MasterSyncMappingPreview struct {
	MappingID       string   `json:"mappingId"`
	MasterInboundID int      `json:"masterInboundId"`
	LocalInboundID  int      `json:"localInboundId"`
	RemoteClients   int      `json:"remoteClients"`
	Added           int      `json:"added"`
	Updated         int      `json:"updated"`
	Unchanged       int      `json:"unchanged"`
	PendingRemoval  int      `json:"pendingRemoval"`
	Conflict        []string `json:"conflict,omitempty"`
}

type MasterSyncPreview struct {
	Mappings []MasterSyncMappingPreview `json:"mappings"`
}

func validateMasterSyncMappings(mappings []MasterSyncMapping) error {
	if len(mappings) == 0 || len(mappings) > MaxInbounds {
		return fmt.Errorf("master sync mappings must contain 1..%d entries", MaxInbounds)
	}
	seen, masters, locals := map[string]bool{}, map[int]bool{}, map[int]bool{}
	for _, m := range mappings {
		if m.ID == "" || strings.ContainsAny(m.ID, "/\\\x00") || seen[m.ID] {
			return errors.New("master sync mapping IDs must be non-empty and unique")
		}
		if m.MasterInboundID < 1 || m.LocalInboundID < 1 || masters[m.MasterInboundID] || locals[m.LocalInboundID] {
			return errors.New("master sync inbound IDs must be positive and unique")
		}
		seen[m.ID], masters[m.MasterInboundID], locals[m.LocalInboundID] = true, true, true
	}
	return nil
}

func cloneClient(c Client) Client {
	b, _ := json.Marshal(c)
	var clone Client
	_ = json.Unmarshal(b, &clone)
	return clone
}

func managedFor(state *State, mapping MasterSyncMapping) *MasterSyncState {
	for j := range state.MasterSync {
		if state.MasterSync[j].MappingID == mapping.ID {
			return &state.MasterSync[j]
		}
	}
	return nil
}

func managedUUID(state MasterSyncState, email string) string {
	for _, client := range state.Managed {
		if client.Email == email {
			return client.UUID
		}
	}
	return ""
}

func validateMasterSyncState(rows []MasterSyncState) error {
	if len(rows) > MaxInbounds {
		return fmt.Errorf("stored master sync mappings exceed %d", MaxInbounds)
	}
	seen, masters, locals := map[string]bool{}, map[int]bool{}, map[int]bool{}
	for _, row := range rows {
		if row.MappingID == "" || strings.ContainsAny(row.MappingID, "/\\\x00") || seen[row.MappingID] {
			return errors.New("invalid stored master sync mapping ID")
		}
		if row.MasterInboundID < 1 || row.LocalInboundID < 1 || masters[row.MasterInboundID] || locals[row.LocalInboundID] {
			return errors.New("invalid stored master sync inbound IDs")
		}
		seen[row.MappingID], masters[row.MasterInboundID], locals[row.LocalInboundID] = true, true, true
		if len(row.Managed) > MaxClients {
			return errors.New("stored master sync clients exceed node limit")
		}
		managedEmails, managedUUIDs := map[string]bool{}, map[string]bool{}
		for _, client := range row.Managed {
			if client.Email == "" || client.UUID == "" || managedEmails[client.Email] || managedUUIDs[client.UUID] {
				return errors.New("invalid stored master sync client identity")
			}
			candidate := Client{}
			candidate.Set("email", client.Email)
			candidate.Set("id", client.UUID)
			candidate.Set("enable", true)
			if err := validateClient(candidate); err != nil {
				return fmt.Errorf("invalid stored master sync client: %w", err)
			}
			managedEmails[client.Email], managedUUIDs[client.UUID] = true, true
		}
	}
	return nil
}

func upsertManaged(state *MasterSyncState, email, uuid string) {
	for j := range state.Managed {
		if state.Managed[j].Email == email {
			state.Managed[j].UUID = uuid
			return
		}
	}
	state.Managed = append(state.Managed, MasterManagedClient{Email: email, UUID: uuid})
}

func clientIndexByEmail(clients []Client, email string) int {
	for j := range clients {
		if clients[j].Text("email") == email {
			return j
		}
	}
	return -1
}

func clientIndexByUUID(clients []Client, uuid string) int {
	for j := range clients {
		if clients[j].Text("id") == uuid {
			return j
		}
	}
	return -1
}

func buildMasterSyncPlan(state *State, remote []Inbound, mappings []MasterSyncMapping) (MasterSyncPreview, error) {
	if err := validateMasterSyncMappings(mappings); err != nil {
		return MasterSyncPreview{}, err
	}
	remoteByID := make(map[int]Inbound, len(remote))
	for _, inbound := range remote {
		if _, exists := remoteByID[inbound.ID]; exists {
			return MasterSyncPreview{}, fmt.Errorf("duplicate master inbound ID %d", inbound.ID)
		}
		remoteByID[inbound.ID] = inbound
	}
	preview := MasterSyncPreview{Mappings: make([]MasterSyncMappingPreview, 0, len(mappings))}
	for _, mapping := range mappings {
		row := MasterSyncMappingPreview{MappingID: mapping.ID, MasterInboundID: mapping.MasterInboundID, LocalInboundID: mapping.LocalInboundID}
		local := state.Find(mapping.LocalInboundID)
		source, sourceOK := remoteByID[mapping.MasterInboundID]
		if local == nil {
			row.Conflict = append(row.Conflict, "local inbound not found")
		}
		if !sourceOK {
			row.Conflict = append(row.Conflict, "master inbound not found")
		}
		if local != nil && local.Protocol != "vless" {
			row.Conflict = append(row.Conflict, "local inbound is not VLESS")
		}
		var remoteClients []Client
		if sourceOK {
			var present bool
			var err error
			remoteClients, present, err = source.ClientList()
			if err != nil {
				row.Conflict = append(row.Conflict, "master clients field is malformed")
			} else if !present {
				row.Conflict = append(row.Conflict, "master clients field is missing")
			}
		}
		row.RemoteClients = len(remoteClients)
		owned := MasterSyncState{MappingID: mapping.ID, MasterInboundID: mapping.MasterInboundID, LocalInboundID: mapping.LocalInboundID}
		if previous := managedFor(state, mapping); previous != nil {
			owned = *previous
		}
		seenEmail, seenUUID := map[string]bool{}, map[string]bool{}
		var localClients []Client
		if local != nil {
			localClients, _ = local.Clients()
		}
		for _, remoteClient := range remoteClients {
			if err := validateClient(remoteClient); err != nil {
				row.Conflict = append(row.Conflict, "unsupported master client fields")
				continue
			}
			email, uuid := remoteClient.Text("email"), remoteClient.Text("id")
			if seenEmail[email] || seenUUID[uuid] {
				row.Conflict = append(row.Conflict, "duplicate client identity in master inbound")
				continue
			}
			seenEmail[email], seenUUID[uuid] = true, true
			byEmail, byUUID := clientIndexByEmail(localClients, email), clientIndexByUUID(localClients, uuid)
			if byEmail >= 0 && localClients[byEmail].Text("id") != uuid {
				if managedUUID(owned, email) == "" || managedUUID(owned, email) != localClients[byEmail].Text("id") {
					row.Conflict = append(row.Conflict, "client email maps to a different UUID locally")
					continue
				}
			}
			if byUUID >= 0 && localClients[byUUID].Text("email") != email {
				row.Conflict = append(row.Conflict, "client UUID maps to a different email locally")
				continue
			}
			if byEmail < 0 {
				row.Added++
			} else if !jsonEqualClient(localClients[byEmail], remoteClient) {
				row.Updated++
			} else {
				row.Unchanged++
			}
		}
		for _, managed := range owned.Managed {
			if !seenEmail[managed.Email] {
				row.PendingRemoval++
			}
		}
		preview.Mappings = append(preview.Mappings, row)
	}
	for _, row := range preview.Mappings {
		if len(row.Conflict) > 0 {
			return preview, fmt.Errorf("master sync has conflicts in mapping %s: %s", row.MappingID, strings.Join(row.Conflict, "; "))
		}
	}
	return preview, nil
}

func jsonEqualClient(a, b Client) bool {
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(aa) == string(bb)
}

func applyMasterSync(state *State, remote []Inbound, mappings []MasterSyncMapping) (MasterSyncPreview, error) {
	preview, err := buildMasterSyncPlan(state, remote, mappings)
	if err != nil {
		return preview, err
	}
	remoteByID := make(map[int]Inbound, len(remote))
	for _, inbound := range remote {
		remoteByID[inbound.ID] = inbound
	}
	for _, mapping := range mappings {
		local := state.Find(mapping.LocalInboundID)
		sourceClients, _, _ := remoteByID[mapping.MasterInboundID].ClientList()
		localClients, _ := local.Clients()
		owned := managedFor(state, mapping)
		if owned == nil {
			state.MasterSync = append(state.MasterSync, MasterSyncState{MappingID: mapping.ID, MasterInboundID: mapping.MasterInboundID, LocalInboundID: mapping.LocalInboundID})
			owned = &state.MasterSync[len(state.MasterSync)-1]
		}
		owned.MasterInboundID, owned.LocalInboundID = mapping.MasterInboundID, mapping.LocalInboundID
		for _, remoteClient := range sourceClients {
			idx := clientIndexByEmail(localClients, remoteClient.Text("email"))
			if idx >= 0 {
				localClients[idx] = cloneClient(remoteClient)
			} else {
				localClients = append(localClients, cloneClient(remoteClient))
			}
			upsertManaged(owned, remoteClient.Text("email"), remoteClient.Text("id"))
		}
		local.SetClients(localClients)
	}
	return preview, nil
}

// PreviewMasterSync computes changes without touching Xray or persistent
// state. The remote request must already have completed before taking n.mu.
func (n *Node) PreviewMasterSync(remote []Inbound, mappings []MasterSyncMapping) (MasterSyncPreview, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return buildMasterSyncPlan(n.state.Clone(), remote, mappings)
}

// ApplyMasterSync atomically applies a previously fetched remote snapshot. It
// never deletes missing clients; removals are recorded as pending in preview.
func (n *Node) ApplyMasterSync(remote []Inbound, mappings []MasterSyncMapping) (MasterSyncPreview, error) {
	obj, err := n.transaction(func(state *State) (any, error) {
		return applyMasterSync(state, remote, mappings)
	})
	if err != nil {
		return MasterSyncPreview{}, err
	}
	return obj.(MasterSyncPreview), nil
}
