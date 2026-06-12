package main

import (
	"os"

	"github.com/J-1000/memlog/internal/cli"
)

func main() {
	if err := cli.NewRoot().Execute(); err != nil {
		os.Exit(cli.ExitCode(err))
	}
}
