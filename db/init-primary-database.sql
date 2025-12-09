-- PostgreSQL setup script for database replication demo
-- Run this on BOTH databases (primary and replica) from .env file

-- Primary DB: postgres.yymdlxjjtfzfoaqgcehf@aws-1-ap-southeast-1.pooler.supabase.com:6543/postgres
-- Replica DB: postgres.mnqdnufgoryxksctxzme@aws-1-ap-southeast-1.pooler.supabase.com:6543/postgres

-- ========================================
-- PART 1: Run on PRIMARY database
-- ========================================

-- Create Users table in primary database
CREATE TABLE IF NOT EXISTS users (
    user_id SERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    email VARCHAR(100),
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

-- Create trigger to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Create a change tracking table for manual CDC
CREATE TABLE IF NOT EXISTS user_changes (
    change_id SERIAL PRIMARY KEY,
    operation VARCHAR(10) NOT NULL,  -- 'INSERT', 'UPDATE', 'DELETE'
    user_id INTEGER,
    name VARCHAR(100),
    email VARCHAR(100),
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ,
    change_timestamp TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    processed BOOLEAN DEFAULT FALSE
);

-- Create triggers to track changes
CREATE OR REPLACE FUNCTION track_user_changes()
RETURNS TRIGGER AS $$
BEGIN
    IF (TG_OP = 'DELETE') THEN
        INSERT INTO user_changes (operation, user_id, name, email, created_at, updated_at)
        VALUES ('DELETE', OLD.user_id, OLD.name, OLD.email, OLD.created_at, OLD.updated_at);
        RETURN OLD;
    ELSIF (TG_OP = 'UPDATE') THEN
        INSERT INTO user_changes (operation, user_id, name, email, created_at, updated_at)
        VALUES ('UPDATE', NEW.user_id, NEW.name, NEW.email, NEW.created_at, NEW.updated_at);
        RETURN NEW;
    ELSIF (TG_OP = 'INSERT') THEN
        INSERT INTO user_changes (operation, user_id, name, email, created_at, updated_at)
        VALUES ('INSERT', NEW.user_id, NEW.name, NEW.email, NEW.created_at, NEW.updated_at);
        RETURN NEW;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER users_change_trigger
    AFTER INSERT OR UPDATE OR DELETE ON users
    FOR EACH ROW
    EXECUTE FUNCTION track_user_changes();


