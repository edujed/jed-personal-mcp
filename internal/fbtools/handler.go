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

	s.AddTool(
		mcp.NewTool(
			"firebird_describe_database",
			mcp.WithDescription("Provides a complete overview of the database structure.\n\n"+
				"Lists all tables with their columns, types, and constraints, and generates a Mermaid ER diagram showing all relationships.\n\n"+
				"Returns (Markdown):\n"+
				"- A summary of all tables with column details\n"+
				"- A Mermaid ER diagram showing the complete relationship map\n\n"+
				"Parameters:\n"+
				"- database: Database name (as defined in config.json). Defaults to the default database.\n"+
				"- include_counts: If true, includes row counts for each table (may be slow for large tables). Defaults to false.\n"+
				"- include_describes: If true, includes column details for each table. Defaults to true.\n\n"+
				"Example output:\n"+
				"### Database: teste\n\n"+
				"#### Tables (9)\n\n"+
				"**DEPARTAMENTO** (12 rows)\n"+
				"- ID (INTEGER, PK)\n"+
				"- NOME (VARCHAR(400), UNIQUE)\n"+
				"...\n\n"+
				"#### Relationships (Mermaid)\n"+
				"```mermaid\n"+
				"erDiagram\n"+
				"    DEPARTAMENTO ||--o{ USUARIO : \"has\"\n"+
				"    USUARIO ||--o{ USUARIO_PERFIL : \"has\"\n"+
				"    ...\n"+
				"```"),
			mcp.WithString("database",
				mcp.Description("Database name (as defined in config.json). Defaults to the default database."),
			),
			mcp.WithBoolean("include_counts",
				mcp.Description("If true, includes row counts for each table. May be slow for large tables. Defaults to false."),
			),
			mcp.WithBoolean("include_describes",
				mcp.Description("If true, includes column details for each table. Defaults to true."),
			),
		),
		h.HandleDescribeDatabase,
	)

	s.AddTool(
		mcp.NewTool(
			"firebird_list",
			mcp.WithDescription(`Lists database objects by type.

Supported types:
- TABLES: Lists all tables and views
- PROCEDURES: Lists all stored procedures
- FUNCTIONS: Lists all user-defined functions
- DOMAINS: Lists all domains (named data types)
- TRIGGERS: Lists all triggers
- INDEXES: Lists all indexes
- SEQUENCES: Lists all sequences

Returns (Markdown):
- A title with the count of objects
- A table with relevant columns for each type

Example output (TABLES):
### Tables and Views (5)

NAME | TYPE | COMMENT
--- | --- | ---
CLIENTS | TABLE | Customer records
ORDERS | TABLE | Order data

Example output (PROCEDURES):
### Stored Procedures

Count: 3 procedures

NAME | PARAMETERS | RETURNS | DESCRIPTION
--- | --- | --- | ---
SP_EXAMPLE | 1 | 2 | Example procedure`),
			mcp.WithString("type",
				mcp.Required(),
				mcp.Description("The object type: TABLES, PROCEDURES, FUNCTIONS, DOMAINS, TRIGGERS, INDEXES, or SEQUENCES"),
			),
			mcp.WithString("database",
				mcp.Description("Database name (as defined in config.json). Defaults to the default database."),
			),
		),
		h.HandleList,
	)

	s.AddTool(
		mcp.NewTool(
			"firebird_metadata_extract",
			mcp.WithDescription(`Extracts complete database metadata using isql -x (extract mode).

This returns the full DDL script for the database, including:
- CREATE DATABASE
- CREATE TABLE statements
- CREATE INDEX statements
- CREATE VIEW statements
- CREATE PROCEDURE statements
- CREATE FUNCTION statements
- CREATE TRIGGER statements
- GRANT statements

The output is a SQL script that can be used to recreate the database structure.

Returns: The raw SQL script from isql -x (stdout).

Note: This can be a large output for databases with many objects.`),
			mcp.WithString("database",
				mcp.Description("Database name (as defined in config.json). Defaults to the default database."),
			),
		),
		h.HandleMetadataExtract,
	)

	s.AddTool(
		mcp.NewTool(
			"firebird_alter_column_type",
			mcp.WithDescription(`Changes the data type of a column to use a domain.

Uses the Firebird syntax: ALTER TABLE table_name ALTER column_name TYPE domain_name;

Parameters:
- table: The table name
- column: The column name
- domain: The domain name to apply

Example: Changes column NOME in table CLIENTS to use domain D_REQUIRED_VARCHAR_100
firebird_alter_column_type(table="CLIENTS", column="NOME", domain="D_REQUIRED_VARCHAR_100")

Returns: A success message or an error.

IMPORTANT - Key Constraints: If the column is part of a Primary Key (PK), Foreign Key (FK), or has a UNIQUE constraint, Firebird will block the alteration. In these cases, you must:
1. Drop the constraint first
2. Alter the column type
3. Recreate the constraint

Example for a PK column:
-- Drop the PK
ALTER TABLE CLIENTS DROP CONSTRAINT PK_CLIENTS;
-- Alter the column
firebird_alter_column_type(table="CLIENTS", column="ID", domain="D_REQUIRED_INTEGER")
-- Recreate the PK
ALTER TABLE CLIENTS ADD CONSTRAINT PK_CLIENTS PRIMARY KEY (ID);`),
			mcp.WithString("table",
				mcp.Required(),
				mcp.Description("The table name"),
			),
			mcp.WithString("column",
				mcp.Required(),
				mcp.Description("The column name"),
			),
			mcp.WithString("domain",
				mcp.Required(),
				mcp.Description("The domain name to apply"),
			),
			mcp.WithString("database",
				mcp.Description("Database name (as defined in config.json). Defaults to the default database."),
			),
		),
		h.HandleAlterColumnType,
	)

	s.AddTool(
		mcp.NewTool(
			"firebird_drop",
			mcp.WithDescription(`Drops a database object by type and name.

Supported types:
- TABLE: DROP TABLE name
- VIEW: DROP VIEW name
- DOMAIN: DROP DOMAIN name
- TRIGGER: DROP TRIGGER name
- PROCEDURE: DROP PROCEDURE name
- FUNCTION: DROP FUNCTION name
- INDEX: DROP INDEX name
- SEQUENCE: DROP SEQUENCE name

Returns: A success message or an error.

Example: Drops table CLIENTS
firebird_drop(type="TABLE", name="CLIENTS")

Example: Drops procedure SP_EXAMPLE
firebird_drop(type="PROCEDURE", name="SP_EXAMPLE")`),
			mcp.WithString("type",
				mcp.Required(),
				mcp.Description("The object type: TABLE, VIEW, DOMAIN, TRIGGER, PROCEDURE, FUNCTION, INDEX, or SEQUENCE"),
			),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("The name of the object to drop"),
			),
			mcp.WithString("database",
				mcp.Description("Database name (as defined in config.json). Defaults to the default database."),
			),
		),
		h.HandleDrop,
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

	// Format boolean values
	if b, ok := v.(bool); ok {
		if b {
			return "true"
		}
		return "false"
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
		b.WriteString("NAME | TYPE | DOMAIN | NULL | DEFAULT | POSITION\n")
		b.WriteString("--- | --- | --- | --- | --- | ---\n")
		for _, col := range schema.Columns {
			typeName := firebird.FormatColumnType(col)
			domain := "-"
			if col.Domain != "" {
				domain = col.Domain
			}
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
			fmt.Fprintf(&b, "%s | %s | %s | %s | %s | %s\n", col.Name, typeName, domain, nullable, def, pos)
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

// HandleDescribeDatabase provides a complete overview of the database structure.
func (h *Handler) HandleDescribeDatabase(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	// Get database name
	dbName, _ := args["database"].(string)
	if dbName == "" {
		dbName = h.Cfg.Server.DefaultDatabase
	}

	// Get include_counts flag
	includeCounts, _ := args["include_counts"].(bool)

	// Get include_describes flag (defaults to true)
	includeDescribes := true
	if v, ok := args["include_describes"].(bool); ok {
		includeDescribes = v
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

	// Get all tables
	tables, err := client.ListTables(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list tables: %v", err)), nil
	}

	// Build output
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### Database: %s\n\n", dbName))
	sb.WriteString(fmt.Sprintf("#### Tables (%d)\n\n", len(tables)))

	// Get foreign keys
	fks, err := client.GetForeignKeys(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get foreign keys: %v", err)), nil
	}

	// Process each table
	for _, table := range tables {
		if table.Type != "TABLE" {
			continue // Skip views
		}

		sb.WriteString(fmt.Sprintf("**%s**", table.Name))

		// Get row count if requested
		if includeCounts {
			count, err := client.Count(ctx, table.Name, "")
			if err == nil {
				sb.WriteString(fmt.Sprintf(" (%d rows)", count))
			}
		}
		sb.WriteString("\n")

		// Get table schema if includes describes
		if includeDescribes {
			schema, err := client.DescribeTable(ctx, table.Name)
			if err != nil {
				sb.WriteString("  - (failed to get schema)\n\n")
				continue
			}

			// Add table comment if exists
			if schema.Comments != nil {
				if tableComment, ok := schema.Comments["table"]; ok && tableComment != "" {
					sb.WriteString(fmt.Sprintf("  // %s\n", tableComment))
				}
			}

			// Get PK columns from indexes (PK indexes have names starting with RDB$PRIMARY)
			pkColumns := make(map[string]bool)
			for _, idx := range schema.Indexes {
				if strings.HasPrefix(idx.Name, "RDB$PRIMARY") {
					for _, colName := range strings.Split(idx.Columns, ", ") {
						pkColumns[strings.TrimSpace(colName)] = true
					}
				}
			}

			// List columns
			for _, col := range schema.Columns {
				colType := firebird.FormatColumnType(col)
				colLine := fmt.Sprintf("  - %s (%s", col.Name, colType)

				// Add PK/NOT NULL info
				if pkColumns[col.Name] {
					colLine += ", PK"
				}
				if col.Nullable == nil || col.Nullable == int64(0) {
					colLine += ", NOT NULL"
				}
				colLine += ")"

				// Add column comment if exists
				if schema.Comments != nil {
					if colComment, ok := schema.Comments[col.Name]; ok && colComment != "" {
						colLine += fmt.Sprintf(" // %s", colComment)
					}
				}

				sb.WriteString(colLine + "\n")
			}
		}

		sb.WriteString("\n")
	}

	// Generate Mermaid ER diagram
	sb.WriteString("#### Relationships (Mermaid)\n")
	sb.WriteString("```mermaid\nerDiagram\n")

	// Add relationships from foreign keys
	for _, fk := range fks {
		// Determine relationship type (simplified: assume 1:N for now)
		sb.WriteString(fmt.Sprintf("    %s ||--o{ %s : \"%s\"\n",
			fk.RefTable, fk.Table, fk.ConstraintName))
	}

	sb.WriteString("```\n")

	return mcp.NewToolResultText(sb.String()), nil
}

// HandleMetadataExtract extracts complete database metadata using isql -x.
func (h *Handler) HandleMetadataExtract(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	// Get database name
	dbName, _ := args["database"].(string)
	if dbName == "" {
		dbName = h.Cfg.Server.DefaultDatabase
	}

	db, err := h.Cfg.Server.FindDatabase(dbName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Execute isql -x to extract metadata
	output, err := h.Cfg.Server.ExecuteMetadataExtract(ctx, db.Path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Metadata extraction failed: %v", err)), nil
	}

	return mcp.NewToolResultText(output), nil
}

// HandleAlterColumnType changes the data type of a column to use a domain.
func (h *Handler) HandleAlterColumnType(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	table, _ := args["table"].(string)
	column, _ := args["column"].(string)
	domain, _ := args["domain"].(string)

	if table == "" || column == "" || domain == "" {
		return mcp.NewToolResultError("table, column, and domain are required"), nil
	}

	client, cleanup, err := h.resolveDB(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer cleanup()

	sql := fmt.Sprintf(`ALTER TABLE %s ALTER %s TYPE %s`, table, column, domain)
	_, err = client.Exec(ctx, sql)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to alter column type: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Column %s.%s type changed to %s.", table, column, domain)), nil
}

// HandleDrop drops a database object by type and name.
func (h *Handler) HandleDrop(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	objType, _ := args["type"].(string)
	name, _ := args["name"].(string)

	if objType == "" || name == "" {
		return mcp.NewToolResultError("Both 'type' and 'name' arguments are required."), nil
	}

	objType = strings.ToUpper(strings.TrimSpace(objType))
	name = strings.TrimSpace(name)

	// Validate object type
	validTypes := map[string]bool{
		"TABLE": true, "VIEW": true, "DOMAIN": true, "TRIGGER": true,
		"PROCEDURE": true, "FUNCTION": true, "INDEX": true, "SEQUENCE": true,
	}
	if !validTypes[objType] {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid type: %s. Valid types: TABLE, VIEW, DOMAIN, TRIGGER, PROCEDURE, FUNCTION, INDEX, SEQUENCE", objType)), nil
	}

	// Validate name
	if !firebird.IsValidIdentifier(name) {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid name: %s", name)), nil
	}

	client, cleanup, err := h.resolveDB(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer cleanup()

	// Build DROP statement
	var sql string
	switch objType {
	case "TABLE":
		sql = fmt.Sprintf(`DROP TABLE "%s"`, name)
	case "VIEW":
		sql = fmt.Sprintf(`DROP VIEW "%s"`, name)
	case "DOMAIN":
		sql = fmt.Sprintf(`DROP DOMAIN "%s"`, name)
	case "TRIGGER":
		sql = fmt.Sprintf(`DROP TRIGGER "%s"`, name)
	case "PROCEDURE":
		sql = fmt.Sprintf(`DROP PROCEDURE "%s"`, name)
	case "FUNCTION":
		sql = fmt.Sprintf(`DROP FUNCTION "%s"`, name)
	case "INDEX":
		sql = fmt.Sprintf(`DROP INDEX "%s"`, name)
	case "SEQUENCE":
		sql = fmt.Sprintf(`DROP SEQUENCE "%s"`, name)
	}

	_, err = client.Exec(ctx, sql)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to drop %s %s: %v", objType, name, err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Dropped %s %s.", objType, name)), nil
}

// HandleList lists database objects by type (PROCEDURES, FUNCTIONS, DOMAINS).
func (h *Handler) HandleList(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	objType, _ := args["type"].(string)
	if objType == "" {
		return mcp.NewToolResultError("The 'type' argument is required."), nil
	}

	objType = strings.ToUpper(strings.TrimSpace(objType))

	// Validate object type
	validTypes := map[string]bool{
		"TABLES": true, "PROCEDURES": true, "FUNCTIONS": true, "DOMAINS": true,
		"TRIGGERS": true, "INDEXES": true, "SEQUENCES": true,
	}
	if !validTypes[objType] {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid type: %s. Valid types: TABLES, PROCEDURES, FUNCTIONS, DOMAINS, TRIGGERS, INDEXES, SEQUENCES", objType)), nil
	}

	client, cleanup, err := h.resolveDB(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer cleanup()

	var sb strings.Builder

	switch objType {
	case "TABLES":
		tables, err := client.ListTables(ctx)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to list tables: %v", err)), nil
		}

		count := len(tables)
		sb.WriteString(fmt.Sprintf("### Tables and Views (%d)\n\n", count))
		if count == 0 {
			sb.WriteString("No tables or views found.\n")
			return mcp.NewToolResultText(sb.String()), nil
		}

		sb.WriteString("NAME | TYPE | COMMENT\n")
		sb.WriteString("--- | --- | ---\n")

		for _, table := range tables {
			comment := table.Comment
			if comment == "" {
				comment = "-"
			}
			sb.WriteString(fmt.Sprintf("%s | %s | %s\n", table.Name, table.Type, comment))
		}

	case "PROCEDURES":
		result, err := client.Query(ctx, `SELECT
			RDB$PROCEDURE_NAME,
			RDB$PROCEDURE_INPUTS,
			RDB$PROCEDURE_OUTPUTS,
			RDB$DESCRIPTION
		FROM RDB$PROCEDURES
		ORDER BY RDB$PROCEDURE_NAME`)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to list procedures: %v", err)), nil
		}

		count := len(result.Rows)
		sb.WriteString("### Stored Procedures\n\n")
		if count == 0 {
			sb.WriteString("No stored procedures found.\n")
			return mcp.NewToolResultText(sb.String()), nil
		}

		sb.WriteString(fmt.Sprintf("Count: %d procedures\n\n", count))
		sb.WriteString("NAME | PARAMETERS | RETURNS | DESCRIPTION\n")
		sb.WriteString("--- | --- | --- | ---\n")

		for _, row := range result.Rows {
			name, _ := row["RDB$PROCEDURE_NAME"].(string)
			params := firebird.ToInt64(row["RDB$PROCEDURE_INPUTS"])
			returns := firebird.ToInt64(row["RDB$PROCEDURE_OUTPUTS"])
			desc, _ := row["RDB$DESCRIPTION"].(string)
			if desc == "" {
				desc = "-"
			}
			sb.WriteString(fmt.Sprintf("%s | %d | %d | %s\n", name, params, returns, desc))
		}

	case "FUNCTIONS":
		result, err := client.Query(ctx, `SELECT
			RDB$FUNCTION_NAME,
			RDB$FUNCTION_TYPE,
			RDB$DESCRIPTION
		FROM RDB$FUNCTIONS
		ORDER BY RDB$FUNCTION_NAME`)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to list functions: %v", err)), nil
		}

		count := len(result.Rows)
		sb.WriteString("### User-Defined Functions\n\n")
		if count == 0 {
			sb.WriteString("No user-defined functions found.\n")
			return mcp.NewToolResultText(sb.String()), nil
		}

		sb.WriteString(fmt.Sprintf("Count: %d functions\n\n", count))
		sb.WriteString("NAME | TYPE | DESCRIPTION\n")
		sb.WriteString("--- | --- | ---\n")

		for _, row := range result.Rows {
			name, _ := row["RDB$FUNCTION_NAME"].(string)
			fnType := firebird.ToInt64(row["RDB$FUNCTION_TYPE"])
			desc, _ := row["RDB$DESCRIPTION"].(string)
			if desc == "" {
				desc = "-"
			}
			typeName := "-"
			if fnType == 0 {
				typeName = "SQL"
			} else if fnType == 1 {
				typeName = "PASM"
			}
			sb.WriteString(fmt.Sprintf("%s | %s | %s\n", name, typeName, desc))
		}

	case "DOMAINS":
		result, err := client.Query(ctx, `SELECT
			RDB$FIELD_NAME,
			RDB$FIELD_TYPE,
			RDB$FIELD_SUB_TYPE,
			RDB$FIELD_LENGTH,
			RDB$FIELD_PRECISION,
			RDB$FIELD_SCALE,
			RDB$DEFAULT_SOURCE,
			RDB$NULL_FLAG,
			RDB$DESCRIPTION
		FROM RDB$FIELDS
		WHERE RDB$SYSTEM_FLAG = 0
		  AND RDB$FIELD_NAME NOT LIKE 'RDB$%'
		  AND RDB$FIELD_NAME NOT LIKE 'MON$%'
		  AND RDB$FIELD_NAME NOT LIKE 'SEC$%'
		ORDER BY RDB$FIELD_NAME`)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to list domains: %v", err)), nil
		}

		count := len(result.Rows)
		sb.WriteString("### Domains (Named Data Types)\n\n")
		if count == 0 {
			sb.WriteString("No domains found.\n")
			return mcp.NewToolResultText(sb.String()), nil
		}

		sb.WriteString(fmt.Sprintf("Count: %d domains\n\n", count))
		sb.WriteString("NAME | TYPE | DEFAULT | NULL\n")
		sb.WriteString("--- | --- | --- | ---\n")

		for _, row := range result.Rows {
			name, _ := row["RDB$FIELD_NAME"].(string)
			fieldType := firebird.ToInt64(row["RDB$FIELD_TYPE"])
			subType := firebird.ToInt64(row["RDB$FIELD_SUB_TYPE"])
			length := firebird.ToInt64(row["RDB$FIELD_LENGTH"])
			precision := firebird.ToInt64(row["RDB$FIELD_PRECISION"])
			scale := firebird.ToInt64(row["RDB$FIELD_SCALE"])
			defaultSource, _ := row["RDB$DEFAULT_SOURCE"].(string)
			nullFlag := firebird.ToInt64(row["RDB$NULL_FLAG"])

			// Use FormatColumnTypeWithMeta for accurate type formatting
			// This handles:
			// - NUMERIC/DECIMAL types (using RDB$FIELD_SUB_TYPE)
			// - Type 12: DATE (not CHAR)
			// - Character types: bytes → characters conversion
			typeName := firebird.FormatColumnTypeWithMeta(fieldType, subType, length, precision, scale)
			defStr := "-"
			if defaultSource != "" {
				defStr = defaultSource
			}
			nullStr := "YES"
			if nullFlag == 1 {
				nullStr = "NO"
			}
			sb.WriteString(fmt.Sprintf("%s | %s | %s | %s\n", name, typeName, defStr, nullStr))
		}

	case "TRIGGERS":
		result, err := client.Query(ctx, `SELECT
			RDB$TRIGGER_NAME,
			RDB$TRIGGER_TYPE,
			RDB$RELATION_NAME,
			RDB$DESCRIPTION
		FROM RDB$TRIGGERS
		WHERE RDB$SYSTEM_FLAG = 0
		ORDER BY RDB$TRIGGER_NAME`)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to list triggers: %v", err)), nil
		}

		count := len(result.Rows)
		sb.WriteString("### Triggers\n\n")
		if count == 0 {
			sb.WriteString("No triggers found.\n")
			return mcp.NewToolResultText(sb.String()), nil
		}

		sb.WriteString(fmt.Sprintf("Count: %d triggers\n\n", count))
		sb.WriteString("NAME | TYPE | TABLE | DESCRIPTION\n")
		sb.WriteString("--- | --- | --- | ---\n")

		for _, row := range result.Rows {
			name, _ := row["RDB$TRIGGER_NAME"].(string)
			trigType := firebird.ToInt64(row["RDB$TRIGGER_TYPE"])
			table, _ := row["RDB$RELATION_NAME"].(string)
			desc, _ := row["RDB$DESCRIPTION"].(string)
			if desc == "" {
				desc = "-"
			}
			typeName := "-"
			if trigType == 1 {
				typeName = "BEFORE INSERT"
			} else if trigType == 2 {
				typeName = "BEFORE UPDATE"
			} else if trigType == 4 {
				typeName = "BEFORE DELETE"
			} else if trigType == 8 {
				typeName = "AFTER INSERT"
			} else if trigType == 16 {
				typeName = "AFTER UPDATE"
			} else if trigType == 32 {
				typeName = "AFTER DELETE"
			}
			sb.WriteString(fmt.Sprintf("%s | %s | %s | %s\n", name, typeName, table, desc))
		}

	case "INDEXES":
		result, err := client.Query(ctx, `SELECT
			RDB$INDEX_NAME,
			RDB$RELATION_NAME,
			RDB$UNIQUE_FLAG,
			RDB$DESCRIPTION
		FROM RDB$INDICES
		WHERE RDB$SYSTEM_FLAG = 0
		ORDER BY RDB$INDEX_NAME`)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to list indexes: %v", err)), nil
		}

		count := len(result.Rows)
		sb.WriteString("### Indexes\n\n")
		if count == 0 {
			sb.WriteString("No indexes found.\n")
			return mcp.NewToolResultText(sb.String()), nil
		}

		sb.WriteString(fmt.Sprintf("Count: %d indexes\n\n", count))
		sb.WriteString("NAME | TABLE | UNIQUE | DESCRIPTION\n")
		sb.WriteString("--- | --- | --- | ---\n")

		for _, row := range result.Rows {
			name, _ := row["RDB$INDEX_NAME"].(string)
			table, _ := row["RDB$RELATION_NAME"].(string)
			uniqueFlag := firebird.ToInt64(row["RDB$UNIQUE_FLAG"])
			desc, _ := row["RDB$DESCRIPTION"].(string)
			if desc == "" {
				desc = "-"
			}
			uniqueStr := "NO"
			if uniqueFlag == 1 {
				uniqueStr = "YES"
			}
			sb.WriteString(fmt.Sprintf("%s | %s | %s | %s\n", name, table, uniqueStr, desc))
		}

	case "SEQUENCES":
		result, err := client.Query(ctx, `SELECT
			RDB$GENERATOR_NAME,
			RDB$DESCRIPTION
		FROM RDB$GENERATORS
		WHERE RDB$SYSTEM_FLAG = 0
		ORDER BY RDB$GENERATOR_NAME`)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to list sequences: %v", err)), nil
		}

		count := len(result.Rows)
		sb.WriteString("### Sequences\n\n")
		if count == 0 {
			sb.WriteString("No sequences found.\n")
			return mcp.NewToolResultText(sb.String()), nil
		}

		sb.WriteString(fmt.Sprintf("Count: %d sequences\n\n", count))
		sb.WriteString("NAME | DESCRIPTION\n")
		sb.WriteString("--- | ---\n")

		for _, row := range result.Rows {
			name, _ := row["RDB$GENERATOR_NAME"].(string)
			desc, _ := row["RDB$DESCRIPTION"].(string)
			if desc == "" {
				desc = "-"
			}
			sb.WriteString(fmt.Sprintf("%s | %s\n", name, desc))
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}
