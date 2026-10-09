package node

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/common/counter"
	"github.com/InazumaV/V2bX/common/trafficqueue"
	"github.com/InazumaV/V2bX/conf"
	"github.com/InazumaV/V2bX/core"
	"github.com/InazumaV/V2bX/limiter"
)

type trafficTestCore struct {
	counts  *counter.TrafficCounter
	deleted bool
}

func (f *trafficTestCore) Start() error                                             { return nil }
func (f *trafficTestCore) Close() error                                             { return nil }
func (f *trafficTestCore) AddNode(string, *panel.NodeInfo, *conf.Options) error     { return nil }
func (f *trafficTestCore) DelNode(string) error                                     { f.deleted = true; return nil }
func (f *trafficTestCore) AddUsers(*core.AddUsersParams) (int, error)               { return 0, nil }
func (f *trafficTestCore) DelUsers([]panel.UserInfo, string, *panel.NodeInfo) error { return nil }
func (f *trafficTestCore) Protocols() []string                                      { return []string{"vmess"} }
func (f *trafficTestCore) Type() string                                             { return "test" }
func (f *trafficTestCore) GetUserTrafficSlice(string, bool) ([]panel.UserTraffic, error) {
	panic("reporting must not use destructive counter reset")
}
func (f *trafficTestCore) CollectUserTraffic(_ string, persist func([]panel.UserTraffic) error) ([]panel.UserTraffic, error) {
	return f.counts.Collect(func(string) int { return 7 }, persist)
}

func TestControllerRetriesSavedTrafficWithoutNewUsageAfterRestart(t *testing.T) {
	status, requests := http.StatusServiceUnavailable, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var payload map[int][]int64
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		want := int64(100)
		if requests == 2 {
			want = 125
		}
		if len(payload[7]) != 2 || payload[7][0] != want {
			t.Errorf("wrong reported usage: %+v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"data":true}`))
	}))
	defer server.Close()
	client, err := panel.New(&conf.ApiConfig{APIHost: server.URL, NodeType: "vmess", NodeID: 1})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	makeController := func() (*Controller, *trafficTestCore) {
		q, err := trafficqueue.Open(dir, "test-node")
		if err != nil {
			t.Fatal(err)
		}
		fake := &trafficTestCore{counts: counter.NewTrafficCounter()}
		return &Controller{server: fake, apiClient: client, Options: &conf.Options{}, tag: "test", trafficQueue: q,
			limiter: &limiter.Limiter{UserOnlineIP: new(sync.Map)}}, fake
	}
	c, fake := makeController()
	fake.counts.Tx("uuid", 100)
	if err := c.reportUserTrafficTask(); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || fake.counts.GetUpCount("uuid") != 0 {
		t.Fatal("failed report not stored")
	}
	// New usage after the last push must also survive graceful shutdown.
	fake.counts.Tx("uuid", 25)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if !fake.deleted {
		t.Fatal("node was not closed")
	}
	c, _ = makeController()
	status = http.StatusOK
	if err := c.reportUserTrafficTask(); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatal("stored traffic not retried without new traffic")
	}
	q, err := trafficqueue.Open(dir, "test-node")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Report(0, func([]panel.UserTraffic) error { t.Fatal("accepted traffic still pending"); return nil }); err != nil {
		t.Fatal(err)
	}
}
