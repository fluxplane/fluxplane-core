package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/fluxplane/agentruntime/internal/architecture"
)

func main() {
	format := flag.String("format", "text", "report format: text, json, dot, mermaid")
	dir := flag.String("dir", ".", "module directory")
	module := flag.String("module", architecture.DefaultModulePath, "module import path")
	outPath := flag.String("out", "", "write report to file instead of stdout")
	includeTests := flag.Bool("tests", false, "include test imports")
	failOnViolation := flag.Bool("fail", false, "exit non-zero when architecture violations exist")
	failOn := flag.String("fail-on", "", "comma-separated gates that fail: boundary, test-boundary, side-effects, unknown, all")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	packages, err := architecture.LoadGoList(ctx, *dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	report := architecture.Analyze(packages, architecture.Config{
		ModulePath:   *module,
		IncludeTests: *includeTests,
	})

	var output bytes.Buffer
	switch *format {
	case "text":
		output.WriteString(architecture.RenderText(report))
	case "json":
		encoder := json.NewEncoder(&output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	case "dot":
		output.WriteString(architecture.RenderDOT(report))
	case "mermaid":
		output.WriteString(architecture.RenderMermaid(report))
	default:
		fmt.Fprintf(os.Stderr, "unknown format %q\n", *format)
		os.Exit(2)
	}

	if err := writeOutput(*outPath, output.Bytes()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	failCategories := *failOn
	if *failOnViolation && failCategories == "" {
		failCategories = "boundary"
	}
	if architecture.HasFailures(report, failCategories) {
		os.Exit(1)
	}
}

func writeOutput(path string, data []byte) error {
	var out io.Writer = os.Stdout
	var file *os.File
	if path != "" {
		created, err := os.Create(path)
		if err != nil {
			return err
		}
		file = created
		out = file
	}
	if _, err := out.Write(data); err != nil {
		if file != nil {
			_ = file.Close()
		}
		return err
	}
	if file != nil {
		return file.Close()
	}
	return nil
}
