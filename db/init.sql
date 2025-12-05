-- Run this on your local SQL Server instance to set up primary and replica databases

-- Create primary database
IF NOT EXISTS (SELECT * FROM sys.databases WHERE name = 'DemoDB')
BEGIN
    CREATE DATABASE DemoDB;
END
GO

USE DemoDB;
GO

-- Enable CDC at database level (requires SQL Server Agent running)
EXEC sys.sp_cdc_enable_db;
GO

-- Create sample table
IF NOT EXISTS (SELECT * FROM sys.tables WHERE name = 'Users')
BEGIN
    CREATE TABLE Users (
        UserId INT PRIMARY KEY IDENTITY(1,1),
        Name NVARCHAR(100) NOT NULL,
        Email NVARCHAR(100),
        CreatedAt DATETIME2 DEFAULT GETDATE(),
        UpdatedAt DATETIME2 DEFAULT GETDATE()
    );
END
GO

-- Enable CDC on the Users table
EXEC sys.sp_cdc_enable_table
    @source_schema = N'dbo',
    @source_name = N'Users',
    @role_name = NULL,
    @supports_net_changes = 1;
GO

-- Create replica database
IF NOT EXISTS (SELECT * FROM sys.databases WHERE name = 'ReplicaDB')
BEGIN
    CREATE DATABASE ReplicaDB;
END
GO

USE ReplicaDB;
GO

-- Create matching table in replica
IF NOT EXISTS (SELECT * FROM sys.tables WHERE name = 'Users')
BEGIN
    CREATE TABLE Users (
        UserId INT PRIMARY KEY,
        Name NVARCHAR(100) NOT NULL,
        Email NVARCHAR(100),
        CreatedAt DATETIME2,
        UpdatedAt DATETIME2
    );
END
GO

PRINT 'Setup complete. DemoDB has CDC enabled on Users table.';
PRINT 'ReplicaDB has Users table ready for replication.';
