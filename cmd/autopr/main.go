package main

import (
	"os"

	"autopr/cmd/autopr/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
