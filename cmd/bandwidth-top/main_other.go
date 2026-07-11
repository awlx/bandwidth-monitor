//go:build !linux && !darwin

package main

import "fmt"

func main() {
	fmt.Println("bandwidth-top is supported on Linux and macOS only")
}
