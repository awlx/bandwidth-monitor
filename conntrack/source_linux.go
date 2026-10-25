//go:build linux

package conntrack

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"syscall"

	ct "github.com/ti-mo/conntrack"
)

const (
	procConntrackMax = "/proc/sys/net/netfilter/nf_conntrack_max"
	procConntrackCnt = "/proc/sys/net/netfilter/nf_conntrack_count"
)

// Default and maximum netlink socket buffer sizes.
const (
	defaultSockBuf = 2 * 1024 * 1024  // 2 MB — enough for ~10k flows
	maxSockBuf     = 16 * 1024 * 1024 // 16 MB — for very large tables
)

// stateSource dumps the netfilter conntrack table over netlink.
type stateSource struct {
	sockBufSize int      // netlink socket receive buffer size
	errCount    int      // consecutive dump errors (for log rate-limiting)
	conn        *ct.Conn // persistent netlink connection
}

func newStateSource() *stateSource {
	return &stateSource{sockBufSize: defaultSockBuf}
}

// probe opens a netlink connection and performs a test dump to verify
// conntrack is available.
func (t *stateSource) probe() bool {
	// Probe: try to open a conntrack netlink connection
	c, err := ct.Dial(nil)
	if err != nil {
		log.Printf("conntrack: netlink dial failed: %v", err)
		log.Println("conntrack: NAT tracking disabled — ensure nf_conntrack and nf_conntrack_netlink modules are loaded and process has CAP_NET_ADMIN")
		log.Println("conntrack: on OpenWrt: apk add kmod-nf-conntrack-netlink  (or opkg install kmod-nf-conntrack-netlink)")
		return false
	}

	// Probe dump to verify the conntrack netlink subsystem actually works.
	// The socket can open even without kmod-nf-conntrack-netlink, but dump
	// will fail with EINVAL.
	if err := c.SetReadBuffer(t.sockBufSize); err != nil {
		log.Printf("conntrack: SetReadBuffer: %v", err)
	}
	_, err = c.Dump(nil)
	c.Close()
	if err != nil {
		log.Printf("conntrack: probe dump failed: %v", err)
		log.Println("conntrack: NAT tracking disabled — the nf_conntrack_netlink kernel module is likely not loaded")
		log.Println("conntrack: on OpenWrt: apk add kmod-nf-conntrack-netlink  (or opkg install kmod-nf-conntrack-netlink)")
		return false
	}

	log.Println("conntrack: netlink connection established")
	return true
}

func (t *stateSource) close() {
	if t.conn != nil {
		t.conn.Close()
		t.conn = nil
	}
}

// dump passes every flow to fn and returns the table's entry count and limit.
func (t *stateSource) dump(fn func(Entry)) (count, max int, ok bool) {
	// Reuse persistent connection; re-dial if needed.
	if t.conn == nil {
		c, err := ct.Dial(nil)
		if err != nil {
			if t.errCount == 0 || t.errCount%60 == 0 {
				log.Printf("conntrack: netlink dial: %v", err)
			}
			t.errCount++
			return 0, 0, false
		}
		if err := c.SetReadBuffer(t.sockBufSize); err != nil {
			log.Printf("conntrack: SetReadBuffer(%d): %v", t.sockBufSize, err)
		}
		t.conn = c
	}

	flows, err := t.conn.Dump(nil)
	if err != nil {
		// Close the broken connection so we re-dial next time.
		t.conn.Close()
		t.conn = nil

		// On EINVAL / buffer-related failures, try increasing the buffer size
		if t.sockBufSize < maxSockBuf {
			t.sockBufSize *= 2
			if t.sockBufSize > maxSockBuf {
				t.sockBufSize = maxSockBuf
			}
			log.Printf("conntrack: dump failed (%v), increasing buffer to %d MB", err, t.sockBufSize/(1024*1024))
		} else if t.errCount == 0 || t.errCount%60 == 0 {
			log.Printf("conntrack: dump: %v (buffer=%d MB)", err, t.sockBufSize/(1024*1024))
		}
		t.errCount++
		return 0, 0, false
	}
	t.errCount = 0

	max = readIntFile(procConntrackMax)
	count = readIntFile(procConntrackCnt)
	if count == 0 {
		count = len(flows)
	}
	for i := range flows {
		fn(convertFlow(&flows[i]))
	}
	return count, max, true
}

// tcpStateName maps conntrack TCP state values to human-readable names.
// These match the kernel's enum tcp_conntrack (include/uapi/linux/netfilter/nf_conntrack_tcp.h).
var tcpStateName = map[uint8]string{
	0: "NONE",
	1: "SYN_SENT",
	2: "SYN_RECV",
	3: "ESTABLISHED",
	4: "FIN_WAIT",
	5: "CLOSE_WAIT",
	6: "LAST_ACK",
	7: "TIME_WAIT",
	8: "CLOSE",
	9: "SYN_SENT2",
}

// convertFlow transforms a ti-mo/conntrack Flow into our Entry type.
func convertFlow(f *ct.Flow) Entry {
	var e Entry

	// Family: netip.Addr uses Is4()/Is6()
	if f.TupleOrig.IP.SourceAddress.Is4() {
		e.Family = "ipv4"
	} else {
		e.Family = "ipv6"
	}

	// Protocol
	proto := f.TupleOrig.Proto.Protocol
	if name, ok := protoName[proto]; ok {
		e.Protocol = name
	} else {
		e.Protocol = fmt.Sprintf("PROTO_%d", proto)
	}

	// TCP state
	if proto == syscall.IPPROTO_TCP {
		if name, ok := tcpStateName[f.ProtoInfo.TCP.State]; ok {
			e.State = name
		} else {
			e.State = fmt.Sprintf("STATE_%d", f.ProtoInfo.TCP.State)
		}
	}

	// TTL / timeout
	e.TTL = int(f.Timeout)

	// Original tuple
	e.OrigSrc = f.TupleOrig.IP.SourceAddress.String()
	e.OrigDst = f.TupleOrig.IP.DestinationAddress.String()
	if f.TupleOrig.Proto.SourcePort != 0 {
		e.OrigSPort = strconv.Itoa(int(f.TupleOrig.Proto.SourcePort))
	}
	if f.TupleOrig.Proto.DestinationPort != 0 {
		e.OrigDPort = strconv.Itoa(int(f.TupleOrig.Proto.DestinationPort))
	}

	// Reply tuple
	e.ReplSrc = f.TupleReply.IP.SourceAddress.String()
	e.ReplDst = f.TupleReply.IP.DestinationAddress.String()
	if f.TupleReply.Proto.SourcePort != 0 {
		e.ReplSPort = strconv.Itoa(int(f.TupleReply.Proto.SourcePort))
	}
	if f.TupleReply.Proto.DestinationPort != 0 {
		e.ReplDPort = strconv.Itoa(int(f.TupleReply.Proto.DestinationPort))
	}

	// Counters (original + reply)
	e.Bytes = f.CountersOrig.Bytes + f.CountersReply.Bytes
	e.Packets = f.CountersOrig.Packets + f.CountersReply.Packets

	// Status flags
	e.Assured = f.Status.SeenReply()

	// NAT detection: compare original vs reply tuples
	e.NATType = detectNAT(e)

	return e
}

func readIntFile(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	v, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return v
}
