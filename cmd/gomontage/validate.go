package main

import (
	"errors"
	"fmt"

	"github.com/ahmedhodiani/gomontage/project"
	"github.com/spf13/cobra"
)

func validateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [project.gomontage.json]",
		Short: "Validate a project file, or the current project structure",
		Long: `With a project file argument: loads the file (JSON Schema and loader rules),
then runs the render-time checks, and prints one line per problem with its
JSON path:

  error: /tracks/0/clips/2: clip "c3": media "m9" does not exist
  warning: /media/1: media "m_music": file not found: media/music.mp3

The last line is a summary. Exits with status 1 if there are errors.

Without an argument: checks that the current directory is a valid Gomontage
project (gomontage.yaml exists and is valid).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				cmd.SilenceUsage = true
				return validateProjectFile(cmd, args[0])
			}
			cfg, err := project.LoadConfig("gomontage.yaml")
			if err != nil {
				return fmt.Errorf("validation failed: %w", err)
			}

			fmt.Printf("Project:    %s\n", cfg.Name)
			fmt.Printf("Version:    %s\n", cfg.Version)
			fmt.Printf("Resolution: %dx%d\n", cfg.Resolution.Width, cfg.Resolution.Height)
			fmt.Printf("FPS:        %.0f\n", cfg.FPS)
			fmt.Printf("Output:     %s (%s)\n", cfg.Output.Directory, cfg.Output.Format)
			fmt.Println()
			fmt.Println("Project is valid.")

			return nil
		},
	}
}

// validateProjectFile prints every load problem, warning and render problem
// of a project file to stdout, one per line, then a summary.
func validateProjectFile(cmd *cobra.Command, path string) error {
	out := cmd.OutOrStdout()
	var problems []project.Problem

	collect := func(err error) error {
		var ve *project.ValidationError
		if errors.As(err, &ve) {
			problems = append(problems, ve.Problems...)
			return nil
		}
		return err
	}

	p, warnings, err := project.Load(path)
	if err != nil {
		if err := collect(err); err != nil {
			return err // unreadable file etc.
		}
	} else {
		// Render-time checks: missing media, fonts, known gaps.
		if _, err := project.Build(p); err != nil {
			if err := collect(err); err != nil {
				return err
			}
		}
		if p.Output != nil {
			if _, _, err := project.RenderTarget(p, "", ""); err != nil {
				if err := collect(err); err != nil {
					problems = append(problems, project.Problem{Path: "/output", Message: err.Error()})
				}
			}
		}
	}

	// A missing media file is a load warning and a render error; report it
	// once, as the error.
	isError := map[string]bool{}
	for _, pr := range problems {
		isError[pr.String()] = true
		fmt.Fprintf(out, "error: %s\n", pr)
	}
	kept := warnings[:0:0]
	for _, w := range warnings {
		if !isError[w.String()] {
			kept = append(kept, w)
			fmt.Fprintf(out, "warning: %s\n", w)
		}
	}
	warnings = kept
	if len(problems) > 0 {
		fmt.Fprintf(out, "%s: %s, %s\n", path, plural(len(problems), "error"), plural(len(warnings), "warning"))
		return fmt.Errorf("%s is not valid", path)
	}
	fmt.Fprintf(out, "%s: valid, %s\n", path, plural(len(warnings), "warning"))
	return nil
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
