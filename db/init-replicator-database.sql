-- ========================================
-- PART 2: Run on REPLICA database
-- ========================================

-- Create Users table in replica database (same structure, no triggers)
CREATE TABLE IF NOT EXISTS users (
    user_id INTEGER PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    email VARCHAR(100),
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ
);



-- Note: No change tracking needed on replica

-- ========================================
-- Sample data (run on PRIMARY only)
-- ========================================

-- Uncomment to insert sample data:
-- INSERT INTO users (name, email) VALUES 
--     ('Alice Johnson', 'alice@example.com'),
--     ('Bob Smith', 'bob@example.com'),
--     ('Charlie Brown', 'charlie@example.com');

-- ========================================
-- Verification queries
-- ========================================

-- Check users table
-- SELECT * FROM users;

-- Check change tracking (PRIMARY only)
-- SELECT * FROM user_changes WHERE processed = false ORDER BY change_id;

-- Mark changes as processed (used by replicator)
-- UPDATE user_changes SET processed = true WHERE change_id <= ?;