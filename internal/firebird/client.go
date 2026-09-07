// Package firebird provides database access to Firebird instances.
package firebird

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/edujed/jed-personal-mcp/internal/config"
	_ "github.com/nakagami/firebirdsql"
)

// Client wraps a Firebird database connection.
type Client struct {
	db         *sql.DB
	user       string
	password   string
	serverAddr string // host:port
}

// NewClient opens a connection to the specified database.
func NewClient(server *config.ServerConfig, dbPath string) (*Client, error) {
	db, err := sql.Open("firebirdsql", server.DSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Firebird: %w", err)
	}
	return &Client{
		db:         db,
		user:       server.User,
		password:   server.Password,
		serverAddr: fmt.Sprintf("%s:%d", server.Host, server.Port),
	}, nil
}

// Close closes the underlying database connection.
func (c *Client) Close() error {
	return c.db.Close()
}

// QueryResult represents a single row as a map of column names to values.
type QueryResult struct {
	Columns []string
	Rows    []map[string]any
}

// Query executes a SELECT statement and returns the results.
func (c *Client) Query(ctx context.Context, query string, args ...any) (*QueryResult, error) {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query error: %w", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	result := &QueryResult{Columns: columns}

	for rows.Next() {
		scanArgs := make([]any, len(columns))
		values := make([]any, len(columns))
		for i := range values {
			scanArgs[i] = &values[i]
		}

		if err := rows.Scan(scanArgs...); err != nil {
			continue
		}

		row := make(map[string]any, len(columns))
		for i, col := range columns {
			if b, ok := values[i].([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = values[i]
			}
		}
		result.Rows = append(result.Rows, row)
	}

	return result, nil
}

// Exec executes a statement that doesn't return rows (INSERT, UPDATE, DDL, etc.).
func (c *Client) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	res, err := c.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("exec error: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Execute runs a statement, trying Query first (for SELECT) and falling back to Exec.
// Returns the query result if rows were returned, or a success message otherwise.
func (c *Client) Execute(ctx context.Context, query string) (*QueryResult, string, error) {
	result, err := c.Query(ctx, query)
	if err == nil {
		return result, "", nil
	}

	// If Query failed (non-SELECT statement), try Exec
	_, execErr := c.Exec(ctx, query)
	if execErr != nil {
		return nil, "", fmt.Errorf("SQL execution error: %w", execErr)
	}
	return nil, "Command executed successfully (DML/DDL applied).", nil
}

// RunScript executes multiple SQL statements in a single transaction.
// Statements are separated by semicolons. If any statement fails, the
// entire transaction is rolled back.
// Returns the number of statements executed successfully.
func (c *Client) RunScript(ctx context.Context, script string) (int, error) {
	statements := splitStatements(script)
	if len(statements) == 0 {
		return 0, fmt.Errorf("no statements found in script")
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}

	executed := 0
	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			tx.Rollback()
			return executed, fmt.Errorf("statement %d failed: %w", executed+1, err)
		}
		executed++
	}

	if err := tx.Commit(); err != nil {
		return executed, fmt.Errorf("failed to commit transaction: %w", err)
	}
	return executed, nil
}

// splitStatements splits a SQL script into individual statements.
// It handles semicolons outside of strings and comments.
func splitStatements(script string) []string {
	var statements []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	inLineComment := false
	inBlockComment := false

	for i := 0; i < len(script); i++ {
		ch := script[i]

		// Handle block comments
		if inBlockComment {
			if ch == '*' && i+1 < len(script) && script[i+1] == '/' {
				inBlockComment = false
				i++ // skip '/'
			} else {
				continue
			}
		} else if inLineComment {
			if ch == '\n' {
				inLineComment = false
			}
			continue
		} else if inSingleQuote {
			if ch == '\'' && i+1 < len(script) && script[i+1] == '\'' {
				current.WriteByte(ch)
				i++ // skip escaped quote
			} else if ch == '\'' {
				inSingleQuote = false
			}
			current.WriteByte(ch)
			continue
		} else if inDoubleQuote {
			if ch == '"' {
				inDoubleQuote = false
			}
			current.WriteByte(ch)
			continue
		}

		// Check for comment starts
		if ch == '-' && i+1 < len(script) && script[i+1] == '-' {
			inLineComment = true
			i++ // skip second '-'
			continue
		}
		if ch == '/' && i+1 < len(script) && script[i+1] == '*' {
			inBlockComment = true
			i++ // skip '*'
			continue
		}

		// Check for quote starts
		if ch == '\'' {
			inSingleQuote = true
			current.WriteByte(ch)
			continue
		}
		if ch == '"' {
			inDoubleQuote = true
			current.WriteByte(ch)
			continue
		}

		// Check for statement end
		if ch == ';' {
			stmt := strings.TrimSpace(current.String())
			if stmt != "" {
				statements = append(statements, stmt)
			}
			current.Reset()
			continue
		}

		current.WriteByte(ch)
	}

	// Handle any remaining statement without trailing semicolon
	if stmt := strings.TrimSpace(current.String()); stmt != "" {
		statements = append(statements, stmt)
	}

	return statements
}

// --- Schema Introspection ---

// TableInfo represents a table or view in the database.
type TableInfo struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Comment string `json:"comment,omitempty"`
}

// ListTables returns all user tables and views.
func (c *Client) ListTables(ctx context.Context) ([]TableInfo, error) {
	query := `
		SELECT
			R.RDB$RELATION_NAME AS NAME,
			CASE R.RDB$VIEW_BLR
				WHEN NULL THEN 'TABLE'
				ELSE 'VIEW'
			END AS TYPE
		FROM RDB$RELATIONS R
		WHERE R.RDB$SYSTEM_FLAG = 0
		ORDER BY R.RDB$RELATION_NAME
	`
	result, err := c.Query(ctx, query)
	if err != nil {
		return nil, err
	}

	tables := make([]TableInfo, 0, len(result.Rows))
	for _, row := range result.Rows {
		tables = append(tables, TableInfo{
			Name:    getString(row, "NAME"),
			Type:    getString(row, "TYPE"),
			Comment: getString(row, "COMMENT"),
		})
	}
	return tables, nil
}

// ColumnInfo represents a column in a table.
type ColumnInfo struct {
	Name      string `json:"name"`
	TypeNum   any    `json:"type_num"`
	Size      any    `json:"size"`
	Precision any    `json:"precision"`
	Scale     any    `json:"scale"`
	Nullable  any    `json:"nullable"`
	Default   any    `json:"default"`
	Position  any    `json:"position"`
}

// IndexInfo represents an index on a table.
type IndexInfo struct {
	Name    string `json:"name"`
	Unique  any    `json:"unique"`
	Type    any    `json:"type"`
	Columns string `json:"columns"`
}

// ConstraintInfo represents a constraint on a table.
type ConstraintInfo struct {
	Name              string `json:"name"`
	Type              string `json:"type"`
	Deferrable        any    `json:"deferrable,omitempty"`
	InitiallyDeferred any    `json:"initially_deferred,omitempty"`
}

// TriggerInfo represents a trigger on a table.
type TriggerInfo struct {
	Name     string `json:"name"`
	Type     any    `json:"type"`
	Sequence any    `json:"sequence"`
}

// TableSchema holds the full schema of a table or view.
type TableSchema struct {
	Table       string            `json:"table"`
	Columns     []ColumnInfo      `json:"columns"`
	Indexes     []IndexInfo       `json:"indexes"`
	Constraints []ConstraintInfo  `json:"constraints"`
	Triggers    []TriggerInfo     `json:"triggers"`
	Generators  []string          `json:"generators"`
	Comments    map[string]string `json:"comments,omitempty"`
}

// DescribeTable returns the full schema of a table or view.
func (c *Client) DescribeTable(ctx context.Context, tableName string) (*TableSchema, error) {
	schema := &TableSchema{Table: tableName}

	// Columns (join RDB$FIELDS to get type info)
	colsQuery := `
			SELECT
				F.RDB$FIELD_NAME        AS NAME,
				T.RDB$FIELD_TYPE        AS TYPE_NUM,
				T.RDB$FIELD_LENGTH      AS SIZE,
				T.RDB$FIELD_PRECISION   AS PREC,
				T.RDB$FIELD_SCALE       AS SCALE,
				F.RDB$NULL_FLAG         AS NULL_FLAG,
				F.RDB$DEFAULT_VALUE     AS DEFVAL,
				F.RDB$FIELD_POSITION    AS POS
			FROM RDB$RELATION_FIELDS F
			LEFT JOIN RDB$FIELDS T
				ON T.RDB$FIELD_NAME = F.RDB$FIELD_SOURCE
			WHERE F.RDB$RELATION_NAME = ?
			ORDER BY F.RDB$FIELD_POSITION
		`
	colsResult, err := c.Query(ctx, colsQuery, tableName)
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}
	for _, row := range colsResult.Rows {
		schema.Columns = append(schema.Columns, ColumnInfo{
			Name:      getString(row, "NAME"),
			TypeNum:   row["TYPE_NUM"],
			Size:      row["SIZE"],
			Precision: row["PREC"],
			Scale:     row["SCALE"],
			Nullable:  row["NULL_FLAG"],
			Default:   row["DEFVAL"],
			Position:  row["POS"],
		})
	}

	// Comments (RDB$OBJECT_COMMENTS may not exist in all Firebird versions)
	if comments, err := c.getComments(ctx, tableName); err == nil {
		schema.Comments = comments
	}

	// Indexes
	idxQuery := `
			SELECT
				I.RDB$INDEX_NAME           AS NAME,
				I.RDB$UNIQUE_FLAG          AS IS_UNIQUE,
				I.RDB$INDEX_TYPE           AS TYPE,
				S.RDB$FIELD_NAME           AS COLUMN_NAME,
				S.RDB$FIELD_POSITION       AS COL_POS
			FROM RDB$INDICES I
			JOIN RDB$INDEX_SEGMENTS S
				ON S.RDB$INDEX_NAME = I.RDB$INDEX_NAME
			WHERE I.RDB$RELATION_NAME = ?
			ORDER BY I.RDB$INDEX_NAME, S.RDB$FIELD_POSITION
		`
	idxResult, err := c.Query(ctx, idxQuery, tableName)
	if err != nil {
		return nil, fmt.Errorf("failed to get indexes: %w", err)
	}

	// Group columns by index name
	type idxKey struct {
		name    string
		unique  any
		idxType any
	}
	idxMap := make(map[idxKey]*IndexInfo)
	idxOrder := make([]idxKey, 0)
	for _, row := range idxResult.Rows {
		key := idxKey{
			name:    getString(row, "NAME"),
			unique:  row["IS_UNIQUE"],
			idxType: row["TYPE"],
		}
		if idx, ok := idxMap[key]; ok {
			idx.Columns += ", " + getString(row, "COLUMN_NAME")
		} else {
			idxMap[key] = &IndexInfo{
				Name:    key.name,
				Unique:  key.unique,
				Type:    key.idxType,
				Columns: getString(row, "COLUMN_NAME"),
			}
			idxOrder = append(idxOrder, key)
		}
	}
	for _, key := range idxOrder {
		schema.Indexes = append(schema.Indexes, *idxMap[key])
	}

	// Constraints
	conQuery := `
			SELECT
				C.RDB$CONSTRAINT_NAME        AS NAME,
				C.RDB$CONSTRAINT_TYPE        AS TYPE,
				C.RDB$DEFERRABLE             AS DEFERRABLE,
				C.RDB$INITIALLY_DEFERRED     AS INIT_DEFERRED
			FROM RDB$RELATION_CONSTRAINTS C
			WHERE C.RDB$RELATION_NAME = ?
			ORDER BY C.RDB$CONSTRAINT_NAME
		`
	conResult, err := c.Query(ctx, conQuery, tableName)
	if err != nil {
		return nil, fmt.Errorf("failed to get constraints: %w", err)
	}
	for _, row := range conResult.Rows {
		schema.Constraints = append(schema.Constraints, ConstraintInfo{
			Name:              getString(row, "NAME"),
			Type:              getString(row, "TYPE"),
			Deferrable:        row["DEFERRABLE"],
			InitiallyDeferred: row["INIT_DEFERRED"],
		})
	}

	// Triggers
	trgQuery := `
		SELECT
			T.RDB$TRIGGER_NAME     AS NAME,
			T.RDB$TRIGGER_TYPE     AS TYPE,
			T.RDB$TRIGGER_SEQUENCE AS SEQUENCE
		FROM RDB$TRIGGERS T
		WHERE T.RDB$RELATION_NAME = ?
		ORDER BY T.RDB$TRIGGER_SEQUENCE
	`
	trgResult, err := c.Query(ctx, trgQuery, tableName)
	if err != nil {
		return nil, fmt.Errorf("failed to get triggers: %w", err)
	}
	for _, row := range trgResult.Rows {
		schema.Triggers = append(schema.Triggers, TriggerInfo{
			Name:     getString(row, "NAME"),
			Type:     row["TYPE"],
			Sequence: row["SEQUENCE"],
		})
	}

	// Generators (Firebird 5: generators are not directly linked to tables)
	// We can't easily filter generators by table, so we'll skip this for now
	// or return all generators if needed

	return schema, nil
}

// CreateTrigger creates a new trigger in the database.
// It handles the SET TERM delimiter automatically.
func (c *Client) CreateTrigger(ctx context.Context, triggerName, tableName, triggerType, timing, body string) error {
	// Validate trigger type
	validTypes := map[string]bool{
		"INSERT": true, "UPDATE": true, "DELETE": true,
	}
	if !validTypes[triggerType] {
		return fmt.Errorf("invalid trigger type: %s. Must be INSERT, UPDATE, or DELETE", triggerType)
	}

	// Validate timing
	validTimings := map[string]bool{
		"BEFORE": true, "AFTER": true,
	}
	if !validTimings[timing] {
		return fmt.Errorf("invalid timing: %s. Must be BEFORE or AFTER", timing)
	}

	// Build the trigger SQL with SET TERM
	triggerSQL := fmt.Sprintf(
		"SET TERM ^ ;\nCREATE TRIGGER \"%s\" FOR \"%s\"\n%s %s\nAS\nBEGIN\n%s\nEND ^\nSET TERM ; ^",
		triggerName, tableName, timing, triggerType, body,
	)

	_, err := c.Exec(ctx, triggerSQL)
	return err
}

// CreateDatabase creates a new Firebird database file.
// It uses the firebirdsql_createdb driver, which creates the database on connect.
// Note: the driver uses a fixed page size of 4096 bytes.
func CreateDatabase(ctx context.Context, server *config.ServerConfig, path string) error {
	dsn := server.DSN(path)

	db, err := sql.Open("firebirdsql_createdb", dsn)
	if err != nil {
		return fmt.Errorf("failed to open createdb connection: %w", err)
	}
	defer db.Close()

	// The database is created on connect; ping to trigger the actual creation.
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to create database: %w", err)
	}
	return nil
}

// getComments fetches table and column comments if RDB$OBJECT_COMMENTS exists.
func (c *Client) getComments(ctx context.Context, tableName string) (map[string]string, error) {
	// Check if RDB$OBJECT_COMMENTS exists
	checkQuery := `SELECT COUNT(*) FROM RDB$RELATIONS WHERE RDB$RELATION_NAME = 'RDB$OBJECT_COMMENTS'`
	checkResult, err := c.Query(ctx, checkQuery)
	if err != nil {
		return nil, err
	}
	if len(checkResult.Rows) == 0 {
		return nil, nil
	}
	countVal, ok := checkResult.Rows[0]["COUNT"].(int64)
	if !ok || countVal == 0 {
		return nil, nil
	}

	// Fetch comments
	commentQuery := `
		SELECT
			RDB$RELATION_NAME AS REL_NAME,
			RDB$FIELD_NAME    AS FIELD_NAME,
			RDB$OBJECT_COMMENT AS COMMENT
		FROM RDB$OBJECT_COMMENTS
		WHERE RDB$RELATION_NAME = ?
	`
	result, err := c.Query(ctx, commentQuery, tableName)
	if err != nil {
		return nil, err
	}

	comments := make(map[string]string)
	for _, row := range result.Rows {
		fieldName := getString(row, "FIELD_NAME")
		comment := getString(row, "COMMENT")
		if fieldName == "" {
			comments["table"] = comment
		} else {
			comments[fieldName] = comment
		}
	}
	return comments, nil
}

// getString safely extracts a string value from a row map.
func getString(row map[string]any, key string) string {
	if v, ok := row[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// IsValidIdentifier checks if the name is a safe SQL identifier.
func IsValidIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		if i == 0 {
			if !isLetter(c) {
				return false
			}
		} else {
			if !isLetter(c) && !isDigit(c) && c != '_' {
				return false
			}
		}
	}
	return true
}

func isLetter(c rune) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func isDigit(c rune) bool {
	return c >= '0' && c <= '9'
}
