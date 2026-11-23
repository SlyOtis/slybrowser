package main

import (
	"os"

	"github.com/nimling/nimbody/cli"
	"github.com/slyotis/slybrowser/internal/slyb"
)

func main() {
	root := slyb.NewImportCommand()
	slyb.AddFlags(root)
	os.Exit(cli.Execute(root))
}
