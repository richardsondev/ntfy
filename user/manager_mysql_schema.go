package user

import (
	"database/sql"
	"fmt"
)

// Initial MySQL schema for the user manager. Mirrors manager_postgres_schema.go
// with MySQL adjustments:
//
//   - "user" reserved word -> backticked `user`
//   - "read"/"write" reserved-in-MySQL words -> backticked `read`/`write`
//   - JSONB -> JSON
//   - EXTRACT(EPOCH FROM NOW())::BIGINT -> UNIX_TIMESTAMP()
//   - ON CONFLICT (id) DO NOTHING -> INSERT IGNORE
//   - utf8mb4 / utf8mb4_bin charset so UNIQUE(user_name), UNIQUE(stripe_*) are
//     case-sensitive and emoji-safe
//
// Existing PostgreSQL deployments are at schema version 7 (after migration
// 6->7 added user_email). New MySQL deployments start fresh at the same
// version, so no in-table migrations are registered yet.
const (
	mysqlCreateTierTableQuery = `
		CREATE TABLE IF NOT EXISTS tier (
			id VARCHAR(64) NOT NULL PRIMARY KEY,
			_seq BIGINT NOT NULL AUTO_INCREMENT,
			code VARCHAR(64) NOT NULL,
			name VARCHAR(128) NOT NULL,
			messages_limit BIGINT NOT NULL,
			messages_expiry_duration BIGINT NOT NULL,
			emails_limit BIGINT NOT NULL,
			calls_limit BIGINT NOT NULL,
			reservations_limit BIGINT NOT NULL,
			attachment_file_size_limit BIGINT NOT NULL,
			attachment_total_size_limit BIGINT NOT NULL,
			attachment_expiry_duration BIGINT NOT NULL,
			attachment_bandwidth_limit BIGINT NOT NULL,
			stripe_monthly_price_id VARCHAR(255) NULL,
			stripe_yearly_price_id VARCHAR(255) NULL,
			UNIQUE KEY uq_tier_code (code),
			UNIQUE KEY uq_tier_stripe_monthly (stripe_monthly_price_id),
			UNIQUE KEY uq_tier_stripe_yearly (stripe_yearly_price_id),
			KEY ix_tier_seq (_seq)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
	`
	mysqlCreateUserTableQuery = "" +
		"CREATE TABLE IF NOT EXISTS `user` (" +
		"id VARCHAR(64) NOT NULL PRIMARY KEY," +
		"tier_id VARCHAR(64) NULL," +
		"user_name VARCHAR(255) NOT NULL," +
		"pass VARCHAR(255) NOT NULL," +
		"role VARCHAR(32) NOT NULL," +
		"prefs JSON NOT NULL," +
		"sync_topic VARCHAR(128) NOT NULL," +
		"provisioned BOOLEAN NOT NULL," +
		"stats_messages BIGINT NOT NULL DEFAULT 0," +
		"stats_emails BIGINT NOT NULL DEFAULT 0," +
		"stats_calls BIGINT NOT NULL DEFAULT 0," +
		"stripe_customer_id VARCHAR(255) NULL," +
		"stripe_subscription_id VARCHAR(255) NULL," +
		"stripe_subscription_status VARCHAR(64) NULL," +
		"stripe_subscription_interval VARCHAR(64) NULL," +
		"stripe_subscription_paid_until BIGINT NULL," +
		"stripe_subscription_cancel_at BIGINT NULL," +
		"created BIGINT NOT NULL," +
		"deleted BIGINT NULL," +
		"UNIQUE KEY uq_user_name (user_name)," +
		"UNIQUE KEY uq_user_stripe_customer (stripe_customer_id)," +
		"UNIQUE KEY uq_user_stripe_subscription (stripe_subscription_id)," +
		"CONSTRAINT chk_user_role CHECK (role IN ('anonymous', 'admin', 'user'))," +
		"CONSTRAINT fk_user_tier FOREIGN KEY (tier_id) REFERENCES tier(id)" +
		") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;"

	mysqlCreateUserAccessTableQuery = "" +
		"CREATE TABLE IF NOT EXISTS user_access (" +
		"user_id VARCHAR(64) NOT NULL," +
		"topic VARCHAR(255) NOT NULL," +
		"`read` BOOLEAN NOT NULL," +
		"`write` BOOLEAN NOT NULL," +
		"owner_user_id VARCHAR(64) NULL," +
		"provisioned BOOLEAN NOT NULL," +
		"PRIMARY KEY (user_id, topic)," +
		"CONSTRAINT fk_user_access_user FOREIGN KEY (user_id) REFERENCES `user`(id) ON DELETE CASCADE," +
		"CONSTRAINT fk_user_access_owner FOREIGN KEY (owner_user_id) REFERENCES `user`(id) ON DELETE CASCADE" +
		") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;"

	mysqlCreateUserTokenTableQuery = "" +
		"CREATE TABLE IF NOT EXISTS user_token (" +
		"user_id VARCHAR(64) NOT NULL," +
		"token VARCHAR(128) NOT NULL," +
		"label VARCHAR(255) NOT NULL," +
		"last_access BIGINT NOT NULL," +
		"last_origin VARCHAR(64) NOT NULL," +
		"expires BIGINT NOT NULL," +
		"provisioned BOOLEAN NOT NULL," +
		"PRIMARY KEY (user_id, token)," +
		"UNIQUE KEY uq_user_token (token)," +
		"CONSTRAINT fk_user_token_user FOREIGN KEY (user_id) REFERENCES `user`(id) ON DELETE CASCADE" +
		") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;"

	mysqlCreateUserPhoneTableQuery = "" +
		"CREATE TABLE IF NOT EXISTS user_phone (" +
		"user_id VARCHAR(64) NOT NULL," +
		"phone_number VARCHAR(64) NOT NULL," +
		"PRIMARY KEY (user_id, phone_number)," +
		"CONSTRAINT fk_user_phone_user FOREIGN KEY (user_id) REFERENCES `user`(id) ON DELETE CASCADE" +
		") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;"

	mysqlCreateUserEmailTableQuery = "" +
		"CREATE TABLE IF NOT EXISTS user_email (" +
		"user_id VARCHAR(64) NOT NULL," +
		"email VARCHAR(255) NOT NULL," +
		"PRIMARY KEY (user_id, email)," +
		"CONSTRAINT fk_user_email_user FOREIGN KEY (user_id) REFERENCES `user`(id) ON DELETE CASCADE" +
		") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;"

	mysqlCreateSchemaVersionTableQuery = `
		CREATE TABLE IF NOT EXISTS schema_version (
			store VARCHAR(64) NOT NULL PRIMARY KEY,
			version INT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
	`
	mysqlInsertEveryoneUserQuery = "INSERT IGNORE INTO `user` (id, user_name, pass, role, prefs, sync_topic, provisioned, created)" +
		" VALUES ('" + everyoneID + "', '*', '', 'anonymous', '{}', '', false, UNIX_TIMESTAMP())"
)

// Schema table management queries for MySQL
const (
	mysqlCurrentSchemaVersion     = 7
	mysqlSelectSchemaVersionQuery = `SELECT version FROM schema_version WHERE store = 'user'`
	mysqlInsertSchemaVersionQuery = `INSERT INTO schema_version (store, version) VALUES ('user', ?)`
)

// New MySQL installs start at the current version, so the migrations map is
// empty for now. Future cross-backend schema changes will register entries
// here, mirroring postgresMigrations.
var mysqlMigrationsUser = map[int]func(db *sql.DB) error{}

func setupMySQLUser(d *sql.DB) error {
	var schemaVersion int
	err := d.QueryRow(mysqlSelectSchemaVersionQuery).Scan(&schemaVersion)
	if err != nil {
		return setupNewMySQLUser(d)
	}
	if schemaVersion == mysqlCurrentSchemaVersion {
		return nil
	} else if schemaVersion > mysqlCurrentSchemaVersion {
		return fmt.Errorf("unexpected schema version: version %d is higher than current version %d", schemaVersion, mysqlCurrentSchemaVersion)
	}
	for i := schemaVersion; i < mysqlCurrentSchemaVersion; i++ {
		fn, ok := mysqlMigrationsUser[i]
		if !ok {
			return fmt.Errorf("cannot find migration step from schema version %d to %d", i, i+1)
		} else if err := fn(d); err != nil {
			return err
		}
	}
	return nil
}

func setupNewMySQLUser(d *sql.DB) error {
	// MySQL DDL implicitly commits, so create tables individually with
	// IF NOT EXISTS rather than wrapping them in a single transaction.
	for _, stmt := range []string{
		mysqlCreateTierTableQuery,
		mysqlCreateUserTableQuery,
		mysqlCreateUserAccessTableQuery,
		mysqlCreateUserTokenTableQuery,
		mysqlCreateUserPhoneTableQuery,
		mysqlCreateUserEmailTableQuery,
		mysqlCreateSchemaVersionTableQuery,
		mysqlInsertEveryoneUserQuery,
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
