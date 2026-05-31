package message

import (
	"database/sql"
	"fmt"

	"heckel.io/ntfy/v2/db"
	"heckel.io/ntfy/v2/log"
)

// Initial MySQL schema. The shape mirrors message/cache_postgres_schema.go with
// MySQL-specific adjustments:
//
//   - BIGSERIAL                -> BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY
//   - tables use utf8mb4/utf8mb4_bin so unique indexes are case-sensitive and
//     comparisons stay emoji-safe
//   - partial indexes (CREATE INDEX ... WHERE ...) are not supported in MySQL,
//     so the predicate is reapplied at query time and the index is created
//     unconditionally over the same columns
const (
	mysqlCreateTablesQuery = `
		CREATE TABLE IF NOT EXISTS message (
			id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			mid VARCHAR(64) NOT NULL,
			sequence_id VARCHAR(64) NOT NULL,
			time BIGINT NOT NULL,
			event VARCHAR(64) NOT NULL,
			expires BIGINT NOT NULL,
			topic VARCHAR(255) NOT NULL,
			message MEDIUMTEXT NOT NULL,
			title TEXT NOT NULL,
			priority INT NOT NULL,
			tags TEXT NOT NULL,
			click TEXT NOT NULL,
			icon TEXT NOT NULL,
			actions MEDIUMTEXT NOT NULL,
			attachment_name TEXT NOT NULL,
			attachment_type TEXT NOT NULL,
			attachment_size BIGINT NOT NULL,
			attachment_expires BIGINT NOT NULL,
			attachment_url TEXT NOT NULL,
			attachment_deleted BOOLEAN NOT NULL DEFAULT FALSE,
			sender VARCHAR(64) NOT NULL,
			user_id VARCHAR(64) NOT NULL,
			content_type VARCHAR(255) NOT NULL,
			encoding VARCHAR(32) NOT NULL,
			published BOOLEAN NOT NULL DEFAULT FALSE,
			INDEX idx_message_mid (mid),
			INDEX idx_message_sequence_id (sequence_id),
			INDEX idx_message_topic_published_time (topic, published, time, id),
			INDEX idx_message_published_expires (published, expires),
			INDEX idx_message_attachment_expires (attachment_expires),
			INDEX idx_message_sender_attachment_expires (sender, attachment_expires),
			INDEX idx_message_user_id_attachment_expires (user_id, attachment_expires)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
	`
	mysqlCreateMessageStatsQuery = `
		CREATE TABLE IF NOT EXISTS message_stats (
			` + "`key`" + ` VARCHAR(64) NOT NULL PRIMARY KEY,
			value BIGINT
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
	`
	mysqlInsertMessageStatsZeroQuery = "INSERT IGNORE INTO message_stats (`key`, value) VALUES ('messages', 0)"
	mysqlCreateSchemaVersionQuery    = `
		CREATE TABLE IF NOT EXISTS schema_version (
			store VARCHAR(64) NOT NULL PRIMARY KEY,
			version INT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
	`
)

// MySQL schema management queries
const (
	mysqlCurrentSchemaVersion     = 15
	mysqlInsertSchemaVersionQuery = `INSERT INTO schema_version (store, version) VALUES ('message', ?)`
	mysqlUpdateSchemaVersionQuery = `UPDATE schema_version SET version = ? WHERE store = 'message'`
	mysqlSelectSchemaVersionQuery = `SELECT version FROM schema_version WHERE store = 'message'`
)

// MySQL schema migrations. New ntfy installs start at the current version so
// only entries that match versions older than postgres' currentSchemaVersion
// need to be registered here.
const (
	// 14 -> 15 (parity with PostgreSQL: ensure idx_message_attachment_expires exists)
	mysqlMigrate14To15CreateIndexQuery = `
		CREATE INDEX idx_message_attachment_expires ON message (attachment_expires);
	`
)

var mysqlMigrations = map[int]func(d *sql.DB) error{
	14: mysqlMigrateFrom14,
}

func setupMySQL(d *sql.DB) error {
	var schemaVersion int
	if err := d.QueryRow(mysqlSelectSchemaVersionQuery).Scan(&schemaVersion); err != nil {
		return setupNewMySQLDB(d)
	} else if schemaVersion == mysqlCurrentSchemaVersion {
		return nil
	} else if schemaVersion > mysqlCurrentSchemaVersion {
		return fmt.Errorf("unexpected schema version: version %d is higher than current version %d", schemaVersion, mysqlCurrentSchemaVersion)
	}
	for i := schemaVersion; i < mysqlCurrentSchemaVersion; i++ {
		fn, ok := mysqlMigrations[i]
		if !ok {
			return fmt.Errorf("cannot find migration step from schema version %d to %d", i, i+1)
		} else if err := fn(d); err != nil {
			return err
		}
	}
	return nil
}

func mysqlMigrateFrom14(d *sql.DB) error {
	log.Tag(tagMessageCache).Info("Migrating message cache database schema: from 14 to 15")
	return db.ExecTx(d, func(tx *sql.Tx) error {
		if _, err := tx.Exec(mysqlMigrate14To15CreateIndexQuery); err != nil {
			return err
		}
		if _, err := tx.Exec(mysqlUpdateSchemaVersionQuery, 15); err != nil {
			return err
		}
		return nil
	})
}

func setupNewMySQLDB(sqlDB *sql.DB) error {
	// MySQL does not allow multiple statements (including DDL) inside a single
	// transaction without implicit COMMIT side-effects; each CREATE TABLE is
	// auto-committed regardless of BEGIN/COMMIT, so issue them as separate
	// idempotent statements.
	for _, stmt := range []string{
		mysqlCreateTablesQuery,
		mysqlCreateMessageStatsQuery,
		mysqlInsertMessageStatsZeroQuery,
		mysqlCreateSchemaVersionQuery,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			return err
		}
	}
	if _, err := sqlDB.Exec(mysqlInsertSchemaVersionQuery, mysqlCurrentSchemaVersion); err != nil {
		return err
	}
	return nil
}
