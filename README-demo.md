# SQL Server CDC Replication Demo

This demo demonstrates real-time database replication using SQL Server Change Data Capture (CDC) and a Go service.

## Prerequisites

1. **SQL Server** installed locally (Developer, Standard, or Enterprise edition)
2. **SQL Server Agent** must be running (CDC requires it)
3. **Go 1.21+** installed
4. **SA credentials** or a user with sysadmin/db_owner permissions

## Setup Instructions

### 1. Enable SQL Server Agent

```bash
# On Windows, ensure SQL Server Agent service is running
# Check Services -> SQL Server Agent (MSSQLSERVER)
```

### 2. Initialize Databases

Run the initialization script to create `DemoDB` (primary) and `ReplicaDB` (secondary):

```bash
sqlcmd -S localhost -U sa -P Your_password123 -i db/init.sql
```

Or using SQL Server Management Studio (SSMS), open and execute `db/init.sql`.

### 3. Install Go Dependencies

```bash
go mod download
```

### 4. Configure Connection Strings

Edit `cmd/replicator/main.go` if your SQL Server uses different credentials:

```go
config := Config{
    PrimaryConnStr:   "server=localhost;user id=sa;password=YOUR_PASSWORD;database=DemoDB",
    SecondaryConnStr: "server=localhost;user id=sa;password=YOUR_PASSWORD;database=ReplicaDB",
    PollInterval:     5 * time.Second,
}
```

### 5. Run the Replicator

```bash
cd cmd/replicator
go run main.go
```

The service will:
- Poll CDC changes every 5 seconds
- Apply changes to ReplicaDB
- Expose a status API on port 8080

## Testing the Replication

### Insert Records on Primary

```bash
sqlcmd -S localhost -U sa -P Your_password123 -d DemoDB -Q "INSERT INTO Users (Name, Email) VALUES ('Alice', 'alice@example.com');"
sqlcmd -S localhost -U sa -P Your_password123 -d DemoDB -Q "INSERT INTO Users (Name, Email) VALUES ('Bob', 'bob@example.com');"
```

### Verify Replication

Wait ~5 seconds, then check ReplicaDB:

```bash
sqlcmd -S localhost -U sa -P Your_password123 -d ReplicaDB -Q "SELECT * FROM Users;"
```

### Check Replication Status

```bash
curl http://localhost:8080/status
```

Response:
```json
{
  "last_lsn": "00000027000000680001",
  "last_sync_time": "2025-12-04T10:30:15Z",
  "records_replicated": 2,
  "error_count": 0,
  "is_running": true
}
```

## Architecture

- **DemoDB**: Primary database with CDC enabled on `Users` table
- **ReplicaDB**: Secondary database receiving replicated changes
- **Go Replicator**: Polls CDC functions (`cdc.fn_cdc_get_all_changes_dbo_Users`) and applies changes to ReplicaDB
- **REST API**: Exposes `/status` and `/health` endpoints

## CDC Operations

- **Operation 1**: Delete
- **Operation 2**: Insert
- **Operation 3**: Before update (old values)
- **Operation 4**: After update (new values)

## Troubleshooting

### CDC Not Working
- Ensure SQL Server Agent is running
- Verify CDC is enabled: `SELECT is_cdc_enabled FROM sys.databases WHERE name = 'DemoDB';`
- Check capture instance: `SELECT * FROM cdc.change_tables;`

### Connection Errors
- Verify SQL Server is listening on TCP port 1433
- Check firewall rules allow localhost connections
- Confirm SA password is correct

### No Changes Replicated
- Insert a test record and wait for the poll interval (5 seconds)
- Check logs in the replicator terminal
- Query CDC directly: `SELECT * FROM cdc.dbo_Users_CT;`

## Cleanup

To remove CDC and databases:

```sql
USE DemoDB;
EXEC sys.sp_cdc_disable_table @source_schema = N'dbo', @source_name = N'Users', @capture_instance = 'dbo_Users';
EXEC sys.sp_cdc_disable_db;
DROP DATABASE DemoDB;
DROP DATABASE ReplicaDB;
```
