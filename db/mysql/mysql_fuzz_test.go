package mysql

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// FuzzOpen_URL checks that URL-form DSN parsing never panics for arbitrary input.
// open intentionally avoids Ping, so this exercises MySQL DSN normalization without
// requiring a live database and closes any lazy *sql.DB it successfully creates.
func FuzzOpen_URL(f *testing.F) {
	f.Add("mysql://user:pass@host:3306/db")
	f.Add("")
	f.Add("mysql://")
	f.Add("mysql://%zz")
	f.Add("postgres://user:pass@host:5432/db")
	f.Add("mysql://u:p@/db")
	f.Add("mysql://u:p@host:3306/db?pool_max_conns=5&pool_max_idle_conns=2&pool_conn_max_lifetime=15m&pool_conn_max_idle_time=5m")
	f.Add("mysql://u:p@host:3306/db?tls=skip-verify")

	f.Fuzz(func(t *testing.T, rawDSN string) {
		h, err := open(rawDSN)
		if err != nil {
			return
		}
		if h != nil && h.DB != nil {
			if err := h.DB.Close(); err != nil {
				t.Fatalf("close DB: %v", err)
			}
		}
	})
}

// FuzzBuildDriverDSN checks the URL-to-driver DSN round trip. If buildDriverDSN
// accepts a parsed mysql:// URL and cleaned query params, the generated native DSN
// must remain parseable by go-sql-driver/mysql because that is what sql.Open uses.
func FuzzBuildDriverDSN(f *testing.F) {
	f.Add("mysql://user:pass@host:3306/db", "tls=skip-verify")
	f.Add("mysql://user:pass@host:3306/db", "parseTime=false")
	f.Add("mysql://user:pass@host:3306/db", "loc=UTC")
	f.Add("mysql://user:pass@host:3306/db", "allowNativePasswords=false")
	f.Add("mysql://user:pass@host:3306/db", "time_zone=%27%2B00%3A00%27")
	f.Add("mysql://user:pass@host:3306/db", "pool_max_conns=5&pool_max_idle_conns=2")
	f.Add("mysql://user:pass@[::1]:3306/db", "charset=utf8mb4&tls=true")

	f.Fuzz(func(t *testing.T, rawURL, qStr string) {
		u, err := url.Parse(rawURL)
		if err != nil {
			t.Skip()
		}
		q, err := url.ParseQuery(qStr)
		if err != nil {
			t.Skip()
		}
		stripNtfyMySQLParams(q)

		dsn, err := buildDriverDSN(u, q)
		if err != nil {
			if err.Error() == "" {
				t.Fatal("buildDriverDSN returned an empty error")
			}
			return
		}
		if _, err := mysqldriver.ParseDSN(dsn); err != nil {
			t.Fatalf("driver DSN is not parseable: %q: %v", dsn, err)
		}
	})
}

// FuzzParseMySQLVersion checks that arbitrary server VERSION() strings never panic
// the compatibility gate. Successfully parsed components must be non-negative.
func FuzzParseMySQLVersion(f *testing.F) {
	for _, seed := range []string{
		"8.0.20",
		"8.0.36",
		"8.4.2",
		"8.0.36-log",
		"8.0.36 Community Server",
		"5.7.42",
		"garbage",
		"8",
		"",
		"..",
		"99999999999999.0.0",
		"a.b.c",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, version string) {
		major, minor, patch, err := parseMySQLVersion(version)
		if err != nil {
			return
		}
		if major < 0 || minor < 0 || patch < 0 {
			t.Fatalf("parsed negative version component from %q: %d.%d.%d", version, major, minor, patch)
		}
	})
}

// FuzzSplitHostPort checks that defensive host:port splitting never panics on
// malformed host strings. When it succeeds, returned pieces should still be
// recognizable substrings of the original input.
func FuzzSplitHostPort(f *testing.F) {
	for _, seed := range []string{
		"host:3306",
		"host",
		"[::1]:3306",
		"[::1]",
		"[",
		"[::1",
		":",
		"::",
		"",
		"host:",
		"host:abc",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, hostport string) {
		host, port, err := splitHostPort(hostport)
		if err != nil {
			return
		}
		if host != "" && !strings.Contains(hostport, host) {
			t.Fatalf("host %q is not recognizable in input %q", host, hostport)
		}
		if port != "" && !strings.Contains(hostport, port) {
			t.Fatalf("port %q is not recognizable in input %q", port, hostport)
		}
	})
}

// FuzzExtractIntParam checks that pool integer extraction never panics on arbitrary
// keys or values. Parse errors must name the key so bad MySQL pool settings are
// actionable in configuration errors.
func FuzzExtractIntParam(f *testing.F) {
	for _, seed := range []struct {
		key string
		val string
	}{
		{"pool_max_conns", "10"},
		{"pool_max_conns", "-1"},
		{"pool_max_conns", "0x10"},
		{"pool_max_conns", "1.5"},
		{"pool_max_conns", "999999999999999999999999999999"},
		{"pool_max_conns", ""},
		{"pool_max_conns", "junk"},
	} {
		f.Add(seed.key, seed.val)
	}

	f.Fuzz(func(t *testing.T, key, val string) {
		q := url.Values{key: {val}}
		_, err := extractIntParam(q, key, 42)
		if err != nil && !strings.Contains(err.Error(), key) {
			t.Fatalf("error %q does not mention key %q", err.Error(), key)
		}
	})
}

// FuzzExtractDurationParam checks that pool duration extraction never panics on
// arbitrary keys or values. Parse errors must name the key so invalid lifetime
// settings can be traced back to the offending MySQL DSN parameter.
func FuzzExtractDurationParam(f *testing.F) {
	for _, seed := range []struct {
		key string
		val string
	}{
		{"pool_conn_max_lifetime", "1s"},
		{"pool_conn_max_lifetime", "1h30m"},
		{"pool_conn_max_lifetime", "0"},
		{"pool_conn_max_lifetime", ""},
		{"pool_conn_max_lifetime", "forever"},
		{"pool_conn_max_lifetime", "-1s"},
		{"pool_conn_max_lifetime", "999999999999999999999999h"},
	} {
		f.Add(seed.key, seed.val)
	}

	f.Fuzz(func(t *testing.T, key, val string) {
		q := url.Values{key: {val}}
		_, err := extractDurationParam(q, key, 42)
		if err != nil && !strings.Contains(err.Error(), key) {
			t.Fatalf("error %q does not mention key %q", err.Error(), key)
		}
	})
}

// FuzzCensorPassword checks that logging-safe URL rendering does not leak the
// password from the userinfo slot. If the same bytes appear elsewhere in the URL,
// that coincidence is ignored; the MySQL backend only promises to redact userinfo.
func FuzzCensorPassword(f *testing.F) {
	f.Add("mysql://phil:hunter2@db/ntfy")
	f.Add("mysql://phil@db/ntfy")
	f.Add("mysql://@db")
	f.Add("")
	f.Add("mysql://%75:%70%40%73%73@db/ntfy")

	f.Fuzz(func(t *testing.T, rawURL string) {
		u, err := url.Parse(rawURL)
		if err != nil {
			t.Skip()
		}
		password, hasPassword := u.User.Password()
		redacted := censorPassword(u)
		if !hasPassword || password == "" {
			return
		}
		// The same bytes can appear elsewhere in an arbitrary fuzzed URL, so inspect
		// the parsed userinfo slot instead of treating whole-URL substring matches as
		// leaks.
		redactedURL, err := url.Parse(redacted)
		if err != nil {
			t.Fatalf("redacted URL is not parseable: %q: %v", redacted, err)
		}
		if redactedPassword, ok := redactedURL.User.Password(); ok && redactedPassword == password {
			t.Fatalf("redacted URL still contains original password in userinfo: %q", redacted)
		}
	})
}

// FuzzRegisterTLSConfig_PEM checks that arbitrary sslrootcert file contents never
// panic TLS setup. Invalid files should fail with an error that points operators at
// PEM/sslrootcert, while valid cert chains may register successfully.
func FuzzRegisterTLSConfig_PEM(f *testing.F) {
	f.Add([]byte(fuzzSamplePEMCert))
	f.Add([]byte("definitely not a PEM"))
	f.Add([]byte(""))
	f.Add([]byte{0, 1, 2})
	f.Add([]byte("-----BEGIN CERTIFICATE-----\ngarbage\n-----END CERTIFICATE-----\n"))

	f.Fuzz(func(t *testing.T, pem []byte) {
		path := filepath.Join(t.TempDir(), "ca.pem")
		if err := os.WriteFile(path, pem, 0o600); err != nil {
			t.Fatalf("write PEM: %v", err)
		}
		_, err := registerTLSConfig("fuzzhost:3306", path, "fuzzhost")
		if err == nil {
			return
		}
		msg := err.Error()
		if !strings.Contains(msg, "PEM") && !strings.Contains(msg, "sslrootcert") {
			t.Fatalf("TLS error should mention PEM or sslrootcert, got %q", msg)
		}
	})
}

func stripNtfyMySQLParams(q url.Values) {
	q.Del("pool_max_conns")
	q.Del("pool_max_idle_conns")
	q.Del("pool_conn_max_lifetime")
	q.Del("pool_conn_max_idle_time")
	q.Del("sslrootcert")
	q.Del("sslservername")
}

// fuzzSamplePEMCert is a self-signed cert used purely to satisfy AppendCertsFromPEM
// in fuzz seeds. It is not used to verify anything live.
const fuzzSamplePEMCert = `-----BEGIN CERTIFICATE-----
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
