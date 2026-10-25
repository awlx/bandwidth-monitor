package talkers

import (
	"strconv"
	"testing"
)

func TestRateSlotHostsAreCapped(t *testing.T) {
	current := rankingBucket(nil)
	slot := &rateSlot{hosts: make(map[string]*hostAccum)}
	for i := 0; i < maxHostsPerBucket+500; i++ {
		accountEndpoint(nil, "h"+strconv.Itoa(i), false, current, slot)
	}
	if len(slot.hosts) != maxHostsPerBucket {
		t.Fatalf("rate slot holds %d hosts, want cap %d", len(slot.hosts), maxHostsPerBucket)
	}
	if _, rate := accountEndpoint(nil, "h0", false, current, slot); rate == nil {
		t.Fatal("existing host lost its rate accumulator")
	}
}

func TestPairsCapCountsPairsNotLocalHosts(t *testing.T) {
	tr := &Tracker{}
	current := rankingBucket(nil)
	// 100 local hosts x 500 remotes would be 50k pairs without a real cap.
	for l := 0; l < 100; l++ {
		for r := 0; r < maxRemotesPerLocalHost; r++ {
			tr.recordPair(current, "l"+strconv.Itoa(l), "r"+strconv.Itoa(r), 1, 1, 0)
		}
	}
	total := 0
	for _, remotes := range current.pairs {
		total += len(remotes)
	}
	if total != maxPairsPerBucket || current.pairCount != total {
		t.Fatalf("stored %d pairs (pairCount %d), want %d", total, current.pairCount, maxPairsPerBucket)
	}
	before := current.pairs["l0"]["r0"].bytes
	tr.recordPair(current, "l0", "r0", 5, 5, 0)
	if current.pairs["l0"]["r0"].bytes != before+5 {
		t.Fatal("existing pair stopped accumulating at the cap")
	}
}
