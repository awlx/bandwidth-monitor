package conntrack

import (
	"bufio"
	"io"
	"net"
	"strconv"
	"strings"
)

// pf TCP peer states as printed by pfctl (enum in <netinet/tcp_fsm.h>).
var pfTCPStateRank = map[string]int{
	"CLOSED":      0,
	"LISTEN":      1,
	"SYN_SENT":    2,
	"SYN_RCVD":    3,
	"ESTABLISHED": 4,
	"CLOSE_WAIT":  5,
	"FIN_WAIT_1":  6,
	"CLOSING":     7,
	"LAST_ACK":    8,
	"FIN_WAIT_2":  9,
	"TIME_WAIT":   10,
}

// pfState is one parsed `pfctl -ss -v` record before deduplication.
type pfState struct {
	entry   Entry
	srcPort string // raw ports kept for ICMP id disambiguation
	dstPort string
}

// parsePFStates parses `pfctl -s states -v` output into conntrack entries.
// pf records a forwarded connection twice (once on the inbound and once on
// the outbound interface); those are merged into one entry, preferring the
// translated side so NAT is reported like Linux conntrack does. It returns
// the merged entries and the raw number of pf states.
func parsePFStates(r io.Reader) ([]Entry, int) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)

	var states []pfState
	var cur *pfState
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			cur = nil
			if st, ok := parsePFStateLine(line); ok {
				states = append(states, st)
				cur = &states[len(states)-1]
			}
			continue
		}
		if cur != nil {
			parsePFDetailLine(strings.TrimSpace(line), &cur.entry)
		}
	}
	return mergePFStates(states), len(states)
}

// parsePFStateLine parses "<if> <proto> A [(A')] -> | <- B [(B')] S1:S2".
func parsePFStateLine(line string) (pfState, bool) {
	f := strings.Fields(line)
	if len(f) < 5 {
		return pfState{}, false
	}
	proto := strings.ToUpper(f[1])
	rest := f[2:]

	arrow := -1
	for i, tok := range rest {
		if tok == "->" || tok == "<-" {
			arrow = i
			break
		}
	}
	if arrow < 1 {
		return pfState{}, false
	}
	left, ok := parsePFSide(rest[:arrow])
	if !ok {
		return pfState{}, false
	}
	rightToks := rest[arrow+1:]
	if len(rightToks) < 2 {
		return pfState{}, false
	}
	stateTok := rightToks[len(rightToks)-1]
	right, ok := parsePFSide(rightToks[:len(rightToks)-1])
	if !ok {
		return pfState{}, false
	}

	// "->" states are outbound: the left side is the source. "<-" states are
	// inbound and printed destination first. In both cases the bare address
	// is the translated one and the parenthesised one the original.
	src, dst := left, right
	if rest[arrow] == "<-" {
		src, dst = right, left
	}

	var e Entry
	switch proto {
	case "ICMP", "TCP", "UDP", "GRE", "SCTP", "DCCP":
		e.Protocol = proto
	case "IPV6-ICMP", "ICMP6", "ICMPV6":
		e.Protocol = "ICMPv6"
	default:
		e.Protocol = proto
	}
	e.Family = "ipv4"
	if ip := net.ParseIP(src.origIP); ip != nil && ip.To4() == nil {
		e.Family = "ipv6"
	}
	e.OrigSrc, e.OrigSPort = src.origIP, src.origPort
	e.OrigDst, e.OrigDPort = dst.origIP, dst.origPort
	e.ReplSrc, e.ReplSPort = dst.ip, dst.port
	e.ReplDst, e.ReplDPort = src.ip, src.port

	peer := strings.SplitN(stateTok, ":", 2)
	if e.Protocol == "TCP" && len(peer) == 2 {
		e.State = pfTCPState(peer[0], peer[1])
	}

	st := pfState{entry: e, srcPort: src.origPort, dstPort: dst.origPort}
	if e.Protocol == "ICMP" || e.Protocol == "ICMPv6" {
		// pf prints ICMP ids/types in the port position; conntrack has none.
		st.entry.OrigSPort, st.entry.OrigDPort = "", ""
		st.entry.ReplSPort, st.entry.ReplDPort = "", ""
	}
	st.entry.NATType = detectNAT(st.entry)
	return st, true
}

type pfSide struct {
	ip, port         string
	origIP, origPort string
}

// parsePFSide parses "addr" or "addr (orig)" tokens of one side.
func parsePFSide(toks []string) (pfSide, bool) {
	if len(toks) == 0 || len(toks) > 2 {
		return pfSide{}, false
	}
	var s pfSide
	var ok bool
	if s.ip, s.port, ok = splitPFHost(toks[0]); !ok {
		return pfSide{}, false
	}
	s.origIP, s.origPort = s.ip, s.port
	if len(toks) == 2 {
		inner := strings.TrimSuffix(strings.TrimPrefix(toks[1], "("), ")")
		if s.origIP, s.origPort, ok = splitPFHost(inner); !ok {
			return pfSide{}, false
		}
	}
	return s, true
}

// splitPFHost splits pfctl host notation: "1.2.3.4:80", "2001:db8::1[443]",
// or a bare address.
func splitPFHost(tok string) (ip, port string, ok bool) {
	if i := strings.IndexByte(tok, '['); i > 0 && strings.HasSuffix(tok, "]") {
		ip, port = tok[:i], tok[i+1:len(tok)-1]
	} else if strings.Count(tok, ":") == 1 {
		i := strings.IndexByte(tok, ':')
		ip, port = tok[:i], tok[i+1:]
	} else {
		ip = tok
	}
	if net.ParseIP(ip) == nil {
		return "", "", false
	}
	if port == "0" {
		port = ""
	}
	return ip, port, true
}

// parsePFDetailLine extracts expiry and counters from the verbose line
// "age 00:01:02, expires in 00:00:30, 3:4 pkts, 100:200 bytes, rule 0".
func parsePFDetailLine(line string, e *Entry) {
	if !strings.HasPrefix(line, "age ") {
		return
	}
	for _, part := range strings.Split(line, ",") {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, "expires in "):
			e.TTL = parsePFDuration(strings.TrimPrefix(part, "expires in "))
		case strings.HasSuffix(part, " pkts"):
			e.Packets = sumPFPair(strings.TrimSuffix(part, " pkts"))
			e.Assured = pfPairBothNonZero(strings.TrimSuffix(part, " pkts"))
		case strings.HasSuffix(part, " bytes"):
			e.Bytes = sumPFPair(strings.TrimSuffix(part, " bytes"))
		}
	}
}

func parsePFDuration(s string) int {
	parts := strings.Split(strings.TrimSpace(s), ":")
	total := 0
	for _, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil {
			return 0
		}
		total = total*60 + v
	}
	return total
}

func splitPFPair(s string) (uint64, uint64) {
	a, b, _ := strings.Cut(strings.TrimSpace(s), ":")
	x, _ := strconv.ParseUint(a, 10, 64)
	y, _ := strconv.ParseUint(b, 10, 64)
	return x, y
}

func sumPFPair(s string) uint64 {
	x, y := splitPFPair(s)
	return x + y
}

func pfPairBothNonZero(s string) bool {
	x, y := splitPFPair(s)
	return x > 0 && y > 0
}

// pfTCPState maps pf's per-peer TCP states to a conntrack-style state name.
func pfTCPState(a, b string) string {
	ra, okA := pfTCPStateRank[a]
	rb, okB := pfTCPStateRank[b]
	if !okA || !okB {
		return a
	}
	lo, hi := min(ra, rb), max(ra, rb)
	est := pfTCPStateRank["ESTABLISHED"]
	switch {
	case hi < est:
		if hi == pfTCPStateRank["SYN_RCVD"] {
			return "SYN_RECV"
		}
		if hi == pfTCPStateRank["SYN_SENT"] {
			return "SYN_SENT"
		}
		return "NONE"
	case lo >= pfTCPStateRank["FIN_WAIT_2"]:
		return "TIME_WAIT"
	case hi == est:
		return "ESTABLISHED"
	}
	switch hi {
	case pfTCPStateRank["CLOSE_WAIT"]:
		return "CLOSE_WAIT"
	case pfTCPStateRank["LAST_ACK"]:
		return "LAST_ACK"
	case pfTCPStateRank["TIME_WAIT"], pfTCPStateRank["FIN_WAIT_2"]:
		if lo == pfTCPStateRank["LAST_ACK"] {
			return "LAST_ACK"
		}
		return "FIN_WAIT"
	default:
		return "FIN_WAIT"
	}
}

// mergePFStates folds the inbound/outbound state pair of forwarded flows.
// Both records share the original source and the post-rdr destination.
func mergePFStates(states []pfState) []Entry {
	type key struct {
		proto, src, sport, dst, dport string
	}
	idx := make(map[key]int, len(states))
	out := make([]Entry, 0, len(states))
	for _, st := range states {
		e := st.entry
		k := key{e.Protocol, e.OrigSrc, st.srcPort, e.ReplSrc, e.ReplSPort}
		if e.Protocol == "ICMP" || e.Protocol == "ICMPv6" {
			k.dport = st.dstPort
		}
		i, seen := idx[k]
		if !seen {
			idx[k] = len(out)
			out = append(out, e)
			continue
		}
		prev := &out[i]
		if prev.NATType == "none" && e.NATType != "none" {
			e.Bytes = max(e.Bytes, prev.Bytes)
			e.Packets = max(e.Packets, prev.Packets)
			e.TTL = max(e.TTL, prev.TTL)
			e.Assured = e.Assured || prev.Assured
			*prev = e
			continue
		}
		prev.Bytes = max(prev.Bytes, e.Bytes)
		prev.Packets = max(prev.Packets, e.Packets)
		prev.TTL = max(prev.TTL, e.TTL)
		prev.Assured = prev.Assured || e.Assured
	}
	return out
}

// parsePFStateLimit extracts the state hard limit from `pfctl -s memory`.
func parsePFStateLimit(r io.Reader) int {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 4 && f[0] == "states" && f[1] == "hard" && f[2] == "limit" {
			v, _ := strconv.Atoi(f[3])
			return v
		}
	}
	return 0
}
