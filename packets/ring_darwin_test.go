//go:build darwin

package packets

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"
)

func TestParseBPFRecordsAlignsAndPreservesWireLength(t *testing.T) {
	ipv4 := make([]byte, 20)
	ipv4[0] = 0x45
	binary.BigEndian.PutUint16(ipv4[2:4], uint16(len(ipv4)))
	ipv6 := make([]byte, 40)
	ipv6[0] = 0x60

	ethernet := append(make([]byte, 12), 0x08, 0x00)
	ethernet = append(ethernet, ipv4...)
	vlan := append(make([]byte, 12), 0x81, 0x00, 0x00, 0x64, 0x86, 0xdd)
	vlan = append(vlan, ipv6...)
	data := append(bpfTestRecord(ethernet, 1514), bpfTestRecord(vlan, 1518)...)

	var versions []byte
	var wireLengths []uint32
	count, err := parseBPFRecords(data, unix.DLT_EN10MB, func(packet []byte, wireLength uint32) {
		versions = append(versions, packet[0]>>4)
		wireLengths = append(wireLengths, wireLength)
	})
	if err != nil || count != 2 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if versions[0] != 4 || versions[1] != 6 ||
		wireLengths[0] != 1514 || wireLengths[1] != 1518 {
		t.Fatalf("versions=%v wire lengths=%v", versions, wireLengths)
	}
}

func TestParseBPFRecordsHandlesLoopbackAndRawIP(t *testing.T) {
	ipv4 := make([]byte, 20)
	ipv4[0] = 0x45
	loopback := make([]byte, 4)
	binary.NativeEndian.PutUint32(loopback, unix.AF_INET)
	loopback = append(loopback, ipv4...)

	for _, test := range []struct {
		name     string
		linkType int
		frame    []byte
	}{
		{name: "null", linkType: unix.DLT_NULL, frame: loopback},
		{name: "raw", linkType: unix.DLT_RAW, frame: ipv4},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			count, err := parseBPFRecords(
				bpfTestRecord(test.frame, uint32(len(test.frame))),
				test.linkType,
				func(packet []byte, _ uint32) {
					called = len(packet) == 20 && packet[0]>>4 == 4
				},
			)
			if err != nil || count != 1 || !called {
				t.Fatalf("count=%d called=%v err=%v", count, called, err)
			}
		})
	}
}

func TestParseBPFRecordsRejectsMalformedInput(t *testing.T) {
	valid := bpfTestRecord(make([]byte, 20), 20)
	tests := map[string][]byte{
		"truncated header": make([]byte, minBPFHeaderLen-1),
		"short header length": func() []byte {
			data := append([]byte(nil), valid...)
			binary.NativeEndian.PutUint16(data[16:18], minBPFHeaderLen-1)
			return data
		}(),
		"captured exceeds wire": func() []byte {
			data := append([]byte(nil), valid...)
			binary.NativeEndian.PutUint32(data[12:16], 1)
			return data
		}(),
		"captured exceeds buffer": func() []byte {
			data := append([]byte(nil), valid...)
			binary.NativeEndian.PutUint32(data[8:12], 4096)
			binary.NativeEndian.PutUint32(data[12:16], 4096)
			return data
		}(),
		"truncated alignment": valid[:len(valid)-1],
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseBPFRecords(data, unix.DLT_RAW, func([]byte, uint32) {}); err == nil {
				t.Fatal("accepted malformed BPF data")
			}
		})
	}
}

func TestDarwinEthernetFilterAcceptsVLANAndDropsNonIP(t *testing.T) {
	vm, err := bpf.NewVM(AnyIpFilter)
	if err != nil {
		t.Fatal(err)
	}
	frames := []struct {
		name string
		data []byte
		want bool
	}{
		{name: "IPv4", data: ethernetTestFrame(0x0800), want: true},
		{name: "IPv6", data: ethernetTestFrame(0x86dd), want: true},
		{name: "802.1Q", data: vlanTestFrame(0x8100, 0x0800), want: true},
		{name: "QinQ", data: qinqTestFrame(0x88a8, 0x8100, 0x86dd), want: true},
		{name: "ARP", data: ethernetTestFrame(0x0806), want: false},
	}
	for _, test := range frames {
		t.Run(test.name, func(t *testing.T) {
			accepted, runErr := vm.Run(test.data)
			if runErr != nil || (accepted > 0) != test.want {
				t.Fatalf("accepted=%d want=%v err=%v", accepted, test.want, runErr)
			}
		})
	}
}

func TestOpenBPFDeviceSkipsBusyDevicesAndExplainsPermissions(t *testing.T) {
	var paths []string
	fd, err := openBPFDevice(func(path string, _ int, _ uint32) (int, error) {
		paths = append(paths, path)
		switch path {
		case "/dev/bpf":
			return -1, unix.ENOENT
		case "/dev/bpf0":
			return -1, unix.EBUSY
		default:
			return 42, nil
		}
	})
	if err != nil || fd != 42 || len(paths) != 3 {
		t.Fatalf("fd=%d paths=%v err=%v", fd, paths, err)
	}

	_, err = openBPFDevice(func(string, int, uint32) (int, error) {
		return -1, unix.EACCES
	})
	if err == nil || !strings.Contains(err.Error(), "root") ||
		!strings.Contains(err.Error(), "/dev/bpf") {
		t.Fatalf("unexpected permission error: %v", err)
	}
}

func TestDarwinRingClosesDescriptorAfterConfigurationFailure(t *testing.T) {
	closeCalls := 0
	system := bpfSystem{
		open:  func(string, int, uint32) (int, error) { return 42, nil },
		close: func(int) error { closeCalls++; return nil },
		ioctlSetInt: func(_ int, request uint, _ int) error {
			if request == unix.BIOCIMMEDIATE {
				return errors.New("configuration failed")
			}
			return nil
		},
		ioctlPtr: func(int, uint, unsafe.Pointer) error { return nil },
	}
	if _, err := newDarwinRing("test0", false, system); err == nil {
		t.Fatal("configuration failure was ignored")
	}
	if closeCalls != 1 {
		t.Fatalf("descriptor closed %d times", closeCalls)
	}
}

func TestDarwinRingCloseIsIdempotent(t *testing.T) {
	closeCalls := 0
	system := bpfSystem{
		open:        func(string, int, uint32) (int, error) { return 42, nil },
		close:       func(int) error { closeCalls++; return nil },
		ioctlSetInt: func(int, uint, int) error { return nil },
		ioctlPtr:    func(int, uint, unsafe.Pointer) error { return nil },
		ioctlNoArg:  func(int, uint) error { return nil },
		ioctlGetInt: func(_ int, request uint) (int, error) {
			if request == unix.BIOCGBLEN {
				return 4096, nil
			}
			return unix.DLT_RAW, nil
		},
	}
	ring, err := newDarwinRing("test0", false, system)
	if err != nil {
		t.Fatal(err)
	}
	ring.Close()
	ring.Close()
	if closeCalls != 1 || ring.buffer != nil {
		t.Fatalf("close calls=%d buffer=%v", closeCalls, ring.buffer)
	}
}

func FuzzParseBPFRecordsNeverPanics(f *testing.F) {
	ipv4 := make([]byte, 20)
	ipv4[0] = 0x45
	f.Add(bpfTestRecord(ipv4, 20))
	f.Add([]byte{})
	f.Add([]byte{1, 2, 3, 4})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = parseBPFRecords(data, unix.DLT_RAW, func([]byte, uint32) {})
	})
}

func bpfTestRecord(payload []byte, wireLength uint32) []byte {
	recordLength := unix.SizeofBpfHdr + len(payload)
	record := make([]byte, bpfWordAlign(recordLength))
	binary.NativeEndian.PutUint32(record[8:12], uint32(len(payload)))
	binary.NativeEndian.PutUint32(record[12:16], wireLength)
	binary.NativeEndian.PutUint16(record[16:18], unix.SizeofBpfHdr)
	copy(record[unix.SizeofBpfHdr:], payload)
	return record
}

func ethernetTestFrame(etherType uint16) []byte {
	frame := make([]byte, 64)
	binary.BigEndian.PutUint16(frame[12:14], etherType)
	return frame
}

func vlanTestFrame(outer, inner uint16) []byte {
	frame := make([]byte, 64)
	binary.BigEndian.PutUint16(frame[12:14], outer)
	binary.BigEndian.PutUint16(frame[16:18], inner)
	return frame
}

func qinqTestFrame(outer, middle, inner uint16) []byte {
	frame := make([]byte, 64)
	binary.BigEndian.PutUint16(frame[12:14], outer)
	binary.BigEndian.PutUint16(frame[16:18], middle)
	binary.BigEndian.PutUint16(frame[20:22], inner)
	return frame
}
