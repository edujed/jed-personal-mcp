// Package config handles loading and validating the MCP server configuration.
package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// DatabaseConfig represents a configured database.
type DatabaseConfig struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path"`
}

// ServerConfig holds the Firebird server connection details and database list.
type ServerConfig struct {
	Host            string           `json:"host"`
	Port            int              `json:"port"`
	User            string           `json:"user"`
	Password        string           `json:"password"`
	AllowedPaths    []string         `json:"allowed_paths"`
	Databases       []DatabaseConfig `json:"databases"`
	DefaultDatabase string           `json:"default_database"`
	ISQLPath        string           `json:"isql_path,omitempty"`
}

// Config is the root structure of config.json.
type Config struct {
	Server ServerConfig `json:"server"`
}

// DSN builds the connection string for a specific database.
func (s *ServerConfig) DSN(dbPath string) string {
	return fmt.Sprintf("%s:%s@%s:%d/%s", s.User, s.Password, s.Host, s.Port, dbPath)
}

// ServerDSN builds a connection string to the server without a specific database.
func (s *ServerConfig) ServerDSN() string {
	return fmt.Sprintf("%s:%s@%s:%d", s.User, s.Password, s.Host, s.Port)
}

// Load reads and validates the configuration file.
// Lookup order: FIREBIRD_CONFIG env var, then ./config.json.
func Load() (*Config, error) {
	path := os.Getenv("FIREBIRD_CONFIG")
	if path == "" {
		path = "config.json"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file (%s): %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// validate checks that the configuration is complete and consistent.
func (c *Config) validate() error {
	if len(c.Server.Databases) == 0 {
		return fmt.Errorf("no databases configured")
	}

	if c.Server.DefaultDatabase == "" {
		c.Server.DefaultDatabase = c.Server.Databases[0].Name
	}

	for _, db := range c.Server.Databases {
		if db.Name == c.Server.DefaultDatabase {
			return nil
		}
	}
	return fmt.Errorf("default_database '%s' not found in database list", c.Server.DefaultDatabase)
}

// FindDatabase looks up a database by name (case-insensitive).
func (s *ServerConfig) FindDatabase(name string) (*DatabaseConfig, error) {
	for i := range s.Databases {
		if strings.EqualFold(s.Databases[i].Name, name) {
			return &s.Databases[i], nil
		}
	}
	return nil, fmt.Errorf("database '%s' not found. Available: %s", name, s.DatabaseNames())
}

// DatabaseNames returns a comma-separated list of database names.
func (s *ServerConfig) DatabaseNames() string {
	names := make([]string, len(s.Databases))
	for i, db := range s.Databases {
		names[i] = db.Name
	}
	return strings.Join(names, ", ")
}

// IsPathAllowed checks if the given path is inside one of the allowed directories.
func (s *ServerConfig) IsPathAllowed(path string) bool {
	for _, dir := range s.AllowedPaths {
		prefix := dir
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// FindISQL locates the isql (or fbisql) executable.
// It first checks the configured ISQLPath, then searches common locations and PATH.
func (s *ServerConfig) FindISQL() (string, error) {
	// 1. Use configured path if set
	if s.ISQLPath != "" {
		if _, err := os.Stat(s.ISQLPath); err == nil {
			return s.ISQLPath, nil
		}
		return "", fmt.Errorf("configured isql_path '%s' not found", s.ISQLPath)
	}

	// 2. Search common locations
	candidates := []string{
		"isql", "fbisql", "isql64", "fbisql64",
		"/usr/bin/isql", "/usr/bin/fbisql",
		"/usr/local/bin/isql", "/usr/local/bin/fbisql",
		"/opt/firebird/bin/isql", "/opt/firebird/bin/fbisql",
		"/opt/firebird64/bin/isql", "/opt/firebird64/bin/fbisql",
	}

	for _, c := range candidates {
		if strings.Contains(c, "/") {
			// Absolute path: check if it exists
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
		} else {
			// Search PATH
			if path, err := exec.LookPath(c); err == nil {
				return path, nil
			}
		}
	}

	return "", fmt.Errorf("isql not found. Set 'isql_path' in config.json or ensure isql/fbisql is in PATH")
}

// ExecuteWithISQL executes SQL via the isql command-line tool.
// This is used for statements that require SET TERM blocks (procedures, generators, triggers).
func (s *ServerConfig) ExecuteWithISQL(ctx context.Context, dbPath, sql string) (string, error) {
	// Find isql
	isqlPath, err := s.FindISQL()
	if err != nil {
		return "", err
	}

	// Build DSN: host/port:path (isql format)
	dsn := fmt.Sprintf("%s/%d:%s", s.Host, s.Port, dbPath)

	// Create temp file for the script
	tmpFile, err := os.CreateTemp("", "isql-*.sql")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write script with CONNECT and QUIT
	script := fmt.Sprintf("CONNECT %s USER %s PASSWORD %s;\n%s\nquit;\n", dsn, s.User, s.Password, sql)
	if _, err := tmpFile.WriteString(script); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	// Build isql command with input file
	cmd := exec.CommandContext(ctx, isqlPath, "-i", tmpFile.Name())

	// Capture output
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("isql execution failed: %w\nstderr: %s", err, stderr.String())
	}

	return stdout.String(), nil
}

// ExecuteMetadataExtract extracts database metadata using isql -x.
func (s *ServerConfig) ExecuteMetadataExtract(ctx context.Context, dbPath string) (string, error) {
	// Find isql
	isqlPath, err := s.FindISQL()
	if err != nil {
		return "", err
	}

	// Build DSN: host/port:path (isql format)
	dsn := fmt.Sprintf("%s/%d:%s", s.Host, s.Port, dbPath)

	// Build isql command with -x flag for metadata extraction
	cmd := exec.CommandContext(ctx, isqlPath,
		"-x",
		"-u", s.User,
		"-p", s.Password,
		dsn,
	)

	// Capture output
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("isql metadata extraction failed: %w\nstderr: %s", err, stderr.String())
	}

	return stdout.String(), nil
}
