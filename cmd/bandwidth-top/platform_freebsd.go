//go:build freebsd

package main

import "golang.org/x/sys/unix"

func captureUsageText() string {
	return "Live BPF traffic viewer; FreeBSD requires root or read access to /dev/bpf."
}

func capturePrivilegeHint() string {
	return "run as root or grant access to /dev/bpf"
}

func terminalFDIsTTY(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	return err == nil
}

func terminalFDSize(fd int) (int, int, error) {
	size, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, err
	}
	return int(size.Col), int(size.Row), nil
}
