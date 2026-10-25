// Package icmputil provides ICMP echo helpers shared by the probers.
package icmputil

import "time"

// RTTMs returns milliseconds elapsed since start.
func RTTMs(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000.0
}
