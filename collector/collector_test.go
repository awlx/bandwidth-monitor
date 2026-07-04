package collector

import (
	"testing"
)

func testCollector() *Collector {
	return &Collector{
		history: map[string][]HistoryPoint{
			"eth0": {
				{Timestamp: 1000, RxRate: 1, TxRate: 2},
				{Timestamp: 2000, RxRate: 3, TxRate: 4},
				{Timestamp: 3000, RxRate: 5, TxRate: 6},
			},
			"ppp0": {
				{Timestamp: 1500, RxRate: 7, TxRate: 8},
				{Timestamp: 2500, RxRate: 9, TxRate: 10},
			},
		},
	}
}

// Zero values mean no filtering: GetHistoryFiltered("", 0) must match GetHistory exactly.
func TestGetHistoryFilteredNoFilters(t *testing.T) {
	c := testCollector()
	got := c.GetHistoryFiltered("", 0)
	if len(got) != 2 || len(got["eth0"]) != 3 || len(got["ppp0"]) != 2 {
		t.Fatalf("unfiltered result wrong shape: %+v", got)
	}
	// Must be a deep copy, not a view of the live slice.
	got["eth0"][0].RxRate = 999
	if c.history["eth0"][0].RxRate == 999 {
		t.Error("filtered result aliases the collector's internal history")
	}
}

func TestGetHistoryFilteredByIface(t *testing.T) {
	got := testCollector().GetHistoryFiltered("eth0", 0)
	if len(got) != 1 || len(got["eth0"]) != 3 {
		t.Fatalf("iface filter: want only eth0 with 3 points, got %+v", got)
	}
}

// A point whose timestamp equals since must be included (>= boundary).
func TestGetHistoryFilteredSince(t *testing.T) {
	got := testCollector().GetHistoryFiltered("", 2000)
	if len(got["eth0"]) != 2 || got["eth0"][0].Timestamp != 2000 {
		t.Errorf("since=2000 eth0: want points [2000 3000], got %+v", got["eth0"])
	}
	if len(got["ppp0"]) != 1 || got["ppp0"][0].Timestamp != 2500 {
		t.Errorf("since=2000 ppp0: want point [2500], got %+v", got["ppp0"])
	}
}

func TestGetHistoryFilteredIfaceAndSince(t *testing.T) {
	got := testCollector().GetHistoryFiltered("ppp0", 2000)
	if len(got) != 1 || len(got["ppp0"]) != 1 || got["ppp0"][0].Timestamp != 2500 {
		t.Errorf("combined filter: want only ppp0 [2500], got %+v", got)
	}
}

// A since past the newest point yields empty (but present) slices, not missing keys.
func TestGetHistoryFilteredSincePastEnd(t *testing.T) {
	got := testCollector().GetHistoryFiltered("eth0", 9999)
	if pts, ok := got["eth0"]; !ok || len(pts) != 0 {
		t.Errorf("since past end: want empty eth0 slice, got %+v", got)
	}
}

// The allowedIfaces whitelist must still be honoured under filtering.
func TestGetHistoryFilteredRespectsAllowedIfaces(t *testing.T) {
	c := testCollector()
	c.allowedIfaces = map[string]bool{"ppp0": true}
	got := c.GetHistoryFiltered("", 0)
	if _, ok := got["eth0"]; ok {
		t.Errorf("whitelist ignored: eth0 present in %+v", got)
	}
	if got := c.GetHistoryFiltered("eth0", 0); len(got) != 0 {
		t.Errorf("explicit iface must not bypass whitelist, got %+v", got)
	}
}
