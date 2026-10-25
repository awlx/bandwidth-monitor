//go:build !freebsd

package handler

import (
	"os"
	"strconv"
	"strings"
)

// readUptime reads the system uptime from /proc/uptime in seconds.
func readUptime() float64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	parts := strings.Fields(string(data))
	if len(parts) < 1 {
		return 0
	}
	v, _ := strconv.ParseFloat(parts[0], 64)
	return v
}

// readLoadAvg reads the 1/5/15 minute load averages from /proc/loadavg.
func readLoadAvg() [3]float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return [3]float64{}
	}
	parts := strings.Fields(string(data))
	if len(parts) < 3 {
		return [3]float64{}
	}
	var la [3]float64
	la[0], _ = strconv.ParseFloat(parts[0], 64)
	la[1], _ = strconv.ParseFloat(parts[1], 64)
	la[2], _ = strconv.ParseFloat(parts[2], 64)
	return la
}

// readProcessCount reads the running/total process count from /proc/loadavg.
func readProcessCount() (running, total int) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0
	}
	parts := strings.Fields(string(data))
	if len(parts) < 4 {
		return 0, 0
	}
	// Format: "running/total"
	rt := strings.SplitN(parts[3], "/", 2)
	if len(rt) == 2 {
		running, _ = strconv.Atoi(rt[0])
		total, _ = strconv.Atoi(rt[1])
	}
	return
}
