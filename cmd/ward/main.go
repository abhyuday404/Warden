package main

import (
	"fmt"
	"os"

	"github.com/abhyuday404/Warden/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
