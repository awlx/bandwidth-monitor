//go:build !linux && !darwin && !freebsd

package main

import "fmt"

func main() {
	fmt.Println("bandwidth-top is supported on Linux, macOS and FreeBSD only")
}
