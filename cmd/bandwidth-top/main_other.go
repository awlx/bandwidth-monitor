//go:build !linux

package main

import "fmt"

func main() {
	fmt.Println("bandwidth-top is supported on Linux only")
}
