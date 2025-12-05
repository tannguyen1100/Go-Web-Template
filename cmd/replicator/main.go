package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	_ "github.com/denisenkom/go-mssqldb"
	"github.com/gorilla/mux"
)

type Config struct {
	PrimaryConnStr   string
	SecondaryConnStr string
	PollInterval     time.Duration
}

type ReplicationStatus struct {
	LastLSN        string    `json:"last_lsn"`
	LastSyncTime   time.Time `json:"last_sync_time"`
	RecordsReplied int64     `json:"records_replicated"`
	ErrorCount     int64     `json:"error_count"`
	LastError      string    `json:"last_error,omitempty"`
	IsRunning      bool      `json:"is_running"`
}

type Replicator struct {
	primaryDB   *sql.DB
	secondaryDB *sql.DB
	config      Config
	status      ReplicationStatus
	statusMu    sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
}

func main() {
	config := Config{
		PrimaryConnStr:   "sqlserver://newt:sqlserver@localhost:135?database=DemoDB&connection+timeout=30",
		SecondaryConnStr: "sqlserver://newt:sqlserver@localhost:135?database=ReplicaDB&connection+timeout=30",
		PollInterval:     5 * time.Second,
	}

	replicator, err := NewReplicator(config)
	if err != nil {
		log.Fatalf("Failed to create replicator: %v", err)
	}
	defer replicator.Close()

	// Start replication in background
	go replicator.Start()

	// Start HTTP server for status endpoint
	router := mux.NewRouter()
	router.HandleFunc("/status", replicator.HandleStatus).Methods("GET")
	router.HandleFunc("/health", replicator.HandleHealth).Methods("GET")

	srv := &http.Server{
		Addr:    ":8080",
		Handler: router,
	}

	go func() {
		log.Println("Starting HTTP server on :8080")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down...")
	replicator.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}

func NewReplicator(config Config) (*Replicator, error) {
	ctx, cancel := context.WithCancel(context.Background())

	primaryDB, err := sql.Open("sqlserver", config.PrimaryConnStr)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to connect to primary: %w", err)
	}

	secondaryDB, err := sql.Open("sqlserver", config.SecondaryConnStr)
	if err != nil {
		primaryDB.Close()
		cancel()
		return nil, fmt.Errorf("failed to connect to secondary: %w", err)
	}

	// Test connections
	if err := primaryDB.Ping(); err != nil {
		primaryDB.Close()
		secondaryDB.Close()
		cancel()
		return nil, fmt.Errorf("primary DB ping failed: %w", err)
	}

	if err := secondaryDB.Ping(); err != nil {
		primaryDB.Close()
		secondaryDB.Close()
		cancel()
		return nil, fmt.Errorf("secondary DB ping failed: %w", err)
	}

	log.Println("Connected to primary and secondary databases")

	return &Replicator{
		primaryDB:   primaryDB,
		secondaryDB: secondaryDB,
		config:      config,
		ctx:         ctx,
		cancel:      cancel,
		status: ReplicationStatus{
			IsRunning: false,
		},
	}, nil
}

func (r *Replicator) Start() {
	r.statusMu.Lock()
	r.status.IsRunning = true
	r.statusMu.Unlock()

	log.Println("Replication started")
	ticker := time.NewTicker(r.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			log.Println("Replication stopped")
			return
		case <-ticker.C:
			if err := r.pollAndReplicate(); err != nil {
				log.Printf("Replication error: %v", err)
				r.statusMu.Lock()
				r.status.ErrorCount++
				r.status.LastError = err.Error()
				r.statusMu.Unlock()
			}
		}
	}
}

func (r *Replicator) Stop() {
	r.statusMu.Lock()
	r.status.IsRunning = false
	r.statusMu.Unlock()
	r.cancel()
}

func (r *Replicator) Close() {
	if r.primaryDB != nil {
		r.primaryDB.Close()
	}
	if r.secondaryDB != nil {
		r.secondaryDB.Close()
	}
}

func (r *Replicator) pollAndReplicate() error {
	// Query CDC changes for Users table
	// CDC function: cdc.fn_cdc_get_all_changes_<capture_instance>
	// Default capture instance is dbo_Users
	query := `
		DECLARE @from_lsn binary(10), @to_lsn binary(10);
		SET @from_lsn = sys.fn_cdc_get_min_lsn('dbo_Users');
		SET @to_lsn = sys.fn_cdc_get_max_lsn();
		
		SELECT 
			__$operation as Operation,
			__$start_lsn as LSN,
			UserId,
			Name,
			Email,
			CreatedAt,
			UpdatedAt
		FROM cdc.fn_cdc_get_all_changes_dbo_Users(@from_lsn, @to_lsn, 'all')
		ORDER BY __$start_lsn;
	`

	rows, err := r.primaryDB.Query(query)
	if err != nil {
		return fmt.Errorf("CDC query failed: %w", err)
	}
	defer rows.Close()

	changeCount := 0
	var lastLSN []byte

	for rows.Next() {
		var operation int
		var lsn []byte
		var userId sql.NullInt32
		var name, email sql.NullString
		var createdAt, updatedAt sql.NullTime

		if err := rows.Scan(&operation, &lsn, &userId, &name, &email, &createdAt, &updatedAt); err != nil {
			return fmt.Errorf("scan error: %w", err)
		}

		lastLSN = lsn

		// Operation codes: 1=delete, 2=insert, 3=before update, 4=after update
		switch operation {
		case 2, 4: // Insert or Update
			if err := r.upsertRecord(userId.Int32, name.String, email.String, createdAt.Time, updatedAt.Time); err != nil {
				return fmt.Errorf("upsert failed: %w", err)
			}
			changeCount++
		case 1: // Delete
			if err := r.deleteRecord(userId.Int32); err != nil {
				return fmt.Errorf("delete failed: %w", err)
			}
			changeCount++
		}
	}

	if changeCount > 0 {
		r.statusMu.Lock()
		r.status.RecordsReplied += int64(changeCount)
		r.status.LastSyncTime = time.Now()
		if lastLSN != nil {
			r.status.LastLSN = fmt.Sprintf("%x", lastLSN)
		}
		r.status.LastError = ""
		r.statusMu.Unlock()
		log.Printf("Replicated %d changes", changeCount)
	}

	return rows.Err()
}

func (r *Replicator) upsertRecord(userId int32, name, email string, createdAt, updatedAt time.Time) error {
	query := `
		MERGE INTO Users AS target
		USING (SELECT @UserId AS UserId, @Name AS Name, @Email AS Email, @CreatedAt AS CreatedAt, @UpdatedAt AS UpdatedAt) AS source
		ON target.UserId = source.UserId
		WHEN MATCHED THEN
			UPDATE SET Name = source.Name, Email = source.Email, UpdatedAt = source.UpdatedAt
		WHEN NOT MATCHED THEN
			INSERT (UserId, Name, Email, CreatedAt, UpdatedAt)
			VALUES (source.UserId, source.Name, source.Email, source.CreatedAt, source.UpdatedAt);
	`
	_, err := r.secondaryDB.Exec(query, sql.Named("UserId", userId), sql.Named("Name", name),
		sql.Named("Email", email), sql.Named("CreatedAt", createdAt), sql.Named("UpdatedAt", updatedAt))
	return err
}

func (r *Replicator) deleteRecord(userId int32) error {
	_, err := r.secondaryDB.Exec("DELETE FROM Users WHERE UserId = @UserId", sql.Named("UserId", userId))
	return err
}

func (r *Replicator) HandleStatus(w http.ResponseWriter, req *http.Request) {
	r.statusMu.RLock()
	status := r.status
	r.statusMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (r *Replicator) HandleHealth(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
