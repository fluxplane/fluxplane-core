package launch

import (
	"testing"

	"github.com/spf13/pflag"
)

// TestBindLocalRuntimeFlagsRespectsPreSetDefaults regresses a bug where
// BindLocalRuntimeFlags hard-coded `false` and `""` as flag defaults instead
// of threading the caller-supplied opts values through. After flag parsing
// with no command-line args, those caller defaults must survive.
func TestBindLocalRuntimeFlagsRespectsPreSetDefaults(t *testing.T) {
	opts := LocalRuntimeFlags{
		Debug:            true,
		Yolo:             true,
		Dev:              true,
		AllowMaxToolRisk: "medium",
	}
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	BindLocalRuntimeFlags(flags, &opts, LocalRuntimeFlagHelp{})
	if err := flags.Parse(nil); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !opts.Debug {
		t.Errorf("Debug = false, want true (caller default must survive)")
	}
	if !opts.Yolo {
		t.Errorf("Yolo = false, want true (caller default must survive)")
	}
	if !opts.Dev {
		t.Errorf("Dev = false, want true (caller default must survive)")
	}
	if opts.AllowMaxToolRisk != "medium" {
		t.Errorf("AllowMaxToolRisk = %q, want medium (caller default must survive)", opts.AllowMaxToolRisk)
	}
}

// TestBindLocalRuntimeFlagsHonorsCommandLineOverride confirms the command-line
// flag still wins over the caller default - the fix must not break override.
func TestBindLocalRuntimeFlagsHonorsCommandLineOverride(t *testing.T) {
	opts := LocalRuntimeFlags{Debug: true, AllowMaxToolRisk: "medium"}
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	BindLocalRuntimeFlags(flags, &opts, LocalRuntimeFlagHelp{})
	if err := flags.Parse([]string{"--debug=false", "--allow-max-tool-risk=low"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if opts.Debug {
		t.Errorf("Debug = true, want false (cli override must win)")
	}
	if opts.AllowMaxToolRisk != "low" {
		t.Errorf("AllowMaxToolRisk = %q, want low (cli override must win)", opts.AllowMaxToolRisk)
	}
}
