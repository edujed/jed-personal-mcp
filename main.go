package main

import (
	"fmt"
	"os"

	"github.com/edujed/jed-personal-mcp/internal/config"
	"github.com/edujed/jed-personal-mcp/internal/fbtools"
	"github.com/mark3labs/mcp-go/server"
)

const (
	serverName    = "jed-personal-mcp"
	serverVersion = "1.0.0"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Create the MCP server
	s := server.NewMCPServer(
		serverName,
		serverVersion,
		server.WithLogging(),
	)

	// Register Firebird tools
	h := fbtools.NewHandler(cfg)
	fbtools.Register(s, h)

	// Start the server on Stdio
	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start MCP server: %v\n", err)
		os.Exit(1)
	}
}
