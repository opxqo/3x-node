package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestMasterSyncWorkerRunsPreviewAndApply(t *testing.T) {
	n, _ := testNode(t)
	remote := []Inbound{masterSyncRemoteInbound([]Client{testClient("alice", "00000000-0000-4000-8000-000000000001")})}
	var fetches atomic.Int32
	worker, err := NewMasterSyncWorker(n, MasterSyncConfig{
		Enabled:         true,
		BaseURL:         "https://master.example",
		TokenFile:       "/tmp/token",
		CertSHA256:      "0000000000000000000000000000000000000000000000000000000000000000",
		IntervalSeconds: DefaultMasterSyncInterval,
		Mappings:        masterSyncMappings(),
	}, func(context.Context, MasterSyncConfig) ([]Inbound, error) {
		fetches.Add(1)
		return remote, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = worker.Preview(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.SyncNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := worker.Status()
	if fetches.Load() != 2 || status.LastSuccessUnix == 0 || status.ConsecutiveFailures != 0 || status.LastError != "" {
		t.Fatalf("unexpected worker status: fetches=%d status=%+v", fetches.Load(), status)
	}
	if len(n.Inbounds()[0].ClientStats) != 1 {
		t.Fatal("worker did not apply remote client")
	}
}

func TestMasterSyncWorkerRecordsFetchAndApplyFailures(t *testing.T) {
	n, _ := testNode(t)
	worker, err := NewMasterSyncWorker(n, MasterSyncConfig{
		Enabled:    true,
		BaseURL:    "https://master.example",
		TokenFile:  "/tmp/token",
		CertSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		Mappings:   masterSyncMappings(),
	}, func(context.Context, MasterSyncConfig) ([]Inbound, error) {
		return nil, errors.New("fetch failed")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = worker.SyncNow(context.Background()); err == nil {
		t.Fatal("fetch failure was hidden")
	}
	status := worker.Status()
	if status.ConsecutiveFailures != 1 || status.LastError != "fetch failed" {
		t.Fatalf("fetch failure not recorded: %+v", status)
	}

	worker.fetch = func(context.Context, MasterSyncConfig) ([]Inbound, error) {
		return []Inbound{masterSyncRemoteInbound([]Client{testClient("alice", "00000000-0000-4000-8000-000000000099")})}, nil
	}
	if _, err = worker.SyncNow(context.Background()); err == nil {
		t.Fatal("apply conflict was hidden")
	}
	status = worker.Status()
	if status.ConsecutiveFailures != 2 || status.LastError == "" {
		t.Fatalf("apply failure not recorded: %+v", status)
	}
}

func TestMasterSyncManagementAPIIsLoopbackOnly(t *testing.T) {
	n, _ := testNode(t)
	remote := []Inbound{masterSyncRemoteInbound([]Client{testClient("alice", "00000000-0000-4000-8000-000000000001")})}
	worker, err := NewMasterSyncWorker(n, MasterSyncConfig{
		Enabled:         true,
		BaseURL:         "https://master.example",
		TokenFile:       "/tmp/token",
		CertSHA256:      "0000000000000000000000000000000000000000000000000000000000000000",
		IntervalSeconds: DefaultMasterSyncInterval,
		Mappings:        masterSyncMappings(),
	}, func(context.Context, MasterSyncConfig) ([]Inbound, error) { return remote, nil })
	if err != nil {
		t.Fatal(err)
	}
	n.AttachMasterSyncWorker(worker)
	handler := n.Handler()
	call := func(remoteAddr, method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(nil))
		r.RemoteAddr = remoteAddr
		r.Header.Set("Authorization", "Bearer "+n.Config.Token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := call("10.0.0.2:1234", http.MethodGet, "/panel/api/sync/status"); w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("local-only")) {
		t.Fatalf("remote sync status was accepted: code=%d body=%s", w.Code, w.Body.String())
	}
	w := call("127.0.0.1:1234", http.MethodPost, "/panel/api/sync/preview")
	var envelope struct {
		Success bool              `json:"success"`
		Obj     MasterSyncPreview `json:"obj"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || !envelope.Success || len(envelope.Obj.Mappings) != 1 {
		t.Fatalf("local sync preview failed: code=%d body=%s err=%v", w.Code, w.Body.String(), err)
	}
}
