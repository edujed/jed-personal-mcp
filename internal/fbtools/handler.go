// Package fbtools implements the MCP tool handlers and registration for Firebird operations.
package fbtools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

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
			mcp.WithDescription(`Executes a single SQL statement against the Firebird database.

Use for: SELECT queries, INSERT, UPDATE, DELETE, CREATE TABLE, ALTER TABLE, DROP, etc.

Returns (Markdown):
- For SELECT: a title with row count, column definitions with types (e.g. "ID (INT), NAME (VARCHAR)"), and a Markdown table with the data.
- For DML/DDL: a success message.

Example SELECT output:
### Query Results (3 rows)

Columns: ID (INT), NAME (VARCHAR), CITY (VARCHAR)

ID | NAME | CITY
--- | --- | ---
1 | Alice | São Paulo
2 | Bob | NULL

Note: Do NOT use SET TERM delimiters. For multiple statements, use firebird_run_script instead.`),
			mcp.WithString("sql",
				mcp.Required(),
				mcp.Description("The raw SQL statement to execute. Single statement only — no SET TERM, no multiple statements."),
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
			mcp.WithDescription(`Lists all user tables and views in the Firebird database.

Returns (Markdown):
A table with columns NAME, TYPE (TABLE or VIEW), and COMMENT (if any).

Example output:
### Tables and Views (5)

NAME | TYPE | COMMENT
--- | --- | ---
CLIENTS | TABLE | Customer records
ORDERS | TABLE | Order data
ORDERS_VIEW | VIEW | Orders with client info

Use this first to discover what tables exist before querying them.`),
			mcp.WithString("database",
				mcp.Description("Database name (as defined in config.json). Defaults to the default database."),
			),
		),
		h.HandleShowTables,
	)

	s.AddTool(
		mcp.NewTool(
			"firebird_describe_table",
			mcp.WithDescription(`Shows the full schema of a table or view: columns (with type, size, nullability, default), indexes, constraints, triggers, and comments.

Returns (Markdown):
Sections for Columns, Indexes, Constraints, Triggers, and Comments (only non-empty sections are shown).

Example output:
### Table: CLIENTS

#### Columns

NAME | TYPE | NULL | DEFAULT | POSITION
--- | --- | --- | --- | ---
ID | INTEGER | NO | - | 1
NAME | VARCHAR(100) | NO | - | 2
EMAIL | VARCHAR(255) | YES | - | 3
CREATED_AT | TIMESTAMP | NO | CURRENT_TIMESTAMP | 4

#### Indexes

NAME | UNIQUE | TYPE | COLUMNS
--- | --- | --- | ---
PK_CLIENTS | YES | 0 | ID

Use this to understand the structure of a table before writing queries against it.`),
			mcp.WithString("table_name",
				mcp.Required(),
				mcp.Description("Table or view name (without quotes, case-insensitive). E.g. CLIENTS, ORDERS."),
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
			mcp.WithDescription(`Lists all databases configured in config.json.

Returns (Markdown):
A table with columns NAME, DESCRIPTION, PATH, and DEFAULT (YES/NO).

Example output:
### Configured Databases (2)

NAME | DESCRIPTION | PATH | DEFAULT
--- | --- | --- | ---
dev | Local development database | /databases/dev.fdb | YES
prod | Production database | /databases/prod.fdb | NO

Use this to see which database names are available for other tools.`),
		),
		h.HandleGetDatabases,
	)

	s.AddTool(
		mcp.NewTool(
			"firebird_create_database",
			mcp.WithDescription(`Creates a new Firebird database file (.fdb) with a fixed page size of 4096 bytes.

The path must be within one of the allowed_paths directories defined in config.json.

Returns: a success message with the path, or an error if the path is not allowed.

Example: creates /databases/newdb.fdb`),
			mcp.WithString("path",
				mcp.Required(),
				mcp.Description("Full path for the new .fdb file. Must be inside an allowed path from config.json."),
			),
		),
		h.HandleCreateDatabase,
	)

	s.AddTool(
		mcp.NewTool(
			"firebird_run_script",
			mcp.WithDescription(`Executes a SQL script (multiple statements) in a single transaction.

Statements are separated by semicolons. If any statement fails, the entire transaction is rolled back.

Returns: a success message with the number of statements committed, or an error with the statement number that failed.

Use this for multi-statement operations (e.g. CREATE TABLE + INSERT + CREATE INDEX). For a single statement, use firebird_query instead.

Example: "CREATE TABLE T (ID INT); INSERT INTO T VALUES (1); CREATE INDEX IDX_T ON T(ID);"`),
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
			mcp.WithDescription(`Creates a new trigger in the database.

		Uses the isql command-line tool to handle SET TERM blocks.

		Returns: a success message with the trigger name and table (or an error).

		Example: creates a BEFORE INSERT trigger on table ORDERS.

		Note: The body should contain only the SQL statements (without BEGIN/END or SET TERM).`),
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
				mcp.Description("The trigger body (SQL statements only). Do NOT include SET TERM, BEGIN, or END."),
			),
		),
		h.HandleCreateTrigger,
	)

	s.AddTool(
		mcp.NewTool(
			"firebird_insert_batch",
			mcp.WithDescription(`Inserts multiple rows into a table in a single transaction using a prepared statement.

This is far more efficient than calling firebird_query multiple times for individual INSERTs.

Returns: a success message with the number of rows inserted, or an error indicating which row failed.

Example: inserts 3 rows into CLIENTS.

Note: All rows must have the same number of values as columns. The entire batch is atomic — if any row fails, all are rolled back.`),
			mcp.WithString("database",
				mcp.Description("Database name (as defined in config.json). Defaults to the default database."),
			),
			mcp.WithString("table",
				mcp.Required(),
				mcp.Description("Table name to insert into."),
			),
			mcp.WithString("columns",
				mcp.Required(),
				mcp.Description(`Column names as a JSON array. E.g. ["NAME", "EMAIL", "AGE"]`),
			),
			mcp.WithString("rows",
				mcp.Required(),
				mcp.Description(`Row data as a JSON array of arrays. E.g. [["Alice", "a@x.com", 30], ["Bob", "b@x.com", 25]]`),
			),
		),
		h.HandleInsertBatch,
	)

	s.AddTool(
		mcp.NewTool(
			"firebird_count",
			mcp.WithDescription(`Returns the number of rows in a table, optionally filtered by a WHERE clause.

Returns: a simple count message, e.g. "Count: 42 rows".

Use this instead of firebird_query with SELECT COUNT(*) for a cleaner, more token-efficient result.`),
			mcp.WithString("database",
				mcp.Description("Database name (as defined in config.json). Defaults to the default database."),
			),
			mcp.WithString("table",
				mcp.Required(),
				mcp.Description("Table name to count rows from."),
			),
			mcp.WithString("where",
				mcp.Description(`Optional WHERE clause (without the WHERE keyword). E.g. "STATUS = 'ACTIVE' AND CREATED_AT > '2026-01-01'"`),
			),
		),
		h.HandleCount,
	)

	s.AddTool(
		mcp.NewTool(
			"firebird_sample",
			mcp.WithDescription(`Returns a sample of rows from a table (default: 10 rows), optionally filtered by a WHERE clause.

Returns (Markdown):
Same format as firebird_query SELECT results — a title with row count, column definitions with types, and a Markdown table.

Use this to quickly inspect table data without writing a full SELECT query.`),
			mcp.WithString("database",
				mcp.Description("Database name (as defined in config.json). Defaults to the default database."),
			),
			mcp.WithString("table",
				mcp.Required(),
				mcp.Description("Table name to sample from."),
			),
			mcp.WithString("where",
				mcp.Description(`Optional WHERE clause (without the WHERE keyword). E.g. "STATUS = 'ACTIVE'"`),
			),
			mcp.WithString("limit",
				mcp.Description("Maximum number of rows to return. Defaults to 10."),
			),
		),
		h.HandleSample,
	)

	s.AddTool(
		mcp.NewTool(
			"firebird_execute_immediate",
			mcp.WithDescription(`Executes a SQL statement that may contain SET TERM blocks (e.g. CREATE PROCEDURE, CREATE GENERATOR, CREATE FUNCTION).

		Uses the isql command-line tool to handle SET TERM blocks.

		For regular DML/DDL (CREATE TABLE, ALTER TABLE, etc.), use firebird_query instead.

		Returns: a success message or an error.

		Example: "SET TERM ^ ; CREATE GENERATOR GEN_ORDERS ^ SET TERM ; ^"`),
			mcp.WithString("database",
				mcp.Description("Database name (as defined in config.json). Defaults to the default database."),
			),
			mcp.WithString("sql",
				mcp.Required(),
				mcp.Description("The SQL statement to execute. May include SET TERM blocks."),
			),
		),
		h.HandleExecuteImmediate,
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

// resultToText converts a firebird.QueryResult to a Markdown text result.
// It renders a title with the row count, a column definition line, and a
// Markdown table with the rows — which is far more token-efficient than JSON.
func resultToText(result *firebird.QueryResult) *mcp.CallToolResult {
	if result == nil || len(result.Rows) == 0 {
		return mcp.NewToolResultText("No results returned.")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "### Query Results (%d row%s)\n\n", len(result.Rows), pluralSuffix(len(result.Rows)))

	// Column definitions: name (type)
	defs := make([]string, len(result.Columns))
	for i, col := range result.Columns {
		if col.Type != "" {
			defs[i] = fmt.Sprintf("%s (%s)", col.Name, col.Type)
		} else {
			defs[i] = col.Name
		}
	}
	fmt.Fprintf(&b, "Columns: %s\n\n", strings.Join(defs, ", "))

	// Header row
	for i, col := range result.Columns {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString(col.Name)
	}
	b.WriteString("\n")

	// Separator row
	for i := range result.Columns {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString("---")
	}
	b.WriteString("\n")

	// Data rows
	for _, row := range result.Rows {
		for i, col := range result.Columns {
			if i > 0 {
				b.WriteString(" | ")
			}
			b.WriteString(formatCell(row[col.Name]))
		}
		b.WriteString("\n")
	}

	return mcp.NewToolResultText(b.String())
}

// pluralSuffix returns "s" for counts other than 1.
func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// formatCell renders a single cell value for a Markdown table.
// NULLs become "NULL", dates/times are formatted cleanly, newlines are
// flattened, and pipes are escaped so they don't break the table structure.
func formatCell(v any) string {
	if v == nil {
		return "NULL"
	}

	// Format time.Time values with appropriate layout
	if t, ok := v.(time.Time); ok {
		// Check if it's a date-only value (no time component)
		if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && t.Nanosecond() == 0 {
			return t.Format("2006-01-02")
		}
		// Check if it's a time-only value (no date component)
		if t.Year() == 0 {
			return t.Format("15:04:05")
		}
		// Full timestamp
		if t.Nanosecond() != 0 {
			return t.Format("2006-01-02 15:04:05.000000")
		}
		return t.Format("2006-01-02 15:04:05")
	}

	s := fmt.Sprintf("%v", v)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	return s
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

	var b strings.Builder
	fmt.Fprintf(&b, "### Tables and Views (%d)\n\n", len(tables))
	b.WriteString("NAME | TYPE | COMMENT\n")
	b.WriteString("--- | --- | ---\n")
	for _, t := range tables {
		comment := t.Comment
		if comment == "" {
			comment = "-"
		}
		fmt.Fprintf(&b, "%s | %s | %s\n", t.Name, t.Type, comment)
	}

	return mcp.NewToolResultText(b.String()), nil
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

	return mcp.NewToolResultText(formatSchema(schema)), nil
}

// formatSchema converts a TableSchema to a Markdown string.
func formatSchema(schema *firebird.TableSchema) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### Table: %s\n\n", schema.Table)

	// Columns
	if len(schema.Columns) > 0 {
		b.WriteString("#### Columns\n\n")
		b.WriteString("NAME | TYPE | NULL | DEFAULT | POSITION\n")
		b.WriteString("--- | --- | --- | --- | ---\n")
		for _, col := range schema.Columns {
			typeName := firebird.FormatColumnType(col)
			nullable := "NO"
			if firebird.ToInt64(col.Nullable) == 1 {
				nullable = "YES"
			}
			def := "-"
			if col.Default != nil {
				def = fmt.Sprintf("%v", col.Default)
			}
			pos := "-"
			if col.Position != nil {
				pos = fmt.Sprintf("%v", col.Position)
			}
			fmt.Fprintf(&b, "%s | %s | %s | %s | %s\n", col.Name, typeName, nullable, def, pos)
		}
		b.WriteString("\n")
	}

	// Indexes
	if len(schema.Indexes) > 0 {
		b.WriteString("#### Indexes\n\n")
		b.WriteString("NAME | UNIQUE | TYPE | COLUMNS\n")
		b.WriteString("--- | --- | --- | ---\n")
		for _, idx := range schema.Indexes {
			unique := "NO"
			if firebird.ToInt64(idx.Unique) == 1 {
				unique = "YES"
			}
			idxType := "-"
			if idx.Type != nil {
				idxType = fmt.Sprintf("%v", idx.Type)
			}
			fmt.Fprintf(&b, "%s | %s | %s | %s\n", idx.Name, unique, idxType, idx.Columns)
		}
		b.WriteString("\n")
	}

	// Constraints
	if len(schema.Constraints) > 0 {
		b.WriteString("#### Constraints\n\n")
		b.WriteString("NAME | TYPE\n")
		b.WriteString("--- | ---\n")
		for _, con := range schema.Constraints {
			fmt.Fprintf(&b, "%s | %s\n", con.Name, con.Type)
		}
		b.WriteString("\n")
	}

	// Triggers
	if len(schema.Triggers) > 0 {
		b.WriteString("#### Triggers\n\n")
		b.WriteString("NAME | TYPE | SEQUENCE\n")
		b.WriteString("--- | --- | ---\n")
		for _, trg := range schema.Triggers {
			trgType := "-"
			if trg.Type != nil {
				trgType = fmt.Sprintf("%v", trg.Type)
			}
			seq := "-"
			if trg.Sequence != nil {
				seq = fmt.Sprintf("%v", trg.Sequence)
			}
			fmt.Fprintf(&b, "%s | %s | %s\n", trg.Name, trgType, seq)
		}
		b.WriteString("\n")
	}

	// Comments
	if len(schema.Comments) > 0 {
		b.WriteString("#### Comments\n\n")
		for field, comment := range schema.Comments {
			if field == "table" {
				fmt.Fprintf(&b, "**Table:** %s\n\n", comment)
			} else {
				fmt.Fprintf(&b, "**%s:** %s\n\n", field, comment)
			}
		}
	}

	return b.String()
}

// HandleGetDatabases lists the available databases from config.
func (h *Handler) HandleGetDatabases(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "### Configured Databases (%d)\n\n", len(h.Cfg.Server.Databases))
	b.WriteString("NAME | DESCRIPTION | PATH | DEFAULT\n")
	b.WriteString("--- | --- | --- | ---\n")
	for _, db := range h.Cfg.Server.Databases {
		defaultMark := "NO"
		if db.Name == h.Cfg.Server.DefaultDatabase {
			defaultMark = "YES"
		}
		desc := db.Description
		if desc == "" {
			desc = "-"
		}
		fmt.Fprintf(&b, "%s | %s | %s | %s\n", db.Name, desc, db.Path, defaultMark)
	}

	return mcp.NewToolResultText(b.String()), nil
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

	// Build trigger SQL with SET TERM
	triggerSQL := fmt.Sprintf(
		"SET TERM ^ ;\nCREATE TRIGGER \"%s\" FOR \"%s\"\n%s %s\nAS\nBEGIN\n%s\nEND ^\nSET TERM ; ^",
		triggerName, tableName, timing, triggerType, body,
	)

	// Execute via isql
	output, err := h.Cfg.Server.ExecuteWithISQL(ctx, dbCfg.Path, triggerSQL)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to create trigger: %v", err)), nil
	}

	if output != "" {
		return mcp.NewToolResultText(output), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Trigger %s created on table %s.", triggerName, tableName)), nil
}

// HandleInsertBatch inserts multiple rows into a table in a single transaction.
func (h *Handler) HandleInsertBatch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	table, ok := args["table"].(string)
	if !ok || table == "" {
		return mcp.NewToolResultError("The 'table' argument is required and must be a string."), nil
	}
	table = strings.ToUpper(strings.TrimSpace(table))
	if !firebird.IsValidIdentifier(table) {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid table name: %s", table)), nil
	}

	columnsJSON, ok := args["columns"].(string)
	if !ok || columnsJSON == "" {
		return mcp.NewToolResultError("The 'columns' argument is required and must be a JSON array string."), nil
	}
	var columns []string
	if err := json.Unmarshal([]byte(columnsJSON), &columns); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to parse 'columns' as JSON array: %v", err)), nil
	}
	if len(columns) == 0 {
		return mcp.NewToolResultError("The 'columns' array must not be empty."), nil
	}

	rowsJSON, ok := args["rows"].(string)
	if !ok || rowsJSON == "" {
		return mcp.NewToolResultError("The 'rows' argument is required and must be a JSON array of arrays string."), nil
	}
	var rows [][]any
	if err := json.Unmarshal([]byte(rowsJSON), &rows); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to parse 'rows' as JSON array of arrays: %v", err)), nil
	}
	if len(rows) == 0 {
		return mcp.NewToolResultError("The 'rows' array must not be empty."), nil
	}

	client, cleanup, err := h.resolveDB(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer cleanup()

	inserted, err := client.InsertBatch(ctx, table, columns, rows)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Insert failed: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Inserted %d row%s into %s.", inserted, pluralSuffix(inserted), table)), nil
}

// HandleCount returns the number of rows in a table.
func (h *Handler) HandleCount(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	table, ok := args["table"].(string)
	if !ok || table == "" {
		return mcp.NewToolResultError("The 'table' argument is required and must be a string."), nil
	}
	table = strings.ToUpper(strings.TrimSpace(table))
	if !firebird.IsValidIdentifier(table) {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid table name: %s", table)), nil
	}

	where := ""
	if w, ok := args["where"].(string); ok {
		where = strings.TrimSpace(w)
	}

	client, cleanup, err := h.resolveDB(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer cleanup()

	count, err := client.Count(ctx, table, where)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Count failed: %v", err)), nil
	}

	msg := fmt.Sprintf("Count: %d row%s in %s", count, pluralSuffix(int(count)), table)
	if where != "" {
		msg += fmt.Sprintf(" (WHERE %s)", where)
	}
	return mcp.NewToolResultText(msg), nil
}

// HandleSample returns a sample of rows from a table.
func (h *Handler) HandleSample(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	table, ok := args["table"].(string)
	if !ok || table == "" {
		return mcp.NewToolResultError("The 'table' argument is required and must be a string."), nil
	}
	table = strings.ToUpper(strings.TrimSpace(table))
	if !firebird.IsValidIdentifier(table) {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid table name: %s", table)), nil
	}

	where := ""
	if w, ok := args["where"].(string); ok {
		where = strings.TrimSpace(w)
	}

	limit := 10
	if l, ok := args["limit"].(string); ok && l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	client, cleanup, err := h.resolveDB(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer cleanup()

	result, err := client.Sample(ctx, table, where, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Sample failed: %v", err)), nil
	}

	return resultToText(result), nil
}

// HandleExecuteImmediate executes a SQL statement that may contain SET TERM blocks.
// It uses the isql command-line tool to handle SET TERM delimiters.
func (h *Handler) HandleExecuteImmediate(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	sqlArg, ok := args["sql"].(string)
	if !ok || sqlArg == "" {
		return mcp.NewToolResultError("The 'sql' argument is required and must be a string."), nil
	}

	// Get database config
	dbName, _ := args["database"].(string)
	if dbName == "" {
		dbName = h.Cfg.Server.DefaultDatabase
	}

	db, err := h.Cfg.Server.FindDatabase(dbName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Execute via isql
	output, err := h.Cfg.Server.ExecuteWithISQL(ctx, db.Path, sqlArg)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Execution failed: %v", err)), nil
	}

	if output != "" {
		return mcp.NewToolResultText(output), nil
	}
	return mcp.NewToolResultText("Statement executed successfully."), nil
}
