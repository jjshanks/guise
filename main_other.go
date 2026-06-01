//go:build !windows

package main

import (
	"fmt"
	"os"
)

// guise is a Windows-only application (§3, §8). This stub lets the module
// build on other platforms (so the pure config/router/chrome logic stays
// testable) while making the platform requirement explicit.
func main() {
	fmt.Fprintln(os.Stderr, "guise runs on Windows only")
	os.Exit(1)
}
