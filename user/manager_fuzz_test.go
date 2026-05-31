package user

import (
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
)

func FuzzIsUniqueConstraintError(f *testing.F) {
	f.Add("")
	f.Add("UNIQUE constraint failed: user.name")
	f.Add("Error 1062 (23000): Duplicate entry 'phil' for key 'user.PRIMARY'")
	f.Add("pq: duplicate key value violates unique constraint \"user_pkey\"")
	f.Add("random unrelated error")
	f.Add("\x00\x01\x02")
	f.Add("contains Error 1062 mid-string buried in noise")
	f.Add(strings.Repeat("x", 1024))

	f.Fuzz(func(t *testing.T, msg string) {
		hash := fnv.New32a()
		_, _ = hash.Write([]byte(msg))
		fuzzedNumber := uint16(hash.Sum32())

		// Bare errors may or may not be unique-constraint errors depending on their text, but must never panic.
		_ = isUniqueConstraintError(errors.New(msg))

		// Wrapped errors may or may not be unique-constraint errors depending on their text, but must never panic.
		_ = isUniqueConstraintError(fmt.Errorf("wrap: %w", errors.New(msg)))

		// MySQL error 1062 is the duplicate-entry code and must always be classified as a unique-constraint error.
		if !isUniqueConstraintError(&mysql.MySQLError{Number: 1062, Message: msg}) {
			t.Fatalf("typed MySQL 1062 was not classified as unique constraint error; msg=%q", msg)
		}

		// MySQL error 1064 is a syntax error and must never be classified as a unique-constraint error.
		if isUniqueConstraintError(&mysql.MySQLError{Number: 1064, Message: msg}) {
			t.Fatalf("typed MySQL 1064 was incorrectly classified as unique constraint error; msg=%q", msg)
		}

		// Arbitrary MySQL error codes may or may not be unique-constraint errors, but must never panic.
		_ = isUniqueConstraintError(&mysql.MySQLError{Number: fuzzedNumber, Message: msg})

		// PostgreSQL SQLSTATE 23505 is unique_violation and must always be classified as a unique-constraint error.
		if !isUniqueConstraintError(&pq.Error{Code: "23505", Message: msg}) {
			t.Fatalf("typed PostgreSQL 23505 was not classified as unique constraint error; msg=%q", msg)
		}

		// PostgreSQL SQLSTATE 42703 is undefined_column and must never be classified as a unique-constraint error.
		if isUniqueConstraintError(&pq.Error{Code: "42703", Message: msg}) {
			t.Fatalf("typed PostgreSQL 42703 was incorrectly classified as unique constraint error; msg=%q", msg)
		}

		// A nil error cannot represent a unique-constraint violation and must never panic.
		if isUniqueConstraintError(nil) {
			t.Fatalf("nil error was incorrectly classified as unique constraint error")
		}
	})
}
