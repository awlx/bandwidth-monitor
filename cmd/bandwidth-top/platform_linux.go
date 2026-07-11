//go:build linux

package main

import "golang.org/x/sys/unix"

func captureUsageText() string {
	return "Live AF_PACKET traffic viewer; requires root or CAP_NET_RAW."
}

func capturePrivilegeHint() string {
	return "run as root or grant CAP_NET_RAW"
}

func terminalFDIsTTY(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	return err == nil
}

func terminalFDSize(fd int) (int, int, error) {
	size, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, err
	}
	return int(size.Col), int(size.Row), nil
}
