# jed-personal-mcp

A personal MCP (Model Context Protocol) server for local development, written in Go. 

Provides database access to a local Firebird 5 instance.

Is suitable for use with the [Zed Editor](https://zed.dev) in development tasks.

> **Warning:** This tool is intended for local development only. It is not safe for production use.

## Features

- **`firebird_query`** — Execute SQL commands (SELECT, INSERT, UPDATE, CREATE TABLE, CREATE TRIGGER, etc.)
- **`firebird_show_tables`** — List all tables and views in the database
- **`firebird_describe_table`** — Show the full schema of a table or view (columns, indexes, constraints, triggers, generators)
- **`firebird_get_databases`** — List all configured databases
- **`firebird_create_database`** — Create a new Firebird database file (restricted to allowed paths)

## Prerequisites

- [Go](https://go.dev/) 1.21+
- [Firebird 5](https://www.firebirdsql.org/) server running locally (or reachable over the network)

## Setup

### 1. Configure your server and databases

Edit `config.json`:

```json
{
  "server": {
    "host": "127.0.0.1",
    "port": 3050,
    "user": "SYSDBA",
    "password": "masterkey",
    "allowed_paths": [
      "/databases"
    ],
    "databases": [
      {
        "name": "dev",
        "description": "Local development database",
        "path": "/databases/dev.fdb"
      }
    ],
    "default_database": "dev"
  }
}
```

### 2. Build

```bash
# Build for current platform
./scripts/build.sh

# Build for a specific platform
./scripts/build.sh linux      # linux/amd64 + linux/arm64
./scripts/build.sh darwin     # darwin/amd64 + darwin/arm64
./scripts/build.sh windows    # windows/amd64 + windows/arm64

# Build for all platforms
./scripts/build.sh all
```

Binaries are output to `bin/`.

### 3. Register in Zed Editor

Add the MCP server to your Zed settings (`~/.config/zed/settings.json`):

```json
{
  "context_servers": {
    "jed-personal-mcp": {
      "command": "/home/edujed/github/jed-personal-mcp/bin/jed-personal-mcp",
      "enabled": true,
      "env": {
        "FIREBIRD_CONFIG": "/home/edujed/github/jed-personal-mcp/config.json"
      },
      "timeout": 20
    }
  }
}
```

| Field | Description |
|---|---|
| `command` | Absolute path to the built binary |
| `enabled` | Whether the server is active (`true`/`false`) |
| `env.FIREBIRD_CONFIG` | Path to `config.json` (optional — defaults to `./config.json` relative to the working directory) |
| `timeout` | Timeout in seconds for server startup |

After saving, Zed will automatically start the MCP server and make the tools available to the agent.

## Configuration

### `config.json`

| Field | Type | Description |
|---|---|---|
| `server.host` | `string` | Firebird server host |
| `server.port` | `int` | Firebird server port |
| `server.user` | `string` | Server admin username |
| `server.password` | `string` | Server admin password |
| `server.allowed_paths` | `array` | Directories where new databases can be created |
| `server.databases` | `array` | List of database configurations |
| `server.default_database` | `string` | Name of the database to use when none is specified |

### Database entry

| Field | Type | Description |
|---|---|---|
| `name` | `string` | Unique identifier used in tool calls |
| `description` | `string` | Human-readable description (optional) |
| `path` | `string` | Path to the `.fdb` file |

### Configuration file lookup

The server looks for the config file in the following order:

1. **`FIREBIRD_CONFIG` environment variable** — if set, this path is used
2. **`./config.json`** — relative to the current working directory

### Environment variables

| Variable | Description |
|---|---|
| `FIREBIRD_CONFIG` | Absolute path to the config file |

## Tools

### `firebird_query`

Executes a raw SQL statement against the database.

| Argument | Type | Required | Description |
|---|---|---|---|
| `sql` | `string` | Yes | The SQL statement to execute |
| `database` | `string` | No | Database name (defaults to `default_database`) |

### `firebird_show_tables`

Lists all user tables and views.

| Argument | Type | Required | Description |
|---|---|---|---|
| `database` | `string` | No | Database name (defaults to `default_database`) |

### `firebird_describe_table`

Shows the full schema of a table or view, including columns, indexes, constraints, triggers, and generators.

| Argument | Type | Required | Description |
|---|---|---|---|
| `table_name` | `string` | Yes | Table or view name |
| `database` | `string` | No | Database name (defaults to `default_database`) |

### `firebird_get_databases`

Lists all databases defined in `config.json`.

No arguments required.

### `firebird_create_database`

Creates a new Firebird database file with a fixed page size of 4096 bytes. The path must be within one of the `allowed_paths` directories.

| Argument | Type | Required | Description |
|---|---|---|---|
| `path` | `string` | Yes | Full path for the new `.fdb` file |

### `firebird_run_script`

Executes a SQL script (multiple statements) in a single transaction. Statements are separated by semicolons. If any statement fails, the entire transaction is rolled back.

| Argument | Type | Required | Description |
|---|---|---|---|
| `database` | `string` | Yes | Database name (required to avoid ambiguity) |
| `script` | `string` | No | The SQL script to execute (inline) |
| `path` | `string` | No | Path to a `.sql` file to execute |

> **Note:** Either `script` or `path` must be provided.

### `firebird_create_trigger`

Creates a new trigger in the database. Handles the `SET TERM` delimiter automatically.

| Argument | Type | Required | Description |
|---|---|---|---|
| `database` | `string` | Yes | Database name |
| `trigger_name` | `string` | Yes | Name for the new trigger |
| `table_name` | `string` | Yes | Table the trigger will be associated with |
| `trigger_type` | `string` | Yes | Trigger event: `INSERT`, `UPDATE`, or `DELETE` |
| `timing` | `string` | Yes | Trigger timing: `BEFORE` or `AFTER` |
| `body` | `string` | Yes | The trigger body (SQL statements, without `SET TERM` or `BEGIN/END`) |

## Project Structure

```
├── main.go                  # Entry point: loads config, creates server, starts Stdio
├── config.json              # Server and database configuration
├── internal/
│   ├── config/
│   │   └── config.go        # Config loading, validation, and DSN building
│   ├── firebird/
│   │   └── client.go        # Firebird DB client: queries, schema introspection, DDL
│   └── fbtools/
│       └── handler.go       # Firebird MCP tool handlers + registration
├── go.mod
└── README.md
```

### Design

- **`internal/config`** — Loads and validates `config.json`. Exposes DSN builders and path validation.
- **`internal/firebird`** — Data access layer. Wraps `database/sql` with typed schema introspection (tables, columns, indexes, constraints, triggers, generators).
- **`internal/fbtools`** — Firebird-specific MCP tool handlers and registration. Exposes `Register(s, h)` to wire all Firebird tools into the server.
- **`main.go`** — Thin entry point. Wires config → handler → MCP server.

### Extending with new database engines

To add support for another engine (e.g. PostgreSQL):

1. Create `internal/postgres/` — data access layer (mirrors `internal/firebird/`)
2. Create `internal/pgtools/` — tool handlers + `Register()` function (mirrors `internal/fbtools/`)
3. In `main.go`, call `pgtools.Register(s, pgHandler)` alongside `fbtools.Register(s, fbHandler)`

Each engine is fully self-contained under `internal/`, keeping the entry point minimal.

## License

See [LICENSE](LICENSE).
