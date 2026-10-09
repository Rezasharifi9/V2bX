package trafficqueue

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
)

func mustOpen(t *testing.T, dir, identity string) *Queue {
	t.Helper()
	q, err := Open(dir, identity)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestPanelOutageRestartAndRecovery(t *testing.T) {
	var status, calls atomic.Int64
	status.Store(http.StatusServiceUnavailable)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/server/UniProxy/push" || r.Method != "POST" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(`{"data":true}`))
	}))
	defer server.Close()
	client, err := panel.New(&conf.ApiConfig{APIHost: server.URL, NodeType: "vmess", NodeID: 1})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	q := mustOpen(t, dir, "node-1")
	first := panel.UserTraffic{UID: 7, Upload: 100, Download: 200}
	if err := q.Add([]panel.UserTraffic{first}); err != nil {
		t.Fatal(err)
	}
	if err := q.Report(0, client.ReportUserTraffic); err == nil {
		t.Fatal("failed HTTP report was accepted")
	}
	q = mustOpen(t, dir, "node-1") // Simulate a process restart during the outage.
	if !reflect.DeepEqual(q.pending[7], first) {
		t.Fatalf("lost stored usage: %+v", q.pending)
	}
	if err := q.Add([]panel.UserTraffic{{UID: 7, Upload: 10, Download: 20}}); err != nil {
		t.Fatal(err)
	}
	status.Store(http.StatusOK)
	if err := q.Report(0, func(batch []panel.UserTraffic) error {
		want := []panel.UserTraffic{{UID: 7, Upload: 110, Download: 220}}
		if !reflect.DeepEqual(batch, want) {
			t.Fatalf("got %+v, want %+v", batch, want)
		}
		return client.ReportUserTraffic(batch)
	}); err != nil {
		t.Fatal(err)
	}
	q = mustOpen(t, dir, "node-1")
	if len(q.pending) != 0 {
		t.Fatalf("accepted traffic still pending: %+v", q.pending)
	}
	if err := q.Report(0, client.ReportUserTraffic); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected failure and recovery only, got %d requests", calls.Load())
	}
}

func TestFailedDiskWriteRetainsPreviousQueue(t *testing.T) {
	dir := t.TempDir()
	q := mustOpen(t, dir, "node")
	first := panel.UserTraffic{UID: 1, Upload: 100}
	if err := q.Add([]panel.UserTraffic{first}); err != nil {
		t.Fatal(err)
	}
	goodPath := q.path
	q.path = filepath.Join(dir, "missing", "queue.json")
	if err := q.Add([]panel.UserTraffic{{UID: 1, Upload: 200}}); err == nil {
		t.Fatal("expected disk error")
	}
	q.path = goodPath
	if q.pending[1] != first {
		t.Fatal("failed write changed in-memory state")
	}
	q = mustOpen(t, dir, "node")
	if q.pending[1] != first {
		t.Fatal("failed write changed durable state")
	}
}

func TestAcceptedTrafficIsNotResentWhenAcknowledgementWriteFails(t *testing.T) {
	dir := t.TempDir()
	q := mustOpen(t, dir, "node")
	if err := q.Add([]panel.UserTraffic{{UID: 1, Upload: 100}}); err != nil {
		t.Fatal(err)
	}
	goodPath := q.path
	calls := 0
	err := q.Report(0, func([]panel.UserTraffic) error {
		calls++
		q.path = filepath.Join(dir, "missing", "queue.json")
		return nil
	})
	if err == nil {
		t.Fatal("expected acknowledgement disk error")
	}
	q.path = goodPath
	if err := q.Add([]panel.UserTraffic{{UID: 1, Upload: 20}}); err != nil {
		t.Fatal(err)
	}
	if err := q.Report(0, func(batch []panel.UserTraffic) error {
		calls++
		if len(batch) != 1 || batch[0].Upload != 20 {
			t.Fatalf("accepted usage resent: %+v", batch)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("unexpected request count")
	}
	if len(mustOpen(t, dir, "node").pending) != 0 {
		t.Fatal("queue not cleared")
	}
}

func TestThresholdAndNodeIsolation(t *testing.T) {
	dir := t.TempDir()
	q := mustOpen(t, dir, "node-1")
	if err := q.Add([]panel.UserTraffic{{UID: 1, Upload: 60}, {UID: 2, Download: 200}}); err != nil {
		t.Fatal(err)
	}
	if err := q.Report(100, func(batch []panel.UserTraffic) error {
		if len(batch) != 1 || batch[0].UID != 2 {
			t.Fatalf("wrong batch: %+v", batch)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	q = mustOpen(t, dir, "node-1")
	if q.pending[1].Upload != 60 {
		t.Fatal("below-threshold usage lost")
	}
	if len(mustOpen(t, dir, "node-2").pending) != 0 {
		t.Fatal("node queues mixed")
	}
	if err := q.Add([]panel.UserTraffic{{UID: 1, Download: 50}}); err != nil {
		t.Fatal(err)
	}
	if err := q.Report(100, func(batch []panel.UserTraffic) error {
		if len(batch) != 1 || batch[0].Upload != 60 || batch[0].Download != 50 {
			t.Fatalf("usage did not accumulate: %+v", batch)
		}
		return errors.New("offline")
	}); err == nil {
		t.Fatal("expected send failure")
	}
	if len(mustOpen(t, dir, "node-1").pending) != 1 {
		t.Fatal("send failure cleared queue")
	}
}

func TestCorruptQueueIsPreserved(t *testing.T) {
	dir := t.TempDir()
	q := mustOpen(t, dir, "node")
	bad := []byte("broken json")
	if err := os.WriteFile(q.path, bad, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, "node"); err == nil {
		t.Fatal("corrupt queue silently discarded")
	}
	got, err := os.ReadFile(q.path)
	if err != nil || !reflect.DeepEqual(got, bad) {
		t.Fatal("corrupt queue overwritten")
	}
}
