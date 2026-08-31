package main

import (
	"os"

	"github.com/QuantaStream/qstream-migrate/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}
