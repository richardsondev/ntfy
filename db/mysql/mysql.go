// Package mysql opens MySQL connection pools for ntfy's database-backed stores.
//
// The package mirrors db/pg: it accepts a URL-form DSN (mysql://user:pass@host:port/dbname?params)
// for UX parity with the PostgreSQL backend, parses pool-tuning and TLS query parameters,
// and converts the URL to the driver's native DSN form before opening a *sql.DB.
//
// MySQL 8.0.20 or newer is required (the runtime queries depend on the row-alias
// "INSERT ... AS new ON DUPLICATE KEY UPDATE c = new.c" syntax). MariaDB is not
// supported; the version probe in Open will reject it with a clear error.
package mysql

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"

	"heckel.io/ntfy/v2/db"
)

const (
	// minMySQLVersion is the lowest MySQL version we accept. 8.0.20 introduced the
	// "INSERT ... AS new ON DUPLICATE KEY UPDATE c = new.c" row-alias syntax that the
	// upsert queries rely on.
	minMySQLMajor = 8
	minMySQLMinor = 0
	minMySQLPatch = 20
)

// tlsRegistry tracks which custom *tls.Config names we have already registered with
// the mysql driver so concurrent Open calls don't race on RegisterTLSConfig.
var (
	tlsRegistryMu sync.Mutex
	tlsRegistry   = make(map[string]struct{})
)

// Open opens a MySQL connection pool for a primary database. It pings the database
// to verify connectivity and rejects MariaDB or MySQL versions older than 8.0.20.
func Open(dsn string) (*db.Host, error) {
	d, err := open(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	if err := d.DB.Ping(); err != nil {
		return nil, fmt.Errorf("database ping failed on %v: %w", d.Addr, err)
	}
	if err := verifyServerVersion(d.DB); err != nil {
		d.DB.Close()
		return nil, fmt.Errorf("database version check failed on %v: %w", d.Addr, err)
	}
	return d, nil
}

// OpenReplica opens a MySQL connection pool for a read replica. Unlike Open it does
// not ping or version-check the database, since replicas are health-checked in the
// background by db.DB.
func OpenReplica(dsn string) (*db.Host, error) {
	return open(dsn)
}

// open parses a mysql:// URL-form DSN, peels off ntfy-specific tuning and TLS
// parameters, converts the URL to the driver's native DSN form, opens the pool,
// and applies pool limits.
//
// Recognised ntfy-specific query parameters (all are stripped from the DSN before
// it reaches the driver):
//
//   - pool_max_conns          (int, default 10)        SetMaxOpenConns
//   - pool_max_idle_conns     (int, default 0)         SetMaxIdleConns
//   - pool_conn_max_lifetime  (duration, default 0)    SetConnMaxLifetime
//   - pool_conn_max_idle_time (duration, default 0)    SetConnMaxIdleTime
//   - sslrootcert             (path)                   load PEM as RootCAs, register a
//                                                      *tls.Config, rewrite "tls=<name>"
//   - sslservername           (string)                 ServerName on the *tls.Config
//
// Pass-through TLS shortcuts handled by the driver itself: "tls=true",
// "tls=skip-verify", "tls=preferred", "tls=false". When sslrootcert is also set,
// any user-supplied tls= value is replaced with the registered config name.
func open(dsn string) (*db.Host, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid database URL: %w", err)
	}
	if u.Scheme != "mysql" {
		return nil, fmt.Errorf("invalid database URL scheme %q, must be \"mysql\" (URL: %s)", u.Scheme, censorPassword(u))
	}
	if u.Host == "" {
		return nil, fmt.Errorf("invalid database URL, host is required (URL: %s)", censorPassword(u))
	}

	q := u.Query()
	maxOpenConns, err := extractIntParam(q, "pool_max_conns", 10)
	if err != nil {
		return nil, err
	}
	maxIdleConns, err := extractIntParam(q, "pool_max_idle_conns", 0)
	if err != nil {
		return nil, err
	}
	connMaxLifetime, err := extractDurationParam(q, "pool_conn_max_lifetime", 0)
	if err != nil {
		return nil, err
	}
	connMaxIdleTime, err := extractDurationParam(q, "pool_conn_max_idle_time", 0)
	if err != nil {
		return nil, err
	}
	sslRootCert := q.Get("sslrootcert")
	q.Del("sslrootcert")
	sslServerName := q.Get("sslservername")
	q.Del("sslservername")

	if sslRootCert != "" {
		tlsName, err := registerTLSConfig(u.Host, sslRootCert, sslServerName)
		if err != nil {
			return nil, err
		}
		q.Set("tls", tlsName)
	}

	driverDSN, err := buildDriverDSN(u, q)
	if err != nil {
		return nil, err
	}

	d, err := sql.Open("mysql", driverDSN)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(maxOpenConns)
	if maxIdleConns > 0 {
		d.SetMaxIdleConns(maxIdleConns)
	}
	if connMaxLifetime > 0 {
		d.SetConnMaxLifetime(connMaxLifetime)
	}
	if connMaxIdleTime > 0 {
		d.SetConnMaxIdleTime(connMaxIdleTime)
	}
	return &db.Host{
		Addr: u.Host,
		DB:   d,
	}, nil
}

// buildDriverDSN converts a parsed mysql:// URL plus its (already-cleaned)
// query values into the go-sql-driver/mysql native DSN format:
//
//	[user[:password]@]tcp(host:port)/dbname?param=value
//
// It always sets parseTime=true (so BIGINT/DATETIME columns scan into time.Time
// when expected) and forces UTC for clarity, unless the caller has explicitly
// overridden those parameters.
func buildDriverDSN(u *url.URL, q url.Values) (string, error) {
	cfg := mysqldriver.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = u.Host
	if u.User != nil {
		cfg.User = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			cfg.Passwd = pw
		}
	}
	cfg.DBName = strings.TrimPrefix(u.Path, "/")
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.AllowNativePasswords = true
	cfg.Params = make(map[string]string)
	for key, vals := range q {
		if len(vals) == 0 {
			continue
		}
		switch key {
		case "tls":
			cfg.TLSConfig = vals[0]
		case "parseTime":
			b, err := strconv.ParseBool(vals[0])
			if err != nil {
				return "", fmt.Errorf("invalid parseTime value %q: %w", vals[0], err)
			}
			cfg.ParseTime = b
		case "loc":
			loc, err := time.LoadLocation(vals[0])
			if err != nil {
				return "", fmt.Errorf("invalid loc value %q: %w", vals[0], err)
			}
			cfg.Loc = loc
		case "allowNativePasswords":
			b, err := strconv.ParseBool(vals[0])
			if err != nil {
				return "", fmt.Errorf("invalid allowNativePasswords value %q: %w", vals[0], err)
			}
			cfg.AllowNativePasswords = b
		default:
			cfg.Params[key] = vals[0]
		}
	}
	return cfg.FormatDSN(), nil
}

// registerTLSConfig loads a PEM CA bundle from sslRootCert, builds a *tls.Config
// (with optional ServerName from sslServerName, falling back to the URL host
// without its port), and registers it under a deterministic name derived from
// the host. The deterministic name keeps re-opens (e.g. across reconnect) from
// piling up entries in the driver registry while still avoiding collisions
// between primary + replicas served from different CAs.
func registerTLSConfig(host, sslRootCert, sslServerName string) (string, error) {
	pem, err := os.ReadFile(sslRootCert)
	if err != nil {
		return "", fmt.Errorf("failed to read sslrootcert %q: %w", sslRootCert, err)
	}
	roots := x509.NewCertPool()
	if ok := roots.AppendCertsFromPEM(pem); !ok {
		return "", fmt.Errorf("sslrootcert %q does not contain any valid PEM-encoded certificates", sslRootCert)
	}
	serverName := sslServerName
	if serverName == "" {
		if h, _, err := splitHostPort(host); err == nil {
			serverName = h
		} else {
			serverName = host
		}
	}
	sum := sha256.Sum256([]byte(host + "|" + sslRootCert + "|" + serverName))
	name := "ntfy-tls-" + hex.EncodeToString(sum[:8])

	tlsRegistryMu.Lock()
	defer tlsRegistryMu.Unlock()
	if _, ok := tlsRegistry[name]; ok {
		return name, nil
	}
	cfg := &tls.Config{
		RootCAs:    roots,
		ServerName: serverName,
		MinVersion: tls.VersionTLS12,
	}
	if err := mysqldriver.RegisterTLSConfig(name, cfg); err != nil {
		return "", fmt.Errorf("failed to register tls config: %w", err)
	}
	tlsRegistry[name] = struct{}{}
	return name, nil
}

// splitHostPort splits "host:port" defensively without requiring a port. It accepts
// bracketed IPv6 forms like "[::1]:3306" and bare hostnames.
func splitHostPort(hostport string) (string, string, error) {
	if strings.HasPrefix(hostport, "[") {
		end := strings.Index(hostport, "]")
		if end == -1 {
			return "", "", fmt.Errorf("invalid IPv6 host: %s", hostport)
		}
		host := hostport[1:end]
		rest := hostport[end+1:]
		port := strings.TrimPrefix(rest, ":")
		return host, port, nil
	}
	idx := strings.LastIndex(hostport, ":")
	if idx == -1 {
		return hostport, "", nil
	}
	return hostport[:idx], hostport[idx+1:], nil
}

// verifyServerVersion runs "SELECT VERSION()" against the pool and rejects MariaDB
// or any MySQL build older than 8.0.20.
func verifyServerVersion(d *sql.DB) error {
	var version string
	if err := d.QueryRow("SELECT VERSION()").Scan(&version); err != nil {
		return fmt.Errorf("failed to query server version: %w", err)
	}
	if strings.Contains(strings.ToLower(version), "mariadb") {
		return fmt.Errorf("MariaDB is not supported (server reported %q); use MySQL 8.0.20 or newer", version)
	}
	major, minor, patch, err := parseMySQLVersion(version)
	if err != nil {
		return fmt.Errorf("failed to parse server version %q: %w", version, err)
	}
	if !atLeast(major, minor, patch, minMySQLMajor, minMySQLMinor, minMySQLPatch) {
		return fmt.Errorf("MySQL %d.%d.%d or newer is required (server reported %q)", minMySQLMajor, minMySQLMinor, minMySQLPatch, version)
	}
	return nil
}

func parseMySQLVersion(version string) (int, int, int, error) {
	core := version
	if i := strings.IndexAny(core, "-+ "); i >= 0 {
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) < 2 {
		return 0, 0, 0, fmt.Errorf("not enough version components: %s", version)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, err
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, err
	}
	patch := 0
	if len(parts) >= 3 {
		patch, err = strconv.Atoi(parts[2])
		if err != nil {
			return 0, 0, 0, err
		}
	}
	return major, minor, patch, nil
}

func atLeast(haveMajor, haveMinor, havePatch, wantMajor, wantMinor, wantPatch int) bool {
	if haveMajor != wantMajor {
		return haveMajor > wantMajor
	}
	if haveMinor != wantMinor {
		return haveMinor > wantMinor
	}
	return havePatch >= wantPatch
}

func extractIntParam(q url.Values, key string, defaultValue int) (int, error) {
	s := q.Get(key)
	if s == "" {
		return defaultValue, nil
	}
	q.Del(key)
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid %s value %q: %w", key, s, err)
	}
	return v, nil
}

func extractDurationParam(q url.Values, key string, defaultValue time.Duration) (time.Duration, error) {
	s := q.Get(key)
	if s == "" {
		return defaultValue, nil
	}
	q.Del(key)
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid %s value %q: %w", key, s, err)
	}
	return d, nil
}

// censorPassword returns a string representation of the URL with the password
// replaced by "*****". Symmetrical to db/pg.censorPassword.
func censorPassword(u *url.URL) string {
	if password, hasPassword := u.User.Password(); hasPassword {
		return strings.Replace(u.String(), ":"+password+"@", ":*****@", 1)
	}
	return u.String()
}
