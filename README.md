# PostgreSQL Database Replication Demo

This demo demonstrates real-time database replication using PostgreSQL trigger-based change tracking and a Go service.

## Prerequisites

1. **Two PostgreSQL databases** (e.g., Supabase instances or local PostgreSQL)
2. **Go 1.21+** installed
3. **Database credentials** in `.env` file

## Architecture

- **Primary Database**: Tracks changes using triggers that write to `user_changes` table
- **Replica Database**: Receives replicated changes from the Go replicator service
- **Go Replicator**: Polls `user_changes` table and applies changes to replica
- **REST API**: Exposes `/status` and `/health` endpoints on port 8080

## Setup Instructions

### 1. Configure Environment Variables

### 2. Initialize Primary Database

Connect to your **primary** database and run the first part of `db/init.sql`:

```bash

Or using Supabase SQL Editor, copy and paste the **PART 1** section from `db/init.sql`.

This creates:
- `users` table
- `user_changes` table for change tracking
- Triggers to automatically track INSERT/UPDATE/DELETE operations

### 3. Initialize Replica Database

Connect to your **replica** database and run the **PART 2** section from `db/init.sql`:

```bash

This creates:
- `users` table (no triggers, just receives data)

### 4. Install Go Dependencies

```bash
go mod download
```

### 5. Run the Replicator

```bash
cd cmd/replicator
go run main.go
```

The service will:
- Poll `user_changes` table every 5 seconds
- Apply INSERT/UPDATE/DELETE operations to the replica
- Mark processed changes
- Expose status API on http://localhost:8080

## Testing the Replication

### Insert Records on Primary

Connect to your primary database:

```sql
-- Insert test users
INSERT INTO users (name, email) VALUES 
    ('Alice Johnson', 'alice@example.com'),
    ('Bob Smith', 'bob@example.com'),
    ('Charlie Brown', 'charlie@example.com');
```

### Verify Change Tracking

Check that changes were captured:

```sql
-- On primary database
SELECT * FROM user_changes WHERE processed = false;
```

### Wait for Replication

Wait ~5 seconds for the replicator to process changes, then check the replica:

```sql
-- On replica database
SELECT * FROM users ORDER BY user_id;
```

### Check Replication Status

```bash
curl http://localhost:8080/status
```

Response:
```json
{
  "last_lsn": "42",
  "last_sync_time": "2025-12-09T10:30:15Z",
  "records_replicated": 3,
  "error_count": 0,
  "is_running": true
}
```

### Test Updates and Deletes

```sql
-- On primary database

-- Update a record
UPDATE users SET email = 'alice.j@example.com' WHERE name = 'Alice Johnson';

-- Delete a record
DELETE FROM users WHERE name = 'Charlie Brown';
```

Wait 5 seconds and verify changes replicated to the replica database.

## How It Works

1. **Change Capture**: PostgreSQL triggers on the `users` table automatically insert change records into `user_changes` whenever data is modified
2. **Polling**: The Go replicator polls `user_changes` every 5 seconds for unprocessed changes
3. **Replication**: Changes are applied to the replica database using INSERT...ON CONFLICT for upserts
4. **Acknowledgment**: Successfully replicated changes are marked as `processed = true`

## API Endpoints

### GET /status
Returns replication status including:
- Last processed change ID (LSN)
- Last sync timestamp
- Total records replicated
- Error count and last error message
- Running status

### GET /health
Simple health check endpoint

## Troubleshooting

### No Changes Replicated
- Check that triggers are created on primary: `SELECT * FROM pg_trigger WHERE tgname LIKE '%user%';`
- Verify changes are being captured: `SELECT * FROM user_changes;`
- Check replicator logs for errors

### Connection Errors
- Verify `.env` file paths and credentials
- Check Supabase connection pooler is accessible
- Ensure SSL mode is appropriate (`sslmode=disable` for pooler, `sslmode=require` for direct)

### Trigger Not Working
- Ensure you have appropriate permissions
- Check PostgreSQL logs for trigger errors
- Verify function `track_user_changes()` exists

## Cleanup

To remove tables:

```sql
-- On primary
DROP TRIGGER IF EXISTS users_change_trigger ON users;
DROP TRIGGER IF EXISTS update_users_updated_at ON users;
DROP TABLE IF EXISTS user_changes;
DROP TABLE IF EXISTS users;
DROP FUNCTION IF EXISTS track_user_changes();
DROP FUNCTION IF EXISTS update_updated_at_column();

-- On replica
DROP TABLE IF EXISTS users;
```
