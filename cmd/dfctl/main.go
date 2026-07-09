// Command dfctl is the headless client for the Sextant JSON-API (/api/v1).
// It is grown alongside the API: each API capability lands with its dfctl
// verb. For now it only reports itself.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "dfctl: Sextant CLI - no commands implemented yet (API lands in phase 2)")
	os.Exit(2)
}
