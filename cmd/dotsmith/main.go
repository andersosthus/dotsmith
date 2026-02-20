// Package main is the entry point for the dotsmith CLI.
package main

import (
	"fmt"
	"os"

	"github.com/andersosthus/dotsmith/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
