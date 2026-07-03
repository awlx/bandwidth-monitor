package handler

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"bandwidth-monitor/collector"
)

func historyRequest(t *testing.T, c *collector.Collector, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/interfaces/history"+query, nil)
	w := httptest.NewRecorder()
	InterfaceHistory(c)(w, req)
	return w
}

func decodeHistory(t *testing.T, w *httptest.ResponseRecorder) map[string][]collector.HistoryPoint {
	t.Helper()
	var out map[string][]collector.HistoryPoint
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

// No params must return every interface's full history — the pre-params contract old
// clients (web UI, older apps) rely on.
func TestInterfaceHistoryNoParams(t *testing.T) {
	c := collector.NewForTest(map[string][]collector.HistoryPoint{
		"eth0": {{Timestamp: 1000}, {Timestamp: 2000}},
		"ppp0": {{Timestamp: 1500}},
	})
	w := historyRequest(t, c, "")
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	got := decodeHistory(t, w)
	if len(got) != 2 || len(got["eth0"]) != 2 || len(got["ppp0"]) != 1 {
		t.Errorf("full response wrong shape: %+v", got)
	}
}

func TestInterfaceHistoryIfaceAndSince(t *testing.T) {
	c := collector.NewForTest(map[string][]collector.HistoryPoint{
		"eth0": {{Timestamp: 1000}, {Timestamp: 2000}, {Timestamp: 3000}},
		"ppp0": {{Timestamp: 1500}},
	})
	w := historyRequest(t, c, "?iface=eth0&since=2000")
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	got := decodeHistory(t, w)
	if len(got) != 1 || len(got["eth0"]) != 2 || got["eth0"][0].Timestamp != 2000 {
		t.Errorf("filtered response: want only eth0 [2000 3000], got %+v", got)
	}
}

func TestInterfaceHistoryBadSince(t *testing.T) {
	c := collector.NewForTest(nil)
	for _, q := range []string{"?since=notanumber", "?since=-5", "?since=1.5"} {
		if w := historyRequest(t, c, q); w.Code != 400 {
			t.Errorf("%s: status = %d, want 400", q, w.Code)
		}
	}
}

func TestInterfaceHistoryIfaceTooLong(t *testing.T) {
	c := collector.NewForTest(nil)
	if w := historyRequest(t, c, "?iface="+strings.Repeat("x", 65)); w.Code != 400 {
		t.Errorf("oversized iface: status = %d, want 400", w.Code)
	}
}
