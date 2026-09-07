// Package config handles loading and validating the MCP server configuration.
package config

import (
	"encoding/json"
	"fmt"
	"os"
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
