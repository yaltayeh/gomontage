// Command gomontage-mcp is the GoMontage MCP server. It speaks the Model
// Context Protocol over stdio, so an agent such as Claude Code can create,
// edit, preview and render GoMontage projects:
//
//	claude mcp add gomontage -- gomontage-mcp
//
// stdout carries only protocol messages. Logs go to stderr (-log-level
// sets how much); anything else in the process that writes to os.Stdout
// is redirected to stderr. See docs/mcp.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ahmedhodiani/gomontage"
	"github.com/ahmedhodiani/gomontage/mcpserver"
)

func main() {
	level := flag.String("log-level", "warn", "stderr log level: debug, info, warn or error")
	version := flag.Bool("version", false, "print the version to stderr and exit")
	flag.Parse()
	if *version {
		fmt.Fprintln(os.Stderr, "gomontage-mcp", gomontage.Version)
		return
	}

	// Keep the real stdout for the protocol; send every other write to
	// os.Stdout to stderr, so a stray print can't corrupt the stream.
	protocol := os.Stdout
	os.Stdout = os.Stderr

	var lv slog.Level
	if err := lv.UnmarshalText([]byte(*level)); err != nil {
		fmt.Fprintf(os.Stderr, "gomontage-mcp: bad -log-level %q\n", *level)
		os.Exit(2)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lv}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s := mcpserver.New(mcpserver.Options{Logger: log, Version: gomontage.Version})
	t := &mcp.IOTransport{Reader: os.Stdin, Writer: protocol}
	if err := s.Run(ctx, t); err != nil && ctx.Err() == nil {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
