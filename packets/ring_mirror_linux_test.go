//go:build linux

package packets

import (
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestRingCapturesMirroredFrames simulates a switch mirror port: frames
// addressed to a foreign MAC arrive on an interface without an IP address.
// It needs root and iproute2, so it only runs when BWM_NETNS_TESTS=1.
func TestRingCapturesMirroredFrames(t *testing.T) {
	if os.Getenv("BWM_NETNS_TESTS") != "1" || os.Geteuid() != 0 {
		t.Skip("set BWM_NETNS_TESTS=1 and run as root")
	}
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("iproute2 not installed")
	}
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("ip", args...).CombinedOutput(); err != nil {
			t.Fatalf("ip %v: %v\n%s", args, err, out)
		}
	}
	run("link", "add", "bwmspan0", "type", "veth", "peer", "name", "bwmsw0")
	t.Cleanup(func() { exec.Command("ip", "link", "del", "bwmspan0").Run() })
	run("link", "set", "bwmspan0", "up")
	run("link", "set", "bwmsw0", "up")

	ring, err := NewRing("bwmspan0", true)
	if err != nil {
		t.Fatal(err)
	}
	defer ring.Close()

	sw, err := net.InterfaceByName("bwmsw0")
	if err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	sa := &unix.SockaddrLinklayer{Ifindex: sw.Index, Halen: 6}

	foreign := []byte{0x02, 0xaa, 0xbb, 0xcc, 0xdd, 0xee}
	srcMAC := []byte{0x02, 0x11, 0x22, 0x33, 0x44, 0x55}
	ip := make([]byte, 40)
	ip[0], ip[3], ip[9] = 0x45, 40, 6
	copy(ip[12:16], []byte{192, 0, 2, 10})
	copy(ip[16:20], []byte{198, 51, 100, 7})

	frame := func(vlan bool) []byte {
		f := append(append([]byte{}, foreign...), srcMAC...)
		if vlan {
			f = append(f, 0x81, 0x00, 0x00, 0x0a)
		}
		f = append(f, 0x08, 0x00)
		return append(f, ip...)
	}
	const perKind = 20
	for i := 0; i < perKind; i++ {
		for _, vlan := range []bool{false, true} {
			if err := unix.Sendto(fd, frame(vlan), 0, sa); err != nil {
				t.Fatal(err)
			}
		}
	}

	got := 0
	deadline := time.Now().Add(3 * time.Second)
	for got < 2*perKind && time.Now().Before(deadline) {
		_, err := ring.ReadBlock(func(pkt []byte, wireLen uint32) {
			p := ParseIPPacket(pkt, true)
			if p.Version == 4 && p.SrcIP.Equal(net.IPv4(192, 0, 2, 10)) {
				got++
			}
		}, 100)
		if err != nil {
			t.Fatal(err)
		}
	}
	if got != 2*perKind {
		t.Fatalf("captured %d mirrored IPv4 packets, want %d (untagged + 802.1Q)", got, 2*perKind)
	}
	st, err := ring.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if st.Packets < 2*perKind || st.Drops != 0 {
		t.Fatalf("stats = %+v, want >= %d packets and no drops", st, 2*perKind)
	}
}
