package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"odoo_method_analyzer/internal/analyzer"
	"odoo_method_analyzer/internal/model"
	"odoo_method_analyzer/internal/project"
	"odoo_method_analyzer/internal/report"
	"odoo_method_analyzer/internal/ui"
)

func main() {
	cfg, err := parseFlags()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cfg.Root = root

	printer := ui.New(cfg.Verbose)
	printer.Header()
	printer.Info("Using current directory as project root: " + cfg.Root)

	paths, err := project.Discover(cfg.Root)
	if err != nil {
		printer.Error(err.Error())
		os.Exit(1)
	}
	if cfg.Verbose {
		for _, path := range paths.SourcePaths {
			printer.Debug("Source path: " + path)
		}
		for _, path := range paths.OdooPaths {
			printer.Debug("Odoo path: " + path)
		}
	}

	result, err := analyzer.Run(context.Background(), cfg, paths, printer)
	if err != nil {
		printer.Error(err.Error())
		os.Exit(1)
	}
	if result.TotalMethods == 0 {
		printer.Warn("No methods found to analyze")
		return
	}

	report.Print(printer, cfg.Root, result)
	if cfg.OutputFile != "" {
		if err := report.WriteJSON(cfg.OutputFile, result); err != nil {
			printer.Error(err.Error())
			os.Exit(1)
		}
		printer.Success("Results saved to " + cfg.OutputFile)
	}
}

func parseFlags() (model.Config, error) {
	var cfg model.Config
	var includeTestsShort bool
	var includeTestsLong bool
	var verboseShort bool
	var verboseLong bool
	var outputShort string
	var outputLong string
	var helpShort bool
	var helpLong bool

	flag.BoolVar(&includeTestsShort, "t", false, "include test files in analysis")
	flag.BoolVar(&includeTestsLong, "include-tests", false, "include test files in analysis")
	flag.BoolVar(&verboseShort, "v", false, "enable verbose output")
	flag.BoolVar(&verboseLong, "verbose", false, "enable verbose output")
	flag.StringVar(&outputShort, "o", "", "write detailed JSON output to file")
	flag.StringVar(&outputLong, "output", "", "write detailed JSON output to file")
	flag.BoolVar(&helpShort, "h", false, "show help")
	flag.BoolVar(&helpLong, "help", false, "show help")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [OPTIONS]\n", os.Args[0])
		fmt.Fprintln(flag.CommandLine.Output())
		fmt.Fprintln(flag.CommandLine.Output(), "Analyze Odoo customer method usage across the project.")
		fmt.Fprintln(flag.CommandLine.Output())
		flag.PrintDefaults()
	}
	flag.Parse()

	if helpShort || helpLong {
		flag.Usage()
		os.Exit(0)
	}
	if flag.NArg() > 0 {
		return cfg, fmt.Errorf("unexpected arguments: %v", flag.Args())
	}

	cfg.IncludeTests = includeTestsShort || includeTestsLong
	cfg.Verbose = verboseShort || verboseLong
	if outputLong != "" {
		cfg.OutputFile = outputLong
	} else {
		cfg.OutputFile = outputShort
	}
	return cfg, nil
}
