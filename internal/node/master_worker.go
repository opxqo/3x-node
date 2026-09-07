package node

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

const (
	masterSyncRequestTimeout = 12 * time.Second
	masterSyncMaxBackoff     = 15 * time.Minute
)

type MasterSyncFetcher func(context.Context, MasterSyncConfig) ([]Inbound, error)

type MasterSyncStatus struct {
	Enabled             bool              `json:"enabled"`
	InProgress          bool              `json:"inProgress"`
	LastAttemptUnix     int64             `json:"lastAttemptUnix,omitempty"`
	LastSuccessUnix     int64             `json:"lastSuccessUnix,omitempty"`
	ConsecutiveFailures int               `json:"consecutiveFailures"`
	LastError           string            `json:"lastError,omitempty"`
	LastPreview         MasterSyncPreview `json:"lastPreview,omitempty"`
}

type MasterSyncWorker struct {
	node     *Node
	cfg      MasterSyncConfig
	fetch    MasterSyncFetcher
	runMu    sync.Mutex
	statusMu sync.Mutex
	status   MasterSyncStatus
}

func NewMasterSyncWorker(n *Node, cfg MasterSyncConfig, fetch MasterSyncFetcher) (*MasterSyncWorker, error) {
	if n == nil {
		return nil, errors.New("master sync node is required")
	}
	if err := ValidateMasterSync(cfg); err != nil {
		return nil, err
	}
	if fetch == nil {
		fetch = FetchMasterInbounds
	}
	return &MasterSyncWorker{
		node:   n,
		cfg:    cfg,
		fetch:  fetch,
		status: MasterSyncStatus{Enabled: cfg.Enabled},
	}, nil
}

func (w *MasterSyncWorker) Status() MasterSyncStatus {
	w.statusMu.Lock()
	defer w.statusMu.Unlock()
	return w.status
}

func (w *MasterSyncWorker) run(ctx context.Context, apply bool) (MasterSyncPreview, error) {
	w.runMu.Lock()
	defer w.runMu.Unlock()
	if !w.cfg.Enabled {
		return MasterSyncPreview{}, errors.New("master sync is disabled")
	}
	w.statusMu.Lock()
	w.status.InProgress = true
	w.status.LastAttemptUnix = time.Now().Unix()
	w.statusMu.Unlock()
	defer func() {
		w.statusMu.Lock()
		w.status.InProgress = false
		w.statusMu.Unlock()
	}()

	remote, err := w.fetch(ctx, w.cfg)
	if err == nil {
		if apply {
			var preview MasterSyncPreview
			preview, err = w.node.ApplyMasterSync(remote, w.cfg.Mappings)
			if err == nil {
				w.recordSuccess(preview)
			} else {
				w.recordFailure(err)
			}
			return preview, err
		}
		preview, previewErr := w.node.PreviewMasterSync(remote, w.cfg.Mappings)
		if previewErr == nil {
			w.recordSuccess(preview)
		} else {
			w.recordFailure(previewErr)
		}
		return preview, previewErr
	}
	w.recordFailure(err)
	return MasterSyncPreview{}, err
}

func (w *MasterSyncWorker) recordSuccess(preview MasterSyncPreview) {
	w.statusMu.Lock()
	w.status.LastSuccessUnix = time.Now().Unix()
	w.status.ConsecutiveFailures = 0
	w.status.LastError = ""
	w.status.LastPreview = preview
	w.statusMu.Unlock()
}

func (w *MasterSyncWorker) recordFailure(err error) {
	w.statusMu.Lock()
	w.status.ConsecutiveFailures++
	w.status.LastError = safeMasterSyncError(err)
	w.statusMu.Unlock()
}

func safeMasterSyncError(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (w *MasterSyncWorker) Preview(ctx context.Context) (MasterSyncPreview, error) {
	return w.run(ctx, false)
}

func (w *MasterSyncWorker) SyncNow(ctx context.Context) (MasterSyncPreview, error) {
	return w.run(ctx, true)
}

func (w *MasterSyncWorker) Run(ctx context.Context) {
	if !w.cfg.Enabled {
		return
	}
	failures := 0
	for {
		requestCtx, cancel := context.WithTimeout(ctx, masterSyncRequestTimeout)
		_, err := w.SyncNow(requestCtx)
		cancel()
		if err != nil {
			failures++
			log.Printf("master sync: %s", safeMasterSyncError(err))
		} else {
			failures = 0
		}
		delay := w.cfg.IntervalSeconds
		if delay == 0 {
			delay = DefaultMasterSyncInterval
		}
		for j := 0; j < failures; j++ {
			if delay >= int(masterSyncMaxBackoff/time.Second)/2 {
				delay = int(masterSyncMaxBackoff / time.Second)
				break
			}
			delay *= 2
		}
		if delay > int(masterSyncMaxBackoff/time.Second) {
			delay = int(masterSyncMaxBackoff / time.Second)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(delay) * time.Second):
		}
	}
}
