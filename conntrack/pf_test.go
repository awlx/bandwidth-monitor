package conntrack

import (
	"strings"
	"testing"
)

// Captured from FreeBSD 14 `pfctl -s states -v` with a LAN client behind
// "nat on $ext_if" and an inbound "rdr" port forward; addresses replaced by
// documentation ranges.
const pfStatesSample = `all icmp 198.51.100.1:8 <- 192.168.50.2:53509       0:0
   age 00:00:03, expires in 00:00:09, 2:2 pkts, 168:168 bytes, rule 0
all icmp 203.0.113.15:33046 (192.168.50.2:53509) -> 198.51.100.1:8       0:0
   age 00:00:03, expires in 00:00:09, 2:2 pkts, 168:168 bytes, rule 0
all udp 198.51.100.3:53 <- 192.168.50.2:37769       SINGLE:MULTIPLE
   age 00:00:01, expires in 00:00:30, 1:1 pkts, 61:161 bytes, rule 0
all udp 203.0.113.15:65171 (192.168.50.2:37769) -> 198.51.100.3:53       MULTIPLE:SINGLE
   age 00:00:01, expires in 00:00:30, 1:1 pkts, 61:161 bytes, rule 0
all tcp 198.51.100.80:80 <- 192.168.50.2:40888       FIN_WAIT_2:FIN_WAIT_2
   [1680186625 + 4278190336]  [2158357291 + 4278190336]
   age 00:00:00, expires in 00:01:30, 7:8 pkts, 405:4261 bytes, rule 0
all tcp 203.0.113.15:58469 (192.168.50.2:40888) -> 198.51.100.80:80       FIN_WAIT_2:FIN_WAIT_2
   [2158357291 + 4278190336]  [1680186625 + 4278190336]
   age 00:00:00, expires in 00:01:30, 7:8 pkts, 405:4261 bytes, rule 0
all tcp 192.168.50.2:80 (203.0.113.15:8081) <- 198.51.100.7:38361       ESTABLISHED:ESTABLISHED
   [3960059173 + 3221291264] wscale 6  [3853319155 + 3221291264] wscale 6
   age 00:00:45, expires in 23:59:15, 3:3 pkts, 269:188 bytes, rule 0
all tcp 198.51.100.7:38361 -> 192.168.50.2:80       ESTABLISHED:ESTABLISHED
   [3853319155 + 3221291264] wscale 6  [3960059173 + 3221291264] wscale 6
   age 00:00:45, expires in 23:59:15, 3:3 pkts, 269:188 bytes, rule 0
vtnet0 tcp 2001:db8::1[22] <- 2001:db8::2[50000]       ESTABLISHED:ESTABLISHED
   age 00:10:00, expires in 24:00:00, 10:0 pkts, 1000:0 bytes, rule 3
`

func TestParsePFStates(t *testing.T) {
	entries, raw := parsePFStates(strings.NewReader(pfStatesSample))
	if raw != 9 {
		t.Fatalf("raw states = %d, want 9", raw)
	}
	if len(entries) != 5 {
		t.Fatalf("merged entries = %d, want 5: %+v", len(entries), entries)
	}

	icmp := entries[0]
	if icmp.Protocol != "ICMP" || icmp.NATType != "snat" || icmp.OrigSPort != "" || icmp.ReplDst != "203.0.113.15" {
		t.Errorf("icmp entry = %+v", icmp)
	}

	udp := entries[1]
	want := Entry{
		Family: "ipv4", Protocol: "UDP", TTL: 30,
		OrigSrc: "192.168.50.2", OrigSPort: "37769", OrigDst: "198.51.100.3", OrigDPort: "53",
		ReplSrc: "198.51.100.3", ReplSPort: "53", ReplDst: "203.0.113.15", ReplDPort: "65171",
		NATType: "snat", Assured: true, Bytes: 222, Packets: 2,
	}
	if udp != want {
		t.Errorf("udp entry\n got %+v\nwant %+v", udp, want)
	}

	tcp := entries[2]
	if tcp.State != "TIME_WAIT" || tcp.NATType != "snat" || tcp.Bytes != 4666 || tcp.TTL != 90 {
		t.Errorf("tcp snat entry = %+v", tcp)
	}

	rdr := entries[3]
	if rdr.NATType != "dnat" || rdr.OrigDst != "203.0.113.15" || rdr.OrigDPort != "8081" ||
		rdr.ReplSrc != "192.168.50.2" || rdr.ReplSPort != "80" || rdr.State != "ESTABLISHED" ||
		rdr.TTL != 86355 {
		t.Errorf("rdr entry = %+v", rdr)
	}

	v6 := entries[4]
	if v6.Family != "ipv6" || v6.OrigSrc != "2001:db8::2" || v6.OrigSPort != "50000" ||
		v6.OrigDst != "2001:db8::1" || v6.OrigDPort != "22" || v6.NATType != "none" || v6.Assured {
		t.Errorf("ipv6 entry = %+v", v6)
	}
}

func TestParsePFStatesIgnoresGarbage(t *testing.T) {
	in := "No ALTQ support in kernel\nall tcp bogus -> 1.2.3.4:5 X:Y\n   age 00:00:01, expires in 00:00:01, 1:1 pkts, 1:1 bytes\n"
	entries, raw := parsePFStates(strings.NewReader(in))
	if len(entries) != 0 || raw != 0 {
		t.Fatalf("got %d entries / %d raw from garbage", len(entries), raw)
	}
}

func TestPFTCPState(t *testing.T) {
	tests := []struct{ a, b, want string }{
		{"SYN_SENT", "CLOSED", "SYN_SENT"},
		{"SYN_SENT", "SYN_RCVD", "SYN_RECV"},
		{"ESTABLISHED", "ESTABLISHED", "ESTABLISHED"},
		{"FIN_WAIT_1", "ESTABLISHED", "FIN_WAIT"},
		{"ESTABLISHED", "CLOSE_WAIT", "CLOSE_WAIT"},
		{"FIN_WAIT_2", "LAST_ACK", "LAST_ACK"},
		{"FIN_WAIT_2", "ESTABLISHED", "FIN_WAIT"},
		{"TIME_WAIT", "TIME_WAIT", "TIME_WAIT"},
		{"WEIRD", "ESTABLISHED", "WEIRD"},
	}
	for _, tt := range tests {
		if got := pfTCPState(tt.a, tt.b); got != tt.want {
			t.Errorf("pfTCPState(%s, %s) = %s, want %s", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestParsePFStateLimit(t *testing.T) {
	in := "states        hard limit   100000\nsrc-nodes     hard limit    10000\n"
	if got := parsePFStateLimit(strings.NewReader(in)); got != 100000 {
		t.Fatalf("limit = %d", got)
	}
}
