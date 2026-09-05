package node

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

var ErrDurability = errors.New("replacement completed but directory durability is uncertain")

func RandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func LoadState(path string) (*State, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		if _, backupErr := os.Stat(path + ".previous"); backupErr == nil {
			return nil, errors.New("primary state missing; restore previous state explicitly")
		}
		return &State{Schema: 1, GUID: RandomHex(16), NextID: 1, NextClientID: 1, Inbounds: []Inbound{}, Traffic: map[string]*Traffic{}, Globals: map[string]map[string]Global{}}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4*MaxBody+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 4*MaxBody {
		return nil, errors.New("state exceeds size limit")
	}
	var s State
	if err = json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("invalid state; previous snapshot retained: %w", err)
	}
	if s.Schema != 1 || s.GUID == "" || s.NextID < 1 || s.NextClientID < 1 || s.Traffic == nil || s.Globals == nil {
		return nil, errors.New("invalid state schema")
	}
	if len(s.Inbounds) > MaxInbounds || len(s.Traffic) > MaxClients || len(s.Globals) > 8 {
		return nil, errors.New("stored state exceeds limits")
	}
	ids, ports, tags := map[int]bool{}, map[int]bool{}, map[string]bool{}
	for j := range s.Inbounds {
		ib := s.Inbounds[j]
		if ib.ID < 1 || ib.ID >= s.NextID || ids[ib.ID] || ports[ib.Port] || tags[ib.Tag] {
			return nil, errors.New("invalid stored inbound identity")
		}
		ids[ib.ID], ports[ib.Port], tags[ib.Tag] = true, true, true
		if err = ValidateInbound(&s.Inbounds[j]); err != nil {
			return nil, fmt.Errorf("invalid stored inbound: %w", err)
		}
	}
	for email, t := range s.Traffic {
		if t == nil || t.Email != email || t.Up < 0 || t.Down < 0 || t.ID < 1 || t.ID >= s.NextClientID {
			return nil, errors.New("invalid stored traffic")
		}
	}
	for _, rows := range s.Globals {
		if len(rows) > MaxClients {
			return nil, errors.New("stored global traffic exceeds limit")
		}
		for _, g := range rows {
			if g.Up < 0 || g.Down < 0 {
				return nil, errors.New("invalid stored global traffic")
			}
		}
	}
	return &s, nil
}

// AtomicWrite keeps the prior file until the replacement has been synced.
func AtomicWrite(path string, b []byte, backup bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".node-write-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if backup {
		old, readErr := os.ReadFile(path)
		if readErr == nil {
			if err = AtomicWrite(path+".previous", old, false); err != nil {
				return err
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return errors.Join(ErrDurability, err)
	}
	defer d.Close()
	if err = d.Sync(); err != nil {
		return errors.Join(ErrDurability, err)
	}
	return nil
}

func SaveState(path string, s *State) error {
	s.CheckpointAt = time.Now().UnixMilli()
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if len(b) > 4*MaxBody {
		return errors.New("state exceeds size limit")
	}
	return AtomicWrite(path, b, true)
}
