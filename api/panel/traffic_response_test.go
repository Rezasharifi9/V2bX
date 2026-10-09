package panel

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/InazumaV/V2bX/conf"
)

func TestTrafficReportRequiresPanelAcknowledgement(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  int
		body    string
		success bool
	}{
		{"accepted", 200, `{"data":true}`, true},
		{"panel unavailable", 503, `{"data":true}`, false},
		{"rejected", 200, `{"data":false}`, false},
		{"login page", 200, `<html>Login</html>`, false},
		{"empty response", 204, ``, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := New(&conf.ApiConfig{APIHost: server.URL, NodeType: "vmess", NodeID: 1})
			if err != nil {
				t.Fatal(err)
			}
			err = client.ReportUserTraffic([]UserTraffic{{UID: 7, Upload: 100}})
			if (err == nil) != test.success {
				t.Fatalf("unexpected acknowledgement result: %v", err)
			}
		})
	}
}
