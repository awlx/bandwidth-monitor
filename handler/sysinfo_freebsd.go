//go:build freebsd

package handler

import (
	"encoding/binary"
	"time"

	"golang.org/x/sys/unix"
)

// readUptime returns the system uptime derived from kern.boottime.
func readUptime() float64 {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return 0
	}
	return time.Since(time.Unix(tv.Unix())).Seconds()
}

// readLoadAvg decodes vm.loadavg (struct loadavg: fixpt_t ldavg[3]; long fscale).
func readLoadAvg() [3]float64 {
	b, err := unix.SysctlRaw("vm.loadavg")
	if err != nil || len(b) < 24 {
		return [3]float64{}
	}
	scale := float64(binary.NativeEndian.Uint64(b[16:24]))
	if scale == 0 {
		return [3]float64{}
	}
	var la [3]float64
	for i := range la {
		la[i] = float64(binary.NativeEndian.Uint32(b[i*4:])) / scale
	}
	return la
}

// readProcessCount returns the run-queue length (vm.vmtotal t_rq) and the
// number of distinct processes in kern.proc.all (which lists one entry per
// thread).
func readProcessCount() (running, total int) {
	// struct vmtotal: nine uint64 counters followed by int16 t_rq.
	if b, err := unix.SysctlRaw("vm.vmtotal"); err == nil && len(b) >= 74 {
		running = int(int16(binary.NativeEndian.Uint16(b[72:74])))
	}
	b, err := unix.SysctlRaw("kern.proc.all")
	if err != nil {
		return running, 0
	}
	const pidOffset = 72 // struct kinfo_proc.ki_pid
	pids := make(map[uint32]struct{})
	for len(b) >= pidOffset+4 {
		size := int(binary.NativeEndian.Uint32(b[0:4])) // ki_structsize
		if size < pidOffset+4 || size > len(b) {
			break
		}
		pids[binary.NativeEndian.Uint32(b[pidOffset:])] = struct{}{}
		b = b[size:]
	}
	return running, len(pids)
}
