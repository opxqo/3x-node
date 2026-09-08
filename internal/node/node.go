package node

import (
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"sync"
	"time"
)

type Node struct {
	mu         sync.Mutex
	Config     Config
	state      *State
	engine     Engine
	baseline   map[string]int64
	generation uint64
	started    time.Time
	save       func(string, *State) error
	metrics    Metrics
	management ManagementActivity
	halted     bool
}

func New(c Config, e Engine) (*Node, error) {
	s, err := LoadState(c.StateFile)
	if err != nil {
		return nil, err
	}
	n := &Node{Config: c, state: s, engine: e, baseline: map[string]int64{}, started: time.Now(), save: SaveState}
	if s.Running {
		log.Printf("unclean previous stop; traffic after checkpoint %d may be unaccounted (nominal sampling window 5s)", s.CheckpointAt)
	}
	s.Running = true
	if err = n.save(c.StateFile, s); err != nil {
		return nil, err
	}
	if err = e.Apply(n.desired(s)); err != nil {
		return nil, err
	}
	n.resetGeneration()
	return n, nil
}

func (n *Node) resetGeneration() {
	if c, ok := n.engine.(*Core); ok && c.Generation != n.generation {
		n.generation = c.Generation
		n.baseline = map[string]int64{}
	}
}

func (n *Node) desired(s *State) []LiveInbound {
	out := []LiveInbound{}
	now := time.Now().UnixMilli()
	for _, ib := range s.Inbounds {
		if !ib.Enable || (ib.Total > 0 && ib.Up >= ib.Total-ib.Down) || (ib.ExpiryTime > 0 && ib.ExpiryTime <= now) {
			continue
		}
		live := LiveInbound{Tag: ib.Tag, Listen: ib.Listen, Port: ib.Port, Stream: ib.StreamSettings, Users: map[string]Account{}}
		cs, _ := ib.Clients()
		for _, c := range cs {
			t := s.Traffic[c.Text("email")]
			if t != nil && c.Enabled() && !s.Exhausted(t, now) {
				flow := c.Text("flow")
				if ib.DisableFlow {
					flow = ""
				}
				live.Users[c.Text("email")] = Account{ID: c.Text("id"), Flow: flow}
			}
		}
		out = append(out, live)
	}
	return out
}

func (n *Node) transaction(fn func(*State) (any, error)) (any, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.halted {
		return nil, errors.New("node stopped after storage failure; repair storage and restart")
	}
	if n.engine.Running() {
		if err := n.sampleLocked(); err != nil {
			return nil, err
		}
	}
	next := n.state.Clone()
	obj, err := fn(next)
	if err != nil {
		return nil, err
	}
	if len(next.Inbounds) > MaxInbounds {
		return nil, fmt.Errorf("node supports at most %d inbounds", MaxInbounds)
	}
	ports := map[int]bool{}
	tags := map[string]bool{}
	for j := range next.Inbounds {
		ib := &next.Inbounds[j]
		if err = ValidateInbound(ib); err != nil {
			return nil, err
		}
		if ib.Port == n.Config.APIPort {
			return nil, errors.New("inbound conflicts with private Xray API port")
		}
		if ports[ib.Port] || tags[ib.Tag] {
			return nil, errors.New("duplicate inbound port or tag")
		}
		ports[ib.Port] = true
		tags[ib.Tag] = true
	}
	if err = next.SyncClients(); err != nil {
		return nil, err
	}
	if reflect.DeepEqual(n.state, next) {
		return obj, nil
	}
	oldLive := n.desired(n.state)
	if err = n.engine.Apply(n.desired(next)); err != nil {
		n.resetGeneration()
		return nil, err
	}
	n.resetGeneration()
	if err = n.save(n.Config.StateFile, next); err != nil {
		if errors.Is(err, ErrDurability) {
			n.halted = true
			_ = n.engine.Stop()
			return nil, err
		}
		if rollback := n.engine.Apply(oldLive); rollback != nil {
			_ = n.engine.Stop()
			return nil, fmt.Errorf("state write and rollback failed; core stopped: %w", errors.Join(err, rollback))
		}
		n.resetGeneration()
		return nil, fmt.Errorf("state write failed; runtime restored: %w", err)
	}
	n.state = next
	return obj, nil
}

func (n *Node) sampleLocked() error {
	n.resetGeneration()
	values, err := n.engine.Stats()
	if err != nil {
		return err
	}
	next := n.state.Clone()
	now := time.Now().UnixMilli()
	for key, value := range values {
		if value < 0 {
			continue
		}
		previous := n.baseline[key]
		delta := value - previous
		if delta < 0 {
			delta = value
		}
		if delta == 0 {
			continue
		}
		parts := strings.Split(key, ">>>")
		if len(parts) != 4 || parts[2] != "traffic" {
			continue
		}
		if parts[0] == "user" {
			t := next.Traffic[parts[1]]
			if t == nil {
				continue
			}
			if parts[3] == "uplink" {
				t.Up += delta
			} else if parts[3] == "downlink" {
				t.Down += delta
			} else {
				continue
			}
			t.LastOnline = now
			if t.ExpiryTime < 0 {
				t.ExpiryTime = now - t.ExpiryTime
				n.setClientField(next, t.Email, "expiryTime", t.ExpiryTime)
			}
		} else if parts[0] == "inbound" {
			for j := range next.Inbounds {
				ib := &next.Inbounds[j]
				if ib.Tag == parts[1] {
					if parts[3] == "uplink" {
						ib.Up += delta
					} else if parts[3] == "downlink" {
						ib.Down += delta
					}
				}
			}
		}
	}
	for email, t := range next.Traffic {
		if t.Enable && next.Exhausted(t, now) {
			t.Enable = false
			n.setClientField(next, email, "enable", false)
		}
	}
	for j := range next.Inbounds {
		ib := &next.Inbounds[j]
		if (ib.Total > 0 && ib.Up >= ib.Total-ib.Down) || (ib.ExpiryTime > 0 && ib.ExpiryTime <= now) {
			ib.Enable = false
		}
	}
	for master, rows := range next.Globals {
		for email, g := range rows {
			if g.UpdatedAt < now-int64(24*time.Hour/time.Millisecond) {
				delete(rows, email)
			}
		}
		if len(rows) == 0 {
			delete(next.Globals, master)
		}
	}
	if !reflect.DeepEqual(n.state, next) {
		if err = n.engine.Apply(n.desired(next)); err != nil {
			return err
		}
		if err = n.save(n.Config.StateFile, next); err != nil {
			n.halted = true
			_ = n.engine.Stop()
			return fmt.Errorf("traffic checkpoint failed; stopped to avoid unaccounted traffic: %w", err)
		}
		n.state = next
	}
	n.baseline = values
	n.resetGeneration()
	return nil
}

func (n *Node) setClientField(s *State, email, key string, v any) {
	for j := range s.Inbounds {
		ib := &s.Inbounds[j]
		cs, _ := ib.Clients()
		for _, c := range cs {
			if c.Text("email") == email {
				c.Set(key, v)
			}
		}
		ib.SetClients(cs)
	}
}

func (n *Node) Run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var events <-chan struct{}
	if c, ok := n.engine.(*Core); ok {
		events = c.Events
	}
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			n.mu.Lock()
			if n.engine.Running() {
				if err := n.sampleLocked(); err != nil {
					log.Printf("final checkpoint: %v", err)
				}
			}
			_ = n.engine.Stop()
			if !n.halted {
				n.state.Running = false
				if err := n.save(n.Config.StateFile, n.state); err != nil {
					log.Printf("clean shutdown marker: %v", err)
				}
			}
			n.mu.Unlock()
			return
		case <-ticker.C:
		case <-events:
		}
		n.mu.Lock()
		if n.halted {
			n.mu.Unlock()
			continue
		}
		if !n.engine.Running() {
			n.mu.Unlock()
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				continue
			case <-timer.C:
			}
			n.mu.Lock()
			if err := n.engine.Apply(n.desired(n.state)); err != nil {
				log.Printf("Xray recovery: %v", err)
				backoff = min(backoff*2, time.Minute)
			} else {
				backoff = time.Second
				n.resetGeneration()
			}
		} else if err := n.sampleLocked(); err != nil {
			log.Printf("node sampling: %v", err)
		}
		n.mu.Unlock()
	}
}

func (n *Node) Inbounds() []Inbound {
	n.mu.Lock()
	defer n.mu.Unlock()
	s := n.state.Clone()
	for j := range s.Inbounds {
		ib := &s.Inbounds[j]
		ib.ClientStats = []Traffic{}
		cs, _ := ib.Clients()
		for _, c := range cs {
			if t := s.Traffic[c.Text("email")]; t != nil {
				copy := *t
				copy.InboundID = ib.ID
				ib.ClientStats = append(ib.ClientStats, copy)
			}
		}
	}
	return s.Inbounds
}

func (n *Node) PutInbound(ib Inbound, id int) (any, error) {
	return n.transaction(func(s *State) (any, error) {
		if ib.Protocol == "vless" {
			clients, err := ib.Clients()
			if err != nil {
				return nil, err
			}
			for j, client := range clients {
				clients[j], err = normalizeVLESSClient(client)
				if err != nil {
					return nil, err
				}
			}
			ib.SetClients(clients)
		}
		if err := n.injectDefaultClient(&ib); err != nil {
			return nil, err
		}
		if id == 0 {
			for _, old := range s.Inbounds {
				if old.Tag == ib.Tag {
					id = old.ID
					break
				}
			}
		}
		if id > 0 {
			old := s.Find(id)
			if old == nil {
				return nil, errors.New("inbound not found")
			}
			ib.ID = id
			ib.Up, ib.Down = old.Up, old.Down
			*old = ib
		} else {
			ib.ID = s.NextID
			s.NextID++
			ib.Up, ib.Down = 0, 0
			s.Inbounds = append(s.Inbounds, ib)
		}
		ib.ClientStats = nil
		return ib, nil
	})
}

// injectDefaultClient is a leaf-only compatibility layer for master panels
// which create a VLESS inbound before they send a matching client record. It
// deliberately acts only on an empty client list, so a real client payload or
// manually managed inbound is never overwritten.
func (n *Node) injectDefaultClient(ib *Inbound) error {
	if ib.Protocol != "vless" || n.Config.DefaultClientUUID == "" {
		return nil
	}
	clients, err := ib.Clients()
	if err != nil {
		return err
	}
	if len(clients) != 0 {
		return nil
	}
	ib.SetClients([]Client{n.Config.DefaultClient()})
	return nil
}

func (n *Node) DeleteInbound(id int) (any, error) {
	return n.transaction(func(s *State) (any, error) {
		for j, ib := range s.Inbounds {
			if ib.ID == id {
				s.Inbounds = append(s.Inbounds[:j], s.Inbounds[j+1:]...)
				return nil, nil
			}
		}
		return nil, nil
	})
}

func (n *Node) ChangeClient(oldEmail string, c Client, ids []int, op string) (any, error) {
	return n.transaction(func(s *State) (any, error) {
		if op == "add" || op == "update" {
			var err error
			c, err = normalizeVLESSClient(c)
			if err != nil {
				return nil, err
			}
		}
		if (op == "add" || op == "detach") && len(ids) == 0 {
			return nil, errors.New("inboundIds required")
		}
		for _, id := range ids {
			if s.Find(id) == nil {
				return nil, errors.New("inbound not found")
			}
		}
		if op == "update" && s.Traffic[oldEmail] == nil {
			return nil, errors.New("client not found")
		}
		if op == "update" && c.Text("email") != oldEmail {
			if s.Traffic[c.Text("email")] != nil {
				return nil, errors.New("client email already exists")
			}
			if t := s.Traffic[oldEmail]; t != nil {
				delete(s.Traffic, oldEmail)
				t.Email = c.Text("email")
				s.Traffic[t.Email] = t
				s.ClearGlobals(oldEmail)
			}
		}
		for j := range s.Inbounds {
			ib := &s.Inbounds[j]
			selected := len(ids) == 0
			for _, id := range ids {
				if ib.ID == id {
					selected = true
				}
			}
			cs, _ := ib.Clients()
			out := []Client{}
			found := false
			for _, prev := range cs {
				match := prev.Text("email") == oldEmail || (op == "add" && prev.Text("email") == c.Text("email"))
				if match {
					found = true
					switch op {
					case "delete":
						continue
					case "detach":
						if selected {
							continue
						}
					case "update":
						prev = c
					case "add":
						if selected {
							prev = c
						}
					}
				}
				out = append(out, prev)
			}
			if op == "add" && selected && !found {
				out = append(out, c)
			}
			ib.SetClients(out)
		}
		return nil, nil
	})
}

func (n *Node) ResetTraffic(email string, id int) (any, error) {
	return n.transaction(func(s *State) (any, error) {
		if email != "" && s.Traffic[email] == nil {
			return nil, errors.New("client not found")
		}
		if id > 0 && s.Find(id) == nil {
			return nil, errors.New("inbound not found")
		}
		// Master inbound resets affect inbound totals, not per-client usage or enabled state.
		if email == "" {
			for j := range s.Inbounds {
				if id == 0 || s.Inbounds[j].ID == id {
					s.Inbounds[j].Up, s.Inbounds[j].Down = 0, 0
				}
			}
			return nil, nil
		}
		emails := map[string]bool{}
		for j := range s.Inbounds {
			ib := &s.Inbounds[j]
			if id == 0 || id == ib.ID {
				if email == "" {
					ib.Up, ib.Down = 0, 0
					ib.Enable = true
				}
				cs, _ := ib.Clients()
				for _, c := range cs {
					if email == "" || c.Text("email") == email {
						emails[c.Text("email")] = true
					}
				}
			}
		}
		for e := range emails {
			if t := s.Traffic[e]; t != nil {
				t.Up, t.Down = 0, 0
				t.Enable = true
				s.ClearGlobals(e)
				n.setClientField(s, e, "enable", true)
			}
		}
		return nil, nil
	})
}

func (n *Node) PushGlobals(master string, rows []Traffic) (any, error) {
	return n.transaction(func(s *State) (any, error) {
		if master == "" || len(master) > 128 {
			return nil, errors.New("invalid masterGuid")
		}
		if len(s.Globals) >= 8 && s.Globals[master] == nil {
			return nil, errors.New("too many master identities")
		}
		if s.Globals[master] == nil {
			s.Globals[master] = map[string]Global{}
		}
		for _, t := range rows {
			if t.Up < 0 || t.Down < 0 {
				return nil, errors.New("negative traffic")
			}
			if s.Traffic[t.Email] != nil {
				s.Globals[master][t.Email] = Global{Up: t.Up, Down: t.Down, UpdatedAt: time.Now().UnixMilli()}
			}
		}
		return nil, nil
	})
}

func (n *Node) Online(kind string) any {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := time.Now().UnixMilli()
	emails := []string{}
	last := map[string]int64{}
	tags := []string{}
	for email, t := range n.state.Traffic {
		last[email] = t.LastOnline
		if t.LastOnline > now-30000 {
			emails = append(emails, email)
		}
	}
	for _, ib := range n.state.Inbounds {
		cs, _ := ib.Clients()
		for _, c := range cs {
			if t := n.state.Traffic[c.Text("email")]; t != nil && t.LastOnline > now-30000 {
				tags = append(tags, ib.Tag)
				break
			}
		}
	}
	switch kind {
	case "lastOnline":
		return last
	case "onlinesByGuid":
		return map[string][]string{n.state.GUID: emails}
	case "activeInbounds":
		return map[string][]string{n.state.GUID: tags}
	default:
		return emails
	}
}

func (n *Node) Restart() (any, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.halted {
		return nil, errors.New("repair storage and restart the node service")
	}
	if n.engine.Running() {
		if err := n.sampleLocked(); err != nil {
			return nil, err
		}
	}
	if err := n.engine.Stop(); err != nil {
		return nil, err
	}
	err := n.engine.Apply(n.desired(n.state))
	n.resetGeneration()
	return nil, err
}
