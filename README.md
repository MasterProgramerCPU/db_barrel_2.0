# DB Barrel 2.0

Visual database schema introspection with project-scoped configs stored in a metadata database.

DB Barrel connects to PostgreSQL, MySQL, MariaDB, and SQLite databases, introspects their schemas, and renders interactive ER diagrams in the browser. Instead of keeping one flat `databases.json` with every target database, the app now points to a metadata database. That metadata database contains a `projects` table, and each row's `barrel_configs` JSON defines one project's databases.

## Features

- Project catalog backed by a metadata database
- One active project at a time, selectable in the UI
- Per-project `barrel_configs` JSON, including optional manual `replication`
- Automatic PostgreSQL replication discovery within the selected project
- Interactive database gallery and ER diagram view
- Add/remove database connections from the selected project directly in the UI
- Embedded frontend assets in a single Go binary
- Docker and Debian packaging support

## Quick Start

### From source

```bash
git clone https://github.com/robotelu/db_barrel_2.0.git
cd db_barrel_2.0

CGO_ENABLED=1 go build -o db_barrel .
cp databases.example.json databases.json
nano databases.json

./db_barrel
# -> http://localhost:8080
```

### From `.deb`

```bash
./build-deb.sh
sudo dpkg -i db-barrel_2.0.0_amd64.deb
sudo nano /etc/db-barrel/databases.json
sudo systemctl start db-barrel

# -> http://localhost:8080
```

### With Docker

```bash
docker build -t db-barrel:local .
docker run --rm -p 30000:30000 db-barrel:local

# -> http://localhost:30000
```

## Top-Level Config

The top-level config still lives in `databases.json`, but it now points to the metadata database instead of listing all target databases directly.

Config locations:

| Method | Path |
|--------|------|
| From source | `./databases.json` |
| `.deb` package | `/etc/db-barrel/databases.json` |
| Docker image | `/config/databases.json` |

Example:

```json
{
  "projectCatalog": {
    "connection": {
      "driver": "postgresql",
      "host": "localhost",
      "port": 5432,
      "user": "postgres",
      "password": "secret",
      "database": "platform",
      "sslMode": "disable"
    },
    "projectsTable": "projects",
    "projectNameColumn": "name",
    "barrelConfigsColumn": "barrel_configs",
    "defaultProject": "Production API"
  }
}
```

Defaults:

- `projectsTable`: `projects`
- `projectNameColumn`: `name`
- `barrelConfigsColumn`: `barrel_configs`

## Metadata Database Schema

By default DB Barrel expects a table like:

```sql
CREATE TABLE projects (
  name text not null,
  barrel_configs jsonb
);
```

Expected columns:

- `name`: the project name shown in the UI
- `barrel_configs`: per-project DB Barrel config JSON

`barrel_configs` may be:

- `NULL`
- `''`
- `'{}'`
- a populated JSON object with databases and optional replication

Empty values are treated as an empty project with zero databases, so you can create the project row first and add databases later from the UI.

## `barrel_configs` JSON Format

Each row's `barrel_configs` contains the same per-project config shape DB Barrel used before.

Example:

```json
{
  "databases": [
    {
      "name": "Main Postgres",
      "driver": "postgresql",
      "host": "db.internal",
      "port": 5432,
      "user": "app",
      "password": "secret",
      "database": "app_db",
      "sslMode": "disable"
    },
    {
      "name": "Analytics",
      "driver": "mysql",
      "host": "mysql.internal",
      "port": 3306,
      "user": "reporting",
      "password": "secret",
      "database": "analytics"
    },
    {
      "name": "Local Cache",
      "driver": "sqlite",
      "path": "/data/cache.db"
    }
  ],
  "replication": [
    {
      "sourceName": "Main Postgres",
      "targetName": "Replica Postgres",
      "type": "streaming"
    }
  ]
}
```

Rules:

- top-level `databases` is optional for empty projects
- top-level `replication` is optional
- do not put `projectCatalog` inside `barrel_configs`

Database fields:

| Field | Required | Notes |
|------|----------|------|
| `name` | yes | Label shown in the UI |
| `driver` | yes | `postgresql`, `mysql`, `mariadb`, or `sqlite` |
| `host` | non-SQLite | Server hostname |
| `port` | no | Defaults depend on database type |
| `user` | no | Username |
| `password` | no | Password |
| `database` | non-SQLite | Database/catalog name |
| `path` | SQLite only | SQLite file path |
| `sslMode` | PostgreSQL only | `disable`, `require`, etc. |
| `params` | no | Extra DSN query parameters |

## Current UI Behavior

On the gallery screen:

- select the active project from the header
- add a database with the floating panel
- drag the panel by its title bar
- collapse the panel into a compact title-bar window
- remove a database by holding on its card for 1 second, then dragging the card onto the trash icon

Add/remove actions update the selected project's `barrel_configs` JSON in the metadata database and then reload that project.

## Replication

For the selected project:

- PostgreSQL streaming/logical replication links are still auto-discovered
- manual `replication` links in `barrel_configs` are still accepted
- the final topology is the merged, deduplicated result

The topology endpoints remain:

- `GET /api/topology`
- `GET /api/topology/report`

## API

Main endpoints used by the frontend:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/projects` | GET | List projects and current project |
| `/api/projects/select` | POST | Switch active project |
| `/api/databases` | GET | List databases for the active project |
| `/api/databases` | POST | Add a database to the active project |
| `/api/databases/{id}` | DELETE | Remove a database from the active project |
| `/api/databases/{id}/schema` | GET | Get schema for one active-project database |
| `/api/topology` | GET | Get replication links for the active project |
| `/api/topology/report` | GET | Get replication diagnostics |
| `/api/reload` | POST | Reload project catalog and active project |

Credentials are never returned by the API.

## Project Structure

```text
db_barrel_2.0/
├── main.go
├── replication.go
├── internal/
│   ├── api/
│   ├── catalog/
│   ├── config/
│   └── driver/
├── web/
│   ├── index.html
│   ├── style.css
│   ├── app.js
│   └── svg/
├── packaging/
├── testdata/
└── databases.example.json
```

## Debian Package

Installed paths:

| Path | Purpose |
|------|---------|
| `/usr/bin/db-barrel` | Application binary |
| `/etc/db-barrel/databases.json` | Metadata-database config |
| `/lib/systemd/system/db-barrel.service` | systemd service unit |
| `/var/lib/db-barrel/` | Data directory |

Useful commands:

```bash
sudo systemctl start db-barrel
sudo systemctl stop db-barrel
sudo systemctl restart db-barrel
sudo systemctl status db-barrel
journalctl -u db-barrel -f
```

## Development

Prerequisites:

- Go 1.21+
- GCC for CGO / SQLite
- `dpkg-deb` for `.deb` builds

Build:

```bash
CGO_ENABLED=1 go build -o db_barrel .
```

Test:

```bash
GOCACHE=/tmp/go-build go test ./...
```

Because the frontend is embedded with `go:embed`, rebuild the binary after editing files in `web/`.

## Security Notes

- The top-level config may contain metadata-database credentials
- Per-project `barrel_configs` may contain target database credentials
- `/api/databases` only returns display metadata and status, never DSNs

## License

Provided as-is. See the repository for license details.
