package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{500 * time.Millisecond, "0.5s"},
		{2 * time.Second, "2.0s"},
		{90 * time.Second, "1m 30.0s"},
		{3661 * time.Second, "1h 1m 1.0s"},
	}

	for _, tt := range tests {
		got := formatDuration(tt.d)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{500, "500 B"},
		{1024, "1.00 KB"},
		{1536, "1.50 KB"},
		{1048576, "1.00 MB"},
		{1073741824, "1.00 GB"},
	}

	for _, tt := range tests {
		got := formatBytes(tt.bytes)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestFormatBitrate(t *testing.T) {
	tests := []struct {
		bps  int64
		want string
	}{
		{128000, "128 kbps"},
		{1500000, "1.5 Mbps"},
		{5000000, "5.0 Mbps"},
	}

	for _, tt := range tests {
		got := formatBitrate(tt.bps)
		if got != tt.want {
			t.Errorf("formatBitrate(%d) = %q, want %q", tt.bps, got, tt.want)
		}
	}
}

func TestCommandsRegistered(t *testing.T) {
	// Verify that all command constructors don't panic and return valid commands.
	commands := []struct {
		name string
		fn   func() *cobra.Command
	}{
		{"init", initCmd},
		{"run", runCmd},
		{"probe", probeCmd},
		{"validate", validateCmd},
		{"render", renderCmd},
		{"docs", docsCmd},
	}

	for _, tc := range commands {
		cmd := tc.fn()
		if cmd == nil {
			t.Errorf("%s command returned nil", tc.name)
			continue
		}
		if cmd.Use == "" {
			t.Errorf("%s command has empty Use field", tc.name)
		}
		if cmd.Short == "" {
			t.Errorf("%s command has empty Short field", tc.name)
		}
		if cmd.RunE == nil {
			t.Errorf("%s command has nil RunE", tc.name)
		}
	}
}

func TestDocsCommandDefaultOutput(t *testing.T) {
	cmd := docsCmd()
	output, err := cmd.Flags().GetString("output")
	if err != nil {
		t.Fatalf("could not get output flag: %v", err)
	}
	if output != "docs" {
		t.Errorf("expected default output to be %q, got %q", "docs", output)
	}
}

func TestInitCommandRequiresArg(t *testing.T) {
	cmd := initCmd()
	// cobra.ExactArgs(1) should reject 0 args
	err := cmd.Args(cmd, []string{})
	if err == nil {
		t.Error("expected init command to reject zero arguments")
	}
}

func TestProbeCommandRequiresArg(t *testing.T) {
	cmd := probeCmd()
	err := cmd.Args(cmd, []string{})
	if err == nil {
		t.Error("expected probe command to reject zero arguments")
	}
}

func runValidate(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := validateCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestValidateProjectFile_LoaderError(t *testing.T) {
	out, err := runValidate(t, "../../schema/testdata/loader/transition-first-clip.json")
	if err == nil {
		t.Fatal("expected an error exit")
	}
	want := `error: /tracks/0/clips/0: clip "c1": transition_in on the first clip of track "t_main" (there is no previous clip)`
	if !strings.Contains(out, want) || !strings.Contains(out, ": 1 error, 0 warnings") {
		t.Errorf("output:\n%s", out)
	}
}

func TestValidateProjectFile_RenderErrorsAndNoDuplicateWarnings(t *testing.T) {
	out, err := runValidate(t, "../../examples/simple-trim-concat.json")
	if err == nil {
		t.Fatal("expected an error exit (media files are missing)")
	}
	if !strings.Contains(out, `error: /media/0: media "m_intro": file not found: media/intro.mp4`) {
		t.Errorf("missing render error:\n%s", out)
	}
	if strings.Contains(out, "warning:") || !strings.Contains(out, ": 3 errors, 0 warnings") {
		t.Errorf("missing-file problems must be reported once:\n%s", out)
	}
}

func TestValidateProjectFile_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.gomontage.json")
	data := `{"version": 1, "format": {"width": 320, "height": 180, "fps": 30}, "media": [],
	  "tracks": [{"id": "t1", "kind": "video", "clips": [{"id": "c1", "type": "color", "color": "#FF0000", "at_ms": 0, "duration_ms": 1000}]}],
	  "output": {"preset": "gif", "path": "out.gif"}}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runValidate(t, path)
	if err != nil || out != path+": valid, 0 warnings\n" {
		t.Errorf("err %v, output %q", err, out)
	}
}

func TestValidate_NoArgsKeepsYAMLBehavior(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := runValidate(t); err == nil || !strings.Contains(err.Error(), "gomontage.yaml") {
		t.Errorf("no-arg validate without gomontage.yaml: err = %v", err)
	}
}
