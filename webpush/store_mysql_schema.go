package webpush

import (
	"database/sql"
	"fmt"
)

// Initial MySQL schema for the web push store. Mirrors webpush/store_postgres.go
// schema with MySQL adjustments:
//
//   - utf8mb4 / utf8mb4_bin charset so the UNIQUE(endpoint) index is
//     case-sensitive and emoji-safe
//   - FOREIGN KEY ... ON DELETE CASCADE clauses use named constraints
//   - schema_version table is created with IF NOT EXISTS so initialization is
//     idempotent across stores that share the same database
const (
	mysqlCreateSubscriptionTableQuery = `
		CREATE TABLE IF NOT EXISTS webpush_subscription (
			id VARCHAR(64) NOT NULL PRIMARY KEY,
			endpoint VARCHAR(1024) NOT NULL,
			key_auth VARCHAR(255) NOT NULL,
			key_p256dh VARCHAR(255) NOT NULL,
			user_id VARCHAR(64) NOT NULL,
			subscriber_ip VARCHAR(64) NOT NULL,
			updated_at BIGINT NOT NULL,
			warned_at BIGINT NOT NULL DEFAULT 0,
			UNIQUE KEY uq_webpush_endpoint (endpoint(255)),
			INDEX idx_webpush_subscriber_ip (subscriber_ip),
			INDEX idx_webpush_updated_at (updated_at),
			INDEX idx_webpush_user_id (user_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
	`
	mysqlCreateSubscriptionTopicTableQuery = `
		CREATE TABLE IF NOT EXISTS webpush_subscription_topic (
			subscription_id VARCHAR(64) NOT NULL,
			topic VARCHAR(255) NOT NULL,
			PRIMARY KEY (subscription_id, topic),
			INDEX idx_webpush_topic (topic),
			CONSTRAINT fk_webpush_topic_subscription FOREIGN KEY (subscription_id) REFERENCES webpush_subscription(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
	`
	mysqlCreateSchemaVersionTableQuery = `
		CREATE TABLE IF NOT EXISTS schema_version (
			store VARCHAR(64) NOT NULL PRIMARY KEY,
			version INT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
	`
)

// MySQL schema management queries
const (
	mysqlCurrentSchemaVersion     = 1
	mysqlInsertSchemaVersionQuery = `INSERT INTO schema_version (store, version) VALUES ('webpush', ?)`
	mysqlSelectSchemaVersionQuery = `SELECT version FROM schema_version WHERE store = 'webpush'`
)

func setupMySQL(d *sql.DB) error {
	var schemaVersion int
	err := d.QueryRow(mysqlSelectSchemaVersionQuery).Scan(&schemaVersion)
	if err != nil {
		return setupNewMySQL(d)
	}
	if schemaVersion > mysqlCurrentSchemaVersion {
		return fmt.Errorf("unexpected schema version: version %d is higher than current version %d", schemaVersion, mysqlCurrentSchemaVersion)
	}
	return nil
}

func setupNewMySQL(d *sql.DB) error {
	// MySQL DDL implicitly commits, so create the tables individually rather
	// than wrapping them in a transaction.
	for _, stmt := range []string{
		mysqlCreateSubscriptionTableQuery,
		mysqlCreateSubscriptionTopicTableQuery,
		mysqlCreateSchemaVersionTableQuery,
	} {
		if _, err := d.Exec(stmt); err != nil {
			return err
		}
	}
	if _, err := d.Exec(mysqlInsertSchemaVersionQuery, mysqlCurrentSchemaVersion); err != nil {
		return err
	}
	return nil
}
