package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/ahmedhodiani/gomontage/project"
	"github.com/spf13/cobra"
)

func renderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "render <project.gomontage.json>",
		Short: "Render a project file",
		Long: `Loads a project file (see docs/project-format.md), checks that it can be
rendered, and renders it with FFmpeg.

The output preset and file come from the project's "output" section; --preset
and -o override them (-o is relative to the current directory). Progress and
warnings go to stderr; on success the output file path is printed to stdout.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			out, _ := cmd.Flags().GetString("output")
			preset, _ := cmd.Flags().GetString("preset")

			p, warnings, err := project.Load(args[0])
			printWarnings(cmd.ErrOrStderr(), warnings)
			if err != nil {
				return problemsError("project is invalid", err)
			}
			tl, err := project.Build(p)
			if err != nil {
				return problemsError("project cannot be rendered", err)
			}
			profile, path, err := project.RenderTarget(p, preset, out)
			if err != nil {
				return problemsError("project cannot be rendered", err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			if err := tl.ExportContext(ctx, profile, path, nil); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
	cmd.Flags().StringP("output", "o", "", "output file (overrides output.path)")
	cmd.Flags().String("preset", "", "output preset (overrides output.preset): "+fmt.Sprint(project.Presets()))
	return cmd
}

func printWarnings(w io.Writer, warnings []project.Warning) {
	for _, wr := range warnings {
		fmt.Fprintf(w, "warning: %s\n", wr)
	}
}

// problemsError formats a *project.ValidationError as one indented line per
// problem under a heading; other errors are returned unchanged.
func problemsError(heading string, err error) error {
	var ve *project.ValidationError
	if !errors.As(err, &ve) {
		return err
	}
	msg := heading + ":"
	for _, p := range ve.Problems {
		msg += "\n  " + p.String()
	}
	return errors.New(msg)
}
