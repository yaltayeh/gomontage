package main

import (
	"fmt"

	"github.com/ahmedhodiani/gomontage/project"
	"github.com/spf13/cobra"
)

func validateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [project.gomontage.json]",
		Short: "Validate a project file, or the current project structure",
		Long: `With a project file argument: checks the file and prints, with JSON paths,

  Project errors   the file is invalid (JSON Schema, loader rules)
  Render blockers  the file is valid but can't be rendered yet (missing
                   media, features the library doesn't support yet)
  Warnings         informational (stale probes, unused missing media, ...)

followed by a summary line. Exits with status 1 if there are project errors
or render blockers.

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

// validateProjectFile prints the check result of a project file in two
// separate sections — project errors (schema, loader rules) and render
// blockers (missing media or fonts) — plus warnings and a summary line.
// It returns an error (exit status 1) if either section is non-empty.
func validateProjectFile(cmd *cobra.Command, path string) error {
	out := cmd.OutOrStdout()
	r, _, err := project.CheckFile(path)
	if err != nil {
		return err
	}

	section := func(title string, problems []project.Problem) {
		if len(problems) == 0 {
			fmt.Fprintf(out, "%s: none\n", title)
			return
		}
		fmt.Fprintf(out, "%s (%d):\n", title, len(problems))
		for _, p := range problems {
			fmt.Fprintf(out, "  %s\n", p)
		}
	}
	section("Project errors", r.ProjectErrors)
	if r.RenderChecked {
		section("Render blockers", r.RenderBlockers)
	} else {
		fmt.Fprintln(out, "Render blockers: not checked (fix the project errors first)")
	}
	section("Warnings", r.Warnings)

	counts := fmt.Sprintf("%s, %s, %s", plural(len(r.ProjectErrors), "project error"),
		plural(len(r.RenderBlockers), "render blocker"), plural(len(r.Warnings), "warning"))
	switch {
	case len(r.ProjectErrors) > 0:
		fmt.Fprintf(out, "%s: invalid (%s)\n", path, counts)
		return fmt.Errorf("%s is not a valid project", path)
	case len(r.RenderBlockers) > 0:
		fmt.Fprintf(out, "%s: valid but cannot be rendered yet (%s)\n", path, counts)
		return fmt.Errorf("%s cannot be rendered", path)
	}
	fmt.Fprintf(out, "%s: OK (%s)\n", path, counts)
	return nil
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
