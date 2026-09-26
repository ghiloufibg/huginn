// Command huginn is a read-only terminal UI for Kubernetes pod logs.
package main

import (
	"os"

	"github.com/ghiloufibg/huginn/internal/bootstrap"
)

func main() {
	os.Exit(bootstrap.Main(os.Args[1:], os.Stdout, os.Stderr))
}
