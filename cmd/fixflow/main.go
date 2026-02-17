package main

import (
	"os"

	"fixflow/cmd/fixflow/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
