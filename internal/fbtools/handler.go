// Package fbtools implements the MCP tool handlers and registration for Firebird operations.
package fbtools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/edujed/jed-personal-mcp/internal/config"
	"github.com/edujed/jed-personal-mcp/internal/firebird"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Handler holds the dependencies for all tool handlers.
type Handler struct {
	Cfg *config.Config
}

// NewHandler creates a new tool handler with the given configuration.
func NewHandler(cfg *config.Config) *Handler {
	return &Handler{Cfg: cfg}
}

// Register registers all Firebird MCP tools with the given server.
func Register(s *server.MCPServer, h *Handler) {
	s.AddTool(
		mcp.NewTool(
			"firebird_query",
			mcp.WithDescription("Executes SQL commands (SELECT, INSERT, UPDATE, CREATE TABLE, CREATE TRIGGER) on the Firebird database."),
			mcp.WithString("sql",
				mcp.Required(),
				mcp.Description("The raw SQL statement to execute. Do not use delimiters like SET TERM."),
			),
			mcp.WithString("database",
				mcp.Description("Database name (as defined in config.json). Defaults to the default database."),
			),
		),
		h.HandleQuery,
	)

	s.AddTool(
		mcp.NewTool(
			"firebird_show_tables",
			mcp.WithDescription("Lists all tables and views in the Firebird database, with type and optional comment."),
			mcp.WithString("database",
				mcp.Description("Database name (as defined in config.json). Defaults to the default database."),
			),
		),
		h.HandleShowTables,
	)

	s.AddTool(
		mcp.NewTool(
			"firebird_describe_table",
			mcp.WithDescription("Shows the full schema of a table or view: columns, indexes, constraints, triggers, and generators."),
			mcp.WithString("table_name",
				mcp.Required(),
				mcp.Description("Table or view name (without quotes). E.g. CLIENTS, ORDERS."),
			),
			mcp.WithString("database",
				mcp.Description("Database name (as defined in config.json). Defaults to the default database."),
			),
		),
		h.HandleDescribeTable,
	)

	s.AddTool(
		mcp.NewTool(
			"firebird_get_databases",
			mcp.WithDescription("Lists the available databases configured in config.json."),
		),
		h.HandleGetDatabases,
	)

	s.AddTool(
		mcp.NewTool(
			"firebird_create_database",
			mcp.WithDescription("Creates a new Firebird database file with a fixed page size of 4096 bytes. The path must be within one of the allowed_paths defined in config.json."),
			mcp.WithString("path",
				mcp.Required(),
				mcp.Description("Full path for the new .fdb file. Must be inside an allowed path."),
			),
		),
		h.HandleCreateDatabase,
	)

	s.AddTool(
		mcp.NewTool(
			"firebird_run_script",
			mcp.WithDescription("Executes a SQL script (multiple statements) in a single transaction. Statements are separated by semicolons. If any statement fails, the entire transaction is rolled back."),
			mcp.WithString("database",
				mcp.Required(),
				mcp.Description("Database name (as defined in config.json). This argument is required to avoid ambiguity."),
			),
			mcp.WithString("script",
				mcp.Description("The SQL script to execute. Multiple statements separated by semicolons."),
			),
			mcp.WithString("path",
				mcp.Description("Path to a .sql file to execute. Alternative to providing the script inline."),
			),
		),
		h.HandleRunScript,
	)

	s.AddTool(
		mcp.NewTool(
			"firebird_create_trigger",
			mcp.WithDescription("Creates a new trigger in the database. Handles the SET TERM delimiter automatically."),
			mcp.WithString("database",
				mcp.Required(),
				mcp.Description("Database name (as defined in config.json)."),
			),
			mcp.WithString("trigger_name",
				mcp.Required(),
				mcp.Description("Name for the new trigger."),
			),
			mcp.WithString("table_name",
				mcp.Required(),
				mcp.Description("Table the trigger will be associated with."),
			),
			mcp.WithString("trigger_type",
				mcp.Required(),
				mcp.Description("Trigger event type: INSERT, UPDATE, or DELETE."),
			),
			mcp.WithString("timing",
				mcp.Required(),
				mcp.Description("Trigger timing: BEFORE or AFTER."),
			),
			mcp.WithString("body",
				mcp.Required(),
				mcp.Description("The trigger body (SQL statements). Do not include SET TERM or BEGIN/END."),
			),
		),
		h.HandleCreateTrigger,
	)
}

// --- Helpers ---

// resolveDB opens a client for the requested database (or the default).
func (h *Handler) resolveDB(request mcp.CallToolRequest) (*firebird.Client, func(), error) {
	dbName := getDatabaseArg(request)
	if dbName == "" {
		dbName = h.Cfg.Server.DefaultDatabase
	}

	dbCfg, err := h.Cfg.Server.FindDatabase(dbName)
	if err != nil {
		return nil, nil, err
	}

	client, err := firebird.NewClient(&h.Cfg.Server, dbCfg.Path)
	if err != nil {
		return nil, nil, err
	}

	cleanup := func() { client.Close() }
	return client, cleanup, nil
}

// getDatabaseArg extracts the optional "database" argument from the request.
func getDatabaseArg(request mcp.CallToolRequest) string {
	args := request.GetArguments()
	if db, ok := args["database"].(string); ok {
		return db
	}
	return ""
}

// resultToText converts a firebird.QueryResult to a JSON text result.
func resultToText(result *firebird.QueryResult) *mcp.CallToolResult {
	if result == nil || len(result.Rows) == 0 {
		return mcp.NewToolResultText("No results returned.")
	}
	data, _ := json.MarshalIndent(result.Rows, "", "  ")
	return mcp.NewToolResultText(string(data))
}

// --- Tool Handlers ---

// HandleQuery executes a generic SQL command (SELECT, DML, DDL).
func (h *Handler) HandleQuery(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	sqlArg, ok := args["sql"].(string)
	if !ok || sqlArg == "" {
		return mcp.NewToolResultError("The 'sql' argument is required and must be a string."), nil
	}

	client, cleanup, err := h.resolveDB(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer cleanup()

	result, message, err := client.Execute(ctx, sqlArg)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if result != nil {
		return resultToText(result), nil
	}
	return mcp.NewToolResultText(message), nil
}

// HandleShowTables lists all tables and views in the database.
func (h *Handler) HandleShowTables(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	client, cleanup, err := h.resolveDB(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer cleanup()

	tables, err := client.ListTables(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if len(tables) == 0 {
		return mcp.NewToolResultText("No tables found."), nil
	}

	data, _ := json.MarshalIndent(tables, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

// HandleDescribeTable shows the full schema of a table or view.
func (h *Handler) HandleDescribeTable(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	tableName, ok := args["table_name"].(string)
	if !ok || tableName == "" {
		return mcp.NewToolResultError("The 'table_name' argument is required and must be a string."), nil
	}
	tableName = strings.ToUpper(strings.TrimSpace(tableName))
	if !firebird.IsValidIdentifier(tableName) {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid table name: %s", tableName)), nil
	}

	client, cleanup, err := h.resolveDB(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer cleanup()

	schema, err := client.DescribeTable(ctx, tableName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	data, _ := json.MarshalIndent(schema, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

// HandleGetDatabases lists the available databases from config.
func (h *Handler) HandleGetDatabases(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	type dbInfo struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Path        string `json:"path"`
		IsDefault   bool   `json:"is_default"`
	}

	var infos []dbInfo
	for _, db := range h.Cfg.Server.Databases {
		infos = append(infos, dbInfo{
			Name:        db.Name,
			Description: db.Description,
			Path:        db.Path,
			IsDefault:   db.Name == h.Cfg.Server.DefaultDatabase,
		})
	}

	data, _ := json.MarshalIndent(infos, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

// HandleCreateDatabase creates a new Firebird database file.
func (h *Handler) HandleCreateDatabase(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return mcp.NewToolResultError("The 'path' argument is required and must be a string."), nil
	}
	path = strings.TrimSpace(path)

	if len(h.Cfg.Server.AllowedPaths) == 0 {
		return mcp.NewToolResultError("No allowed paths configured. Add 'allowed_paths' to the server config to enable database creation."), nil
	}
	if !h.Cfg.Server.IsPathAllowed(path) {
		return mcp.NewToolResultError(fmt.Sprintf(
			"Path '%s' is not within any allowed path. Allowed: %s",
			path, strings.Join(h.Cfg.Server.AllowedPaths, ", "),
		)), nil
	}

	if err := firebird.CreateDatabase(ctx, &h.Cfg.Server, path); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Database created successfully at %s (page size: 4096).", path)), nil
}

// HandleRunScript executes a SQL script in a single transaction.
func (h *Handler) HandleRunScript(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	// Database is required
	dbName, ok := args["database"].(string)
	if !ok || dbName == "" {
		return mcp.NewToolResultError("The 'database' argument is required."), nil
	}

	// Get script from inline text or file path
	var script string
	if s, ok := args["script"].(string); ok && s != "" {
		script = s
	} else if p, ok := args["path"].(string); ok && p != "" {
		// Read script from file
		data, err := os.ReadFile(p)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to read script file: %v", err)), nil
		}
		script = string(data)
	} else {
		return mcp.NewToolResultError("Either 'script' or 'path' argument is required."), nil
	}

	// Resolve database
	dbCfg, err := h.Cfg.Server.FindDatabase(dbName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	client, err := firebird.NewClient(&h.Cfg.Server, dbCfg.Path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer client.Close()

	executed, err := client.RunScript(ctx, script)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Script execution failed: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Script executed successfully. %d statements committed.", executed)), nil
}

// HandleCreateTrigger creates a new trigger in the database.
func (h *Handler) HandleCreateTrigger(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	// Extract required arguments
	dbName, ok := args["database"].(string)
	if !ok || dbName == "" {
		return mcp.NewToolResultError("The 'database' argument is required."), nil
	}

	triggerName, ok := args["trigger_name"].(string)
	if !ok || triggerName == "" {
		return mcp.NewToolResultError("The 'trigger_name' argument is required."), nil
	}

	tableName, ok := args["table_name"].(string)
	if !ok || tableName == "" {
		return mcp.NewToolResultError("The 'table_name' argument is required."), nil
	}

	triggerType, ok := args["trigger_type"].(string)
	if !ok || triggerType == "" {
		return mcp.NewToolResultError("The 'trigger_type' argument is required."), nil
	}
	triggerType = strings.ToUpper(triggerType)

	timing, ok := args["timing"].(string)
	if !ok || timing == "" {
		return mcp.NewToolResultError("The 'timing' argument is required."), nil
	}
	timing = strings.ToUpper(timing)

	body, ok := args["body"].(string)
	if !ok || body == "" {
		return mcp.NewToolResultError("The 'body' argument is required."), nil
	}

	// Resolve database
	dbCfg, err := h.Cfg.Server.FindDatabase(dbName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	client, err := firebird.NewClient(&h.Cfg.Server, dbCfg.Path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer client.Close()

	if err := client.CreateTrigger(ctx, triggerName, tableName, triggerType, timing, body); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to create trigger: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Trigger '%s' created successfully on table '%s'.", triggerName, tableName)), nil
}
