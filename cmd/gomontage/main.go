package main

import (
	"fmt"
	"os"

	gomontage "github.com/ahmedhodiani/gomontage"
	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "gomontage",
		Short: "Gomontage — programmatic video editing framework for Go",
		Long: `Gomontage is a Go framework for editing videos with code.

Write high-level Go code to trim clips, arrange them on tracks,
layer audio, apply effects, and export professional video.

Get started:
  gomontage init my-project    Create a new project
  gomontage run                Run the project's main.go
  gomontage probe video.mp4    Inspect a media file
  gomontage render p.gomontage.json    Render a project file
  gomontage validate p.gomontage.json  Check a project file
  gomontage docs               Generate API documentation`,
		Version: gomontage.Version,
		// Errors are printed once, by main, to stderr.
		SilenceErrors: true,
	}

	rootCmd.AddCommand(
		initCmd(),
		runCmd(),
		probeCmd(),
		validateCmd(),
		renderCmd(),
		docsCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
