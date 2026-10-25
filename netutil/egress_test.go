package netutil

import (
	"net"
	"strings"
	"testing"
)

func cidr(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

const (
	wanA = 2 // default route in main
	wanB = 3 // default route only in a policy table
	lan  = 4
)

// Fake netlink state of a policy-routed two-WAN host.
var (
	testRoutes = []policyRoute{
		{Table: 254, LinkIndex: wanA},
		{Table: 100, LinkIndex: wanB},
		{Table: 200, LinkIndex: wanA}, // unrelated table, no rule
	}
	mainRules = []policyRule{
		{Priority: 0, Table: 255},
		{Priority: 32766, Table: 254},
		{Priority: 32767, Table: 253},
	}
	testAddrs = []policyAddr{
		{IP: net.ParseIP("192.0.2.10"), LinkIndex: lan},
		{IP: net.ParseIP("198.51.100.7"), LinkIndex: wanB},
		{IP: net.ParseIP("203.0.113.5"), LinkIndex: wanA},
	}
)

func rulesWith(extra ...policyRule) []policyRule {
	return append(append([]policyRule(nil), mainRules...), extra...)
}

func TestResolveEgress(t *testing.T) {
	cases := []struct {
		name  string
		iface string
		idx   int
		rules []policyRule
		want  Egress
		unr   string // substring of Unroutable
	}{
		{name: "main table default", iface: "wanA", idx: wanA, rules: rulesWith(),
			want: Egress{Iface: "wanA"}},
		{name: "fwmark rule", iface: "wanB", idx: wanB,
			rules: rulesWith(policyRule{Priority: 100, Table: 100, Mark: 0x6}),
			want:  Egress{Iface: "wanB", Mark: 0x6}},
		{name: "from rule with address on the WAN", iface: "wanB", idx: wanB,
			rules: rulesWith(policyRule{Priority: 100, Table: 100, Src: cidr("198.51.100.7/32")}),
			want:  Egress{Iface: "wanB", Src: net.ParseIP("198.51.100.7")}},
		{name: "from prefix with address on another interface", iface: "wanB", idx: wanB,
			rules: rulesWith(policyRule{Priority: 100, Table: 100, Src: cidr("192.0.2.0/24")}),
			want:  Egress{Iface: "wanB", Src: net.ParseIP("192.0.2.10")}},
		{name: "from prefix without local address is skipped", iface: "wanB", idx: wanB,
			rules: rulesWith(policyRule{Priority: 100, Table: 100, Src: cidr("10.99.0.0/16")}),
			unr:   "no ip rule selects"},
		{name: "oif rule matches bound socket", iface: "wanB", idx: wanB,
			rules: rulesWith(policyRule{Priority: 100, Table: 100, OifName: "wanB"}),
			want:  Egress{Iface: "wanB"}},
		{name: "lowest priority wins", iface: "wanB", idx: wanB,
			rules: rulesWith(
				policyRule{Priority: 200, Table: 100, Src: cidr("198.51.100.7/32")},
				policyRule{Priority: 150, Table: 100, Mark: 0x9},
			),
			want: Egress{Iface: "wanB", Mark: 0x9}},
		{name: "plain rule preferred over earlier source rule", iface: "wanA", idx: wanA,
			rules: rulesWith(policyRule{Priority: 90, Table: 254, Src: cidr("192.0.2.10/32")}),
			want:  Egress{Iface: "wanA"}},
		{name: "plain rule preferred over earlier mark rule", iface: "wanB", idx: wanB,
			rules: rulesWith(
				policyRule{Priority: 90, Table: 100, Mark: 0x3},
				policyRule{Priority: 95, Table: 100, OifName: "wanB"},
			),
			want: Egress{Iface: "wanB"}},
		{name: "destination-only rule does not select host traffic", iface: "wanB", idx: wanB,
			rules: rulesWith(policyRule{Priority: 50, Table: 100, Dst: cidr("198.18.0.1/32")}),
			unr:   "no ip rule selects"},
		{name: "unusable selectors are ignored", iface: "wanB", idx: wanB,
			rules: rulesWith(
				policyRule{Priority: 10, Table: 100, IifName: "lan0"},
				policyRule{Priority: 11, Table: 100, OifName: "other"},
				policyRule{Priority: 12, Table: 100, Invert: true},
				policyRule{Priority: 13, Table: 100, Other: true},
				policyRule{Priority: 14, Table: 101, Mark: 0x7}, // wrong table
			),
			unr: "no ip rule selects"},
		{name: "iif lo matches local traffic", iface: "wanB", idx: wanB,
			rules: rulesWith(policyRule{Priority: 10, Table: 100, IifName: "lo", Mark: 0x2}),
			want:  Egress{Iface: "wanB", Mark: 0x2}},
		{name: "no default route", iface: "lan0", idx: lan, rules: rulesWith(),
			unr: "no default route"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveEgress(c.iface, c.idx, testRoutes, c.rules, testAddrs)
			if c.unr != "" {
				if !strings.Contains(got.Unroutable, c.unr) || got.Mark != 0 || got.Src != nil {
					t.Fatalf("got %+v, want unroutable containing %q", got, c.unr)
				}
				return
			}
			if !got.Equal(c.want) {
				t.Fatalf("got %+v (%s), want %+v", got, got, c.want)
			}
		})
	}
}

func TestEgressEqual(t *testing.T) {
	a := Egress{Iface: "x", Mark: 1, Src: net.ParseIP("192.0.2.1")}
	if !a.Equal(Egress{Iface: "x", Mark: 1, Src: net.ParseIP("192.0.2.1").To4()}) {
		t.Error("equal egress differs")
	}
	if a.Equal(Egress{Iface: "x", Mark: 2, Src: a.Src}) || a.Equal(Egress{Iface: "x", Mark: 1}) {
		t.Error("different egress equal")
	}
}
