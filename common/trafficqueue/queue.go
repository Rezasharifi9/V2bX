// Package trafficqueue keeps unreported usage on disk until the panel accepts it.
package trafficqueue

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"

	"github.com/InazumaV/V2bX/api/panel"
)

type diskState struct {
	Version int                 `json:"version"`
	Traffic []panel.UserTraffic `json:"traffic"`
}

type Queue struct {
	mu      sync.Mutex
	path    string
	pending map[int]panel.UserTraffic
	// Remember accepted usage when clearing the file fails, so this process
	// retries the local acknowledgement without sending that usage twice.
	accepted []panel.UserTraffic
}

func DefaultDirectory() (string, error) {
	if runtime.GOOS == "linux" {
		return "/var/lib/V2bX/traffic", nil
	}
	dir, err := os.UserCacheDir()
	return filepath.Join(dir, "V2bX", "traffic"), err
}

func Open(dir, identity string) (*Queue, error) {
	if dir == "" {
		var err error
		dir, err = DefaultDirectory()
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(identity))
	q := &Queue{path: filepath.Join(dir, fmt.Sprintf("%x.json", key)), pending: make(map[int]panel.UserTraffic)}
	data, err := os.ReadFile(q.path)
	if errors.Is(err, os.ErrNotExist) {
		// Verify that durable writes work before starting the node.
		if err := q.save(q.pending); err != nil {
			return nil, err
		}
		return q, nil
	}
	if err != nil {
		return nil, err
	}
	var state diskState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("read traffic queue %s: %w", q.path, err)
	}
	if state.Version != 1 {
		return nil, fmt.Errorf("unsupported traffic queue version %d", state.Version)
	}
	for _, usage := range state.Traffic {
		if usage.UID <= 0 || usage.Upload < 0 || usage.Download < 0 {
			return nil, errors.New("invalid stored traffic")
		}
		if _, exists := q.pending[usage.UID]; exists {
			return nil, errors.New("duplicate stored traffic user")
		}
		q.pending[usage.UID] = usage
	}
	return q, nil
}

// Add must finish successfully before the caller subtracts usage from counters.
func (q *Queue) Add(traffic []panel.UserTraffic) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(traffic) == 0 {
		return nil
	}
	next := q.copyPending()
	for _, usage := range traffic {
		if usage.UID <= 0 || usage.Upload < 0 || usage.Download < 0 {
			return errors.New("invalid traffic")
		}
		old := next[usage.UID]
		if old.Upload > math.MaxInt64-usage.Upload || old.Download > math.MaxInt64-usage.Download {
			return errors.New("traffic counter overflow")
		}
		old.UID = usage.UID
		old.Upload += usage.Upload
		old.Download += usage.Download
		next[usage.UID] = old
	}
	if err := q.save(next); err != nil {
		return err
	}
	q.pending = next
	return nil
}

// Report retries pending usage on every call, including after process restarts.
// The panel API has no idempotency key; a lost response can still cause a retry
// of a request that the panel already applied (at-least-once delivery).
func (q *Queue) Report(minBytes int64, send func([]panel.UserTraffic) error) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.accepted) > 0 {
		if err := q.acknowledge(); err != nil {
			return err
		}
	}
	var batch []panel.UserTraffic
	for _, usage := range sorted(q.pending) {
		if usage.Upload > minBytes || usage.Download > minBytes-usage.Upload {
			batch = append(batch, usage)
		}
	}
	if len(batch) == 0 {
		return nil
	}
	if err := send(batch); err != nil {
		return err
	}
	q.accepted = batch
	return q.acknowledge()
}

func (q *Queue) acknowledge() error {
	next := q.copyPending()
	for _, usage := range q.accepted {
		old := next[usage.UID]
		old.Upload -= usage.Upload
		old.Download -= usage.Download
		if old.Upload == 0 && old.Download == 0 {
			delete(next, usage.UID)
		} else {
			next[usage.UID] = old
		}
	}
	if err := q.save(next); err != nil {
		return fmt.Errorf("persist accepted traffic: %w", err)
	}
	q.pending = next
	q.accepted = nil
	return nil
}

func (q *Queue) copyPending() map[int]panel.UserTraffic {
	next := make(map[int]panel.UserTraffic, len(q.pending))
	for uid, usage := range q.pending {
		next[uid] = usage
	}
	return next
}

func sorted(pending map[int]panel.UserTraffic) []panel.UserTraffic {
	traffic := make([]panel.UserTraffic, 0, len(pending))
	for _, usage := range pending {
		traffic = append(traffic, usage)
	}
	sort.Slice(traffic, func(i, j int) bool { return traffic[i].UID < traffic[j].UID })
	return traffic
}

func (q *Queue) save(pending map[int]panel.UserTraffic) error {
	data, err := json.Marshal(diskState{Version: 1, Traffic: sorted(pending)})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(q.path), ".traffic-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), q.path); err != nil {
		return err
	}
	// Flush directory metadata where supported. The file itself is always synced.
	if dir, err := os.Open(filepath.Dir(q.path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
