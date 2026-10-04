//go:build !darwin || !cgo

package main

import (
	"errors"
	"fmt"
	"os"
)

var errUnsupported = errors.New("native Swift bridge loading is supported only on macOS with cgo enabled")

func main() {
	fmt.Fprintln(os.Stderr, "seamprobe:", errUnsupported)
	os.Exit(2)
}
