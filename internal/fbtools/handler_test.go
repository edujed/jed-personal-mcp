package fbtools

import (
	"strings"
	"testing"
	"time"

	"github.com/edujed/jed-personal-mcp/internal/firebird"
)

func TestFormatSchema(t *testing.T) {
	schema := &firebird.TableSchema{
		Table: "TEST_TABLE",
		Columns: []firebird.ColumnInfo{
			{
				Name:     "ID",
				TypeNum:  8, // INTEGER
				Size:     4,
				Nullable: 0,
				Default:  nil,
				Position: 1,
			},
			{
				Name:     "NAME",
				TypeNum:  20,  // VARCHAR
				Size:     400, // 100 chars * 4 bytes/char (UTF8)
				Nullable: 1,
				Default:  nil,
				Position: 2,
			},
			{
				Name:     "CREATED_AT",
				TypeNum:  29, // TIMESTAMP
				Size:     0,
				Nullable: 0,
				Default:  "CURRENT_TIMESTAMP",
				Position: 3,
			},
		},
		Indexes: []firebird.IndexInfo{
			{
				Name:    "PK_TEST_TABLE",
				Unique:  1,
				Type:    0,
				Columns: "ID",
			},
		},
		Constraints: []firebird.ConstraintInfo{
			{
				Name: "PK_TEST_TABLE",
				Type: "PRIMARY KEY",
			},
		},
		Triggers: []firebird.TriggerInfo{
			{
				Name:     "TRG_TEST",
				Type:     1,
				Sequence: 0,
			},
		},
	}

	result := formatSchema(schema)
	t.Log(result)

	// Verify key parts are present
	if !strings.Contains(result, "### Table: TEST_TABLE") {
		t.Error("Missing table header")
	}
	if !strings.Contains(result, "ID | INTEGER | - | NO | - | 1") {
		t.Error("Missing ID column with correct values")
	}
	if !strings.Contains(result, "NAME | VARCHAR(100) | - | YES | - | 2") {
		t.Error("Missing NAME column with correct values")
	}
	if !strings.Contains(result, "CREATED_AT | TIMESTAMP | - | NO | CURRENT_TIMESTAMP | 3") {
		t.Error("Missing CREATED_AT column with default value")
	}
	if !strings.Contains(result, "PK_TEST_TABLE | YES | 0 | ID") {
		t.Error("Missing index")
	}
	if !strings.Contains(result, "PK_TEST_TABLE | PRIMARY KEY") {
		t.Error("Missing constraint")
	}
	if !strings.Contains(result, "TRG_TEST | 1 | 0") {
		t.Error("Missing trigger")
	}
}

func TestFormatCell_DateTime(t *testing.T) {
	// Test DATE (no time component)
	date := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	if got := formatCell(date); got != "2026-09-26" {
		t.Errorf("DATE: got %q, want %q", got, "2026-09-26")
	}

	// Test TIMESTAMP (with time component)
	ts := time.Date(2026, 9, 26, 14, 30, 45, 0, time.UTC)
	if got := formatCell(ts); got != "2026-09-26 14:30:45" {
		t.Errorf("TIMESTAMP: got %q, want %q", got, "2026-09-26 14:30:45")
	}

	// Test TIMESTAMP with nanoseconds
	tsNano := time.Date(2026, 9, 26, 14, 30, 45, 123456000, time.UTC)
	if got := formatCell(tsNano); got != "2026-09-26 14:30:45.123456" {
		t.Errorf("TIMESTAMP with ns: got %q, want %q", got, "2026-09-26 14:30:45.123456")
	}

	// Test NULL
	if got := formatCell(nil); got != "NULL" {
		t.Errorf("NULL: got %q, want %q", got, "NULL")
	}

	// Test string with pipe
	if got := formatCell("a|b"); got != `a\|b` {
		t.Errorf("pipe escape: got %q, want %q", got, `a\|b`)
	}
}
