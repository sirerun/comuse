//go:build !darwin || !cgo

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "fixture acceptance requires macOS with cgo")
	os.Exit(69)
}
