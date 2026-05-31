package dbtest

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"heckel.io/ntfy/v2/db"
	"heckel.io/ntfy/v2/db/mysql"
	"heckel.io/ntfy/v2/db/pg"
	"heckel.io/ntfy/v2/util"
)

const testPoolMaxConns = "2"

// CreateTestPostgresSchema creates a temporary PostgreSQL schema and returns the DSN pointing to it.
// It registers a cleanup function to drop the schema when the test finishes.
// If NTFY_TEST_DATABASE_URL is not set, the test is skipped.
func CreateTestPostgresSchema(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("NTFY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NTFY_TEST_DATABASE_URL not set")
	}
	schema := fmt.Sprintf("test_%s", util.RandomString(10))
	u, err := url.Parse(dsn)
	require.Nil(t, err)
	q := u.Query()
	q.Set("pool_max_conns", testPoolMaxConns)
	u.RawQuery = q.Encode()
	dsn = u.String()
	setupHost, err := pg.Open(dsn)
	require.Nil(t, err)
	_, err = setupHost.DB.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema))
	require.Nil(t, err)
	require.Nil(t, setupHost.DB.Close())
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	schemaDSN := u.String()
	t.Cleanup(func() {
		cleanHost, err := pg.Open(dsn)
		if err == nil {
			cleanHost.DB.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
			cleanHost.DB.Close()
		}
	})
	return schemaDSN
}

// CreateTestPostgres creates a temporary PostgreSQL schema and returns an open *db.DB connection to it.
// It registers cleanup functions to close the DB and drop the schema when the test finishes.
// If NTFY_TEST_DATABASE_URL is not set, the test is skipped.
func CreateTestPostgres(t *testing.T) *db.DB {
	t.Helper()
	schemaDSN := CreateTestPostgresSchema(t)
	testHost, err := pg.Open(schemaDSN)
	require.Nil(t, err)
	d := db.New(testHost, nil)
	t.Cleanup(func() {
		d.Close()
	})
	return d
}

// CreateTestMySQLSchema creates a temporary, throwaway MySQL database and
// returns a DSN pointing to it. MySQL has no per-connection equivalent of
// PostgreSQL's "search_path", so each test gets its own database (named
// "ntfytest_<random>") instead of its own schema within a shared database.
// The cleanup function drops the database when the test finishes.
//
// If NTFY_TEST_MYSQL_DATABASE_URL is not set, the test is skipped.
//
// The DSN must point at a server account with CREATE/DROP DATABASE privileges
// scoped to the configured DB name pattern. The database referenced by the
// supplied DSN is only used as the connection target for the CREATE/DROP — the
// returned DSN substitutes the throwaway database name into the URL path.
func CreateTestMySQLSchema(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("NTFY_TEST_MYSQL_DATABASE_URL")
	if dsn == "" {
		t.Skip("NTFY_TEST_MYSQL_DATABASE_URL not set")
	}
	dbName := "ntfytest_" + util.RandomString(10)
	u, err := url.Parse(dsn)
	require.Nil(t, err)
	q := u.Query()
	q.Set("pool_max_conns", testPoolMaxConns)
	u.RawQuery = q.Encode()
	setupDSN := u.String()
	setupHost, err := mysql.Open(setupDSN)
	require.Nil(t, err)
	_, err = setupHost.DB.Exec(fmt.Sprintf("CREATE DATABASE `%s` DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin", dbName))
	require.Nil(t, err)
	require.Nil(t, setupHost.DB.Close())

	// Build the per-test DSN by swapping the URL path to the new database name.
	testURL := *u
	testURL.Path = "/" + dbName
	testDSN := testURL.String()

	t.Cleanup(func() {
		cleanHost, err := mysql.Open(setupDSN)
		if err == nil {
			cleanHost.DB.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", dbName))
			cleanHost.DB.Close()
		}
	})
	return testDSN
}

// CreateTestMySQL creates a temporary MySQL database and returns an open *db.DB
// connection to it. It registers cleanup functions to close the connection and
// drop the database when the test finishes. If NTFY_TEST_MYSQL_DATABASE_URL is
// not set, the test is skipped.
func CreateTestMySQL(t *testing.T) *db.DB {
	t.Helper()
	testDSN := CreateTestMySQLSchema(t)
	testHost, err := mysql.Open(testDSN)
	require.Nil(t, err)
	d := db.New(testHost, nil)
	t.Cleanup(func() {
		d.Close()
	})
	return d
}

// IsMySQLDSN reports whether the given DSN is a mysql:// URL. Used by tests
// that need to know which backend they are about to wire up (e.g. dispatch
// between user.NewPostgresManager and user.NewMySQLManager).
func IsMySQLDSN(dsn string) bool {
	return strings.HasPrefix(dsn, "mysql://")
}
