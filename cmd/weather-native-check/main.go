// weather-native-check is an offline development helper, never a runtime dependency.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/joega/a-weather-app/internal/nativebuild"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Native check:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: weather-native-check shaders|shader-header|translate-qt|hardening|symbols|inputs")
	}
	switch args[0] {
	case "shaders":
		flags := flag.NewFlagSet("shaders", flag.ContinueOnError)
		root := flags.String("root", ".", "checkout root")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("unexpected arguments")
		}
		if err := nativebuild.Shaders(*root); err != nil {
			return err
		}
	case "shader-header", "translate-qt":
		if len(args) != 3 {
			return errors.New("SOURCE OUTPUT required")
		}
		source, err := filepath.Abs(args[1])
		if err != nil {
			return err
		}
		output, err := filepath.Abs(args[2])
		if err != nil {
			return err
		}
		if args[0] == "shader-header" {
			err = nativebuild.ShaderHeader(source, output)
		} else {
			err = nativebuild.TranslateQtFile(source, output)
		}
		if err != nil {
			return err
		}
	case "hardening":
		if len(args) < 2 {
			return errors.New("ELF paths required")
		}
		for _, file := range args[1:] {
			if err := nativebuild.Hardening(file); err != nil {
				return fmt.Errorf("%s: %w", file, err)
			}
		}
	case "symbols":
		if len(args) < 2 || len(args) > 3 {
			return errors.New("PLUGIN [HOST] required")
		}
		host := "/usr/bin/Hyprland"
		if len(args) == 3 {
			host = args[2]
		}
		if err := nativebuild.Symbols(args[1], host); err != nil {
			return err
		}
	case "inputs":
		if len(args) != 2 {
			return errors.New("ATMOSPHERE_HOST required")
		}
		host, err := filepath.Abs(args[1])
		if err != nil {
			return err
		}
		if err = nativebuild.Inputs(host); err != nil {
			return err
		}
	default:
		return errors.New("unknown native check")
	}
	fmt.Println("PASS:", args[0])
	return nil
}
