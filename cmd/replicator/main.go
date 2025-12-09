package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
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
	primaryDB   *pgxpool.Pool
	secondaryDB *pgxpool.Pool
	config      Config
	status      ReplicationStatus
	statusMu    sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
}

func main() {
	err := godotenv.Load("D:\\Code\\Go-Web-Template\\.env")
	if err != nil {
		log.Fatalf("Error loading .env file: %v", err)
	}

	main_database_host := os.Getenv("DATABASE_URL_HOST")
	main_database_port := 6543
	main_database_user := os.Getenv("DATABASE_URL_USER")
	main_database_password := os.Getenv("DATABASE_URL_PASSWORD")
	main_database_dbname := os.Getenv("DATABASE_URL_DBNAME")

	replicator_database_host := os.Getenv("DATABASE_URL_REPLICATOR_HOST")
	replicator_database_port := 6543
	replicator_database_user := os.Getenv("DATABASE_URL_REPLICATOR_USER")
	replicator_database_password := os.Getenv("DATABASE_URL_REPLICATOR_PASSWORD")
	replicator_database_dbname := os.Getenv("DATABASE_URL_REPLICATOR_DBNAME")

	config := Config{
		PrimaryConnStr: fmt.Sprintf("host=%s port=%d user=%s "+
			"password=%s dbname=%s sslmode=require default_query_exec_mode=simple_protocol", main_database_host, main_database_port, main_database_user, main_database_password, main_database_dbname),
		SecondaryConnStr: fmt.Sprintf("host=%s port=%d user=%s "+
			"password=%s dbname=%s sslmode=require default_query_exec_mode=simple_protocol", replicator_database_host, replicator_database_port, replicator_database_user, replicator_database_password, replicator_database_dbname),
		PollInterval: 5 * time.Second,
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

	primaryDB, err := pgxpool.New(context.Background(), config.PrimaryConnStr)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to connect to primary: %w", err)
	}

	secondaryDB, err := pgxpool.New(context.Background(), config.SecondaryConnStr)
	if err != nil {
		primaryDB.Close()
		cancel()
		return nil, fmt.Errorf("failed to connect to secondary: %w", err)
	}

	// Test connections
	if err := primaryDB.Ping(context.Background()); err != nil {
		primaryDB.Close()
		secondaryDB.Close()
		cancel()
		return nil, fmt.Errorf("primary DB ping failed: %w", err)
	}

	if err := secondaryDB.Ping(context.Background()); err != nil {
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
	// Query unprocessed changes from user_changes table
	query := `
		SELECT 
			change_id,
			operation,
			user_id,
			name,
			email,
			created_at,
			updated_at
		FROM user_changes
		WHERE processed = false
		ORDER BY change_id
		LIMIT 100
	`

	rows, err := r.primaryDB.Query(context.Background(), query)
	if err != nil {
		return fmt.Errorf("change query failed: %w", err)
	}
	defer rows.Close()

	changeCount := 0
	var lastChangeID int64
	processedIDs := []int64{}

	for rows.Next() {
		var changeID int64
		var operation string
		var userId *int32
		var name, email *string
		var createdAt, updatedAt *time.Time

		if err := rows.Scan(&changeID, &operation, &userId, &name, &email, &createdAt, &updatedAt); err != nil {
			return fmt.Errorf("scan error: %w", err)
		}

		lastChangeID = changeID

		// Apply the change to secondary database
		switch operation {
		case "INSERT", "UPDATE":
			if userId != nil && name != nil {
				if err := r.upsertRecord(*userId, *name, email, createdAt, updatedAt); err != nil {
					return fmt.Errorf("upsert failed: %w", err)
				}
				processedIDs = append(processedIDs, changeID)
				changeCount++
			}
		case "DELETE":
			if userId != nil {
				if err := r.deleteRecord(*userId); err != nil {
					return fmt.Errorf("delete failed: %w", err)
				}
				processedIDs = append(processedIDs, changeID)
				changeCount++
			}
		}
	}

	if err := rows.Err(); err != nil {
		return err
	}

	// Mark changes as processed
	if len(processedIDs) > 0 {
		updateQuery := `UPDATE user_changes SET processed = true WHERE change_id = ANY($1)`
		_, err := r.primaryDB.Exec(context.Background(), updateQuery, processedIDs)
		if err != nil {
			return fmt.Errorf("failed to mark changes as processed: %w", err)
		}
	}

	if changeCount > 0 {
		r.statusMu.Lock()
		r.status.RecordsReplied += int64(changeCount)
		r.status.LastSyncTime = time.Now()
		r.status.LastLSN = fmt.Sprintf("%d", lastChangeID)
		r.status.LastError = ""
		r.statusMu.Unlock()
		log.Printf("Replicated %d changes (up to change_id %d)", changeCount, lastChangeID)
	}

	return nil
}

func (r *Replicator) upsertRecord(userId int32, name string, email *string, createdAt, updatedAt *time.Time) error {
	query := `
		INSERT INTO users (user_id, name, email, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id) 
		DO UPDATE SET 
			name = EXCLUDED.name,
			email = EXCLUDED.email,
			updated_at = EXCLUDED.updated_at
	`
	_, err := r.secondaryDB.Exec(context.Background(), query, userId, name, email, createdAt, updatedAt)
	return err
}

func (r *Replicator) deleteRecord(userId int32) error {
	_, err := r.secondaryDB.Exec(context.Background(), "DELETE FROM users WHERE user_id = $1", userId)
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
