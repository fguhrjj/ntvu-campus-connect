//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "校园网自动连接需要 Windows 10 或更高版本。")
	os.Exit(1)
}
