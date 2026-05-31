package mysql

import (
	"crypto/x509"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

func TestOpen_RejectsNonMySQLScheme(t *testing.T) {
	_, err := Open("postgres://user:pass@localhost:5432/ntfy")
	require.NotNil(t, err)
	require.Contains(t, err.Error(), "scheme")
}

func TestOpen_RejectsEmptyHost(t *testing.T) {
	_, err := Open("mysql://user:pass@/ntfy")
	require.NotNil(t, err)
	require.Contains(t, err.Error(), "host is required")
}

func TestOpen_RejectsInvalidURL(t *testing.T) {
	_, err := Open("mysql://%zz")
	require.NotNil(t, err)
}

func TestBuildDriverDSN_BasicAuthAndDB(t *testing.T) {
	dsn, err := buildDSN(t, "mysql://phil:secret@db.example.com:3306/ntfy")
	require.Nil(t, err)
	cfg, err := mysqldriver.ParseDSN(dsn)
	require.Nil(t, err)
	require.Equal(t, "phil", cfg.User)
	require.Equal(t, "secret", cfg.Passwd)
	require.Equal(t, "tcp", cfg.Net)
	require.Equal(t, "db.example.com:3306", cfg.Addr)
	require.Equal(t, "ntfy", cfg.DBName)
	require.True(t, cfg.ParseTime)
	require.True(t, cfg.AllowNativePasswords)
}

func TestBuildDriverDSN_StripsPoolParams(t *testing.T) {
	dsn, err := buildDSN(t, "mysql://u:p@db.example.com:3306/ntfy?pool_max_conns=5&pool_max_idle_conns=2&pool_conn_max_lifetime=15m&pool_conn_max_idle_time=5m")
	require.Nil(t, err)
	require.NotContains(t, dsn, "pool_max_conns")
	require.NotContains(t, dsn, "pool_max_idle_conns")
	require.NotContains(t, dsn, "pool_conn_max_lifetime")
	require.NotContains(t, dsn, "pool_conn_max_idle_time")
}

func TestBuildDriverDSN_PreservesPassThroughParams(t *testing.T) {
	// Custom server-side variables (any key the driver doesn't have a typed Config
	// field for) round-trip through cfg.Params unchanged.
	dsn, err := buildDSN(t, "mysql://u:p@db.example.com:3306/ntfy?tls=skip-verify&time_zone=%27%2B00%3A00%27")
	require.Nil(t, err)
	cfg, err := mysqldriver.ParseDSN(dsn)
	require.Nil(t, err)
	require.Equal(t, "skip-verify", cfg.TLSConfig)
	require.Contains(t, cfg.Params, "time_zone")
	require.Equal(t, "'+00:00'", cfg.Params["time_zone"])
}

func TestBuildDriverDSN_InvalidParseTime(t *testing.T) {
	_, err := buildDSN(t, "mysql://u:p@db.example.com:3306/ntfy?parseTime=not-a-bool")
	require.NotNil(t, err)
	require.Contains(t, err.Error(), "parseTime")
}

func TestExtractIntParam_BadValue(t *testing.T) {
	q := url.Values{}
	q.Set("pool_max_conns", "abc")
	_, err := extractIntParam(q, "pool_max_conns", 10)
	require.NotNil(t, err)
}

func TestExtractDurationParam_BadValue(t *testing.T) {
	q := url.Values{}
	q.Set("pool_conn_max_lifetime", "not-a-duration")
	_, err := extractDurationParam(q, "pool_conn_max_lifetime", 0)
	require.NotNil(t, err)
}

func TestRegisterTLSConfig_MissingFile(t *testing.T) {
	_, err := registerTLSConfig("db.example.com:3306", filepath.Join(t.TempDir(), "missing.pem"), "")
	require.NotNil(t, err)
	require.Contains(t, err.Error(), "sslrootcert")
}

func TestRegisterTLSConfig_InvalidPEM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garbage.pem")
	require.Nil(t, os.WriteFile(path, []byte("definitely not a PEM"), 0o600))
	_, err := registerTLSConfig("db.example.com:3306", path, "")
	require.NotNil(t, err)
	require.Contains(t, err.Error(), "valid PEM")
}

func TestRegisterTLSConfig_ValidPEM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ca.pem")
	require.Nil(t, os.WriteFile(path, []byte(samplePEMCert), 0o600))
	name, err := registerTLSConfig("db.example.com:3306", path, "db.example.com")
	require.Nil(t, err)
	require.True(t, strings.HasPrefix(name, "ntfy-tls-"))
	// Second call with identical inputs should reuse the registered name.
	name2, err := registerTLSConfig("db.example.com:3306", path, "db.example.com")
	require.Nil(t, err)
	require.Equal(t, name, name2)
}

func TestRegisterTLSConfig_PerHostUniqueness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ca.pem")
	require.Nil(t, os.WriteFile(path, []byte(samplePEMCert), 0o600))
	name1, err := registerTLSConfig("primary.example.com:3306", path, "")
	require.Nil(t, err)
	name2, err := registerTLSConfig("replica.example.com:3306", path, "")
	require.Nil(t, err)
	require.NotEqual(t, name1, name2)
}

func TestSplitHostPort(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantPort string
	}{
		{"host:3306", "host", "3306"},
		{"host", "host", ""},
		{"[::1]:3306", "::1", "3306"},
		{"[::1]", "::1", ""},
	}
	for _, c := range cases {
		host, port, err := splitHostPort(c.in)
		require.Nil(t, err, c.in)
		require.Equal(t, c.wantHost, host, c.in)
		require.Equal(t, c.wantPort, port, c.in)
	}
}

func TestParseMySQLVersion(t *testing.T) {
	cases := []struct {
		in    string
		major int
		minor int
		patch int
		ok    bool
	}{
		{"8.0.20", 8, 0, 20, true},
		{"8.0.36", 8, 0, 36, true},
		{"8.4.2", 8, 4, 2, true},
		{"8.0.36-log", 8, 0, 36, true},
		{"8.0.36 Community Server", 8, 0, 36, true},
		{"5.7.42", 5, 7, 42, true},
		{"garbage", 0, 0, 0, false},
		{"8", 0, 0, 0, false},
	}
	for _, c := range cases {
		major, minor, patch, err := parseMySQLVersion(c.in)
		if !c.ok {
			require.NotNil(t, err, c.in)
			continue
		}
		require.Nil(t, err, c.in)
		require.Equal(t, c.major, major, c.in)
		require.Equal(t, c.minor, minor, c.in)
		require.Equal(t, c.patch, patch, c.in)
	}
}

func TestAtLeast(t *testing.T) {
	require.True(t, atLeast(8, 0, 20, 8, 0, 20))
	require.True(t, atLeast(8, 0, 36, 8, 0, 20))
	require.True(t, atLeast(8, 4, 0, 8, 0, 20))
	require.True(t, atLeast(9, 0, 0, 8, 0, 20))
	require.False(t, atLeast(8, 0, 19, 8, 0, 20))
	require.False(t, atLeast(5, 7, 99, 8, 0, 20))
	require.False(t, atLeast(7, 9, 99, 8, 0, 20))
}

func TestCensorPassword(t *testing.T) {
	u, err := url.Parse("mysql://phil:hunter2@db.example.com:3306/ntfy")
	require.Nil(t, err)
	require.NotContains(t, censorPassword(u), "hunter2")
	require.Contains(t, censorPassword(u), "*****")
}

func buildDSN(t *testing.T, raw string) (string, error) {
	t.Helper()
	u, err := url.Parse(raw)
	require.Nil(t, err)
	q := u.Query()
	q.Del("pool_max_conns")
	q.Del("pool_max_idle_conns")
	q.Del("pool_conn_max_lifetime")
	q.Del("pool_conn_max_idle_time")
	q.Del("sslrootcert")
	q.Del("sslservername")
	return buildDriverDSN(u, q)
}

// samplePEMCert is a self-signed cert used purely to satisfy
// AppendCertsFromPEM in tests. It is not used to verify anything live.
const samplePEMCert = `-----BEGIN CERTIFICATE-----
MIIBhTCCASugAwIBAgIQIRi6zePL6mKjOipn+dNuaTAKBggqhkjOPQQDAjASMRAw
DgYDVQQKEwdBY21lIENvMB4XDTE3MTAyMDE5NDMwNloXDTE4MTAyMDE5NDMwNlow
EjEQMA4GA1UEChMHQWNtZSBDbzBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABD0d
7VNhbWvZLWPuj/RtHFjvtJBEwOkhbN/BnnE8rnZR8+sbwnc/KhCk3FhnpHZnQz7B
5aETbbIgmuvewdjvSBSjYzBhMA4GA1UdDwEB/wQEAwICpDATBgNVHSUEDDAKBggr
BgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MCkGA1UdEQQiMCCCDmxvY2FsaG9zdDo1
NDUzgg4xMjcuMC4wLjE6NTQ1MzAKBggqhkjOPQQDAgNIADBFAiEA2zpJEPQyz6/l
Wf86aX6PepsntZv2GYlA5UpabfT2EZICICpJ5h/iI+i341gBmLiAFQOyTDT+/wQc
6MF9+Yw1Yy0t
-----END CERTIFICATE-----
`

// Silence unused-import warnings in environments where the imports above are
// guarded by build tags.
var _ = x509.NewCertPool
