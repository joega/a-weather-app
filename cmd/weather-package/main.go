// weather-package is a development-only manifest creation and verification tool.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/joega/a-weather-app/internal/buildmeta"
	"os"
	"path/filepath"
)

func run(args []string) error {
	if len(args) == 0 || (args[0] != "create" && args[0] != "verify") {
		return errors.New("usage: weather-package create|verify --root PATH [--source PATH]")
	}
	flags := flag.NewFlagSet("weather-package", flag.ContinueOnError)
	root := flags.String("root", "", "runtime package directory")
	source := flags.String("source", "", "source checkout directory")
	if e := flags.Parse(args[1:]); e != nil {
		return e
	}
	if *root == "" || flags.NArg() != 0 || (args[0] == "create" && *source == "") {
		return errors.New("runtime root and create source are required")
	}
	r, e := filepath.Abs(*root)
	if e != nil {
		return e
	}
	s := ""
	if *source != "" {
		s, e = filepath.Abs(*source)
		if e != nil {
			return e
		}
	}
	if args[0] == "create" {
		e = buildmeta.Create(context.Background(), r, s)
	} else {
		e = buildmeta.Verify(r, s)
	}
	if e != nil {
		return e
	}
	suffix := ""
	if s != "" {
		suffix = " and source inventory"
	}
	fmt.Println("PASS: Go/Qt artifact checksums" + suffix)
	return nil
}
func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "weather-package:", e)
		os.Exit(1)
	}
}
