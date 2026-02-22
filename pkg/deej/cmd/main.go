package main

import (
	"flag"
	"fmt"

	"github.com/omriharel/deej/pkg/deej"
)

var (
	gitCommit  string
	versionTag string
	buildType  string

	verbose   bool
	logFilter string
	cliMode   bool
)

func init() {
	flag.BoolVar(&verbose, "verbose", false, "show verbose logs (useful for debugging)")
	flag.BoolVar(&verbose, "v", false, "shorthand for --verbose")
	flag.StringVar(&logFilter, "log-filter", "", "filter logs by component (e.g., 'audio-meter', 'serial', 'process-monitor')")
	flag.StringVar(&logFilter, "f", "", "shorthand for --log-filter")
	flag.BoolVar(&cliMode, "cli", false, "run in CLI mode (no tray icon, exits on Ctrl+C)")
	flag.Parse()
}

func main() {
	// Create logger with optional filtering
	logger, err := deej.NewLoggerWithFilter(buildType, logFilter)
	if err != nil {
		panic(fmt.Sprintf("Failed to create logger: %v", err))
	}

	named := logger.Named("main")
	named.Debug("Created logger")

	named.Infow("Version info",
		"gitCommit", gitCommit,
		"versionTag", versionTag,
		"buildType", buildType)

	if verbose {
		named.Debug("Verbose flag provided, all log messages will be shown")
	}

	if logFilter != "" {
		named.Infow("Log filter active", "filter", logFilter)
	}

	// Create the deej instance
	d, err := deej.NewDeej(logger, verbose)
	if err != nil {
		named.Fatalw("Failed to create deej object", "error", err)
	}

	if cliMode {
		d.SetCLIMode(true)
	}

	// Set version info for tray display if provided by build process
	if buildType != "" && (versionTag != "" || gitCommit != "") {
		identifier := gitCommit
		if versionTag != "" {
			identifier = versionTag
		}
		d.SetVersion(fmt.Sprintf("Version %s-%s", buildType, identifier))
	}

	// Start deej
	if err = d.Initialize(); err != nil {
		named.Fatalw("Failed to initialize deej", "error", err)
	}
}
