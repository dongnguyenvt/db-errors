package pgx

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"
	"time"

	dberrors "db-errors"

	pgconn "github.com/jackc/pgx/v5/pgconn"
)

func pgErr(sqlstate, message string) *pgconn.PgError {
	return &pgconn.PgError{Code: sqlstate, Message: message, Severity: "ERROR"}
}

func TestClassifySQLState(t *testing.T) {
	tests := []struct {
		state string
		want  dberrors.Code
	}{
		{"23505", dberrors.CodeUniqueViolation},
		{"23503", dberrors.CodeForeignKeyViolation},
		{"23502", dberrors.CodeNotNullViolation},
		{"23514", dberrors.CodeCheckViolation},
		{"40P01", dberrors.CodeDeadlock},
		{"40001", dberrors.CodeSerializationFailure},
		{"55P03", dberrors.CodeLockTimeout},
		{"57014", dberrors.CodeQueryCanceled},
		{"57P01", dberrors.CodeConnectionFailure},
		{"57P02", dberrors.CodeConnectionFailure},
		{"57P03", dberrors.CodeConnectionFailure},
		{"28P01", dberrors.CodeAuthenticationFailed},
		{"28000", dberrors.CodeAuthenticationFailed},
		{"42501", dberrors.CodePermissionDenied},
		{"22001", dberrors.CodeDataTooLong},
		{"22003", dberrors.CodeNumericOutOfRange},
		{"42P01", dberrors.CodeUndefinedTable},
		{"42703", dberrors.CodeUndefinedColumn},
		{"42601", dberrors.CodeSyntaxError},
		// Class fallbacks.
		{"08000", dberrors.CodeConnectionFailure},
		{"08003", dberrors.CodeConnectionFailure},
		{"08006", dberrors.CodeConnectionFailure},
		{"08P01", dberrors.CodeConnectionFailure},
		{"23001", dberrors.CodeConstraintViolation},
		{"23P01", dberrors.CodeConstraintViolation}, // exclusion_violation
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			got, ok := classify(tt.state)
			if !ok || got != tt.want {
				t.Errorf("classify(%q) = (%q, %v), want (%q, true)", tt.state, got, ok, tt.want)
			}
		})
	}
}

func TestClassifyUnknownSQLState(t *testing.T) {
	for _, state := range []string{"", "XX000", "42000", "99999", "0A000"} {
		if code, ok := classify(state); ok || code != "" {
			t.Errorf("classify(%q) = (%q, %v), want (\"\", false)", state, code, ok)
		}
	}
}

func TestTranslateNil(t *testing.T) {
	if got := Translate(nil); got != nil {
		t.Errorf("Translate(nil) = %v, want nil", got)
	}
}

func TestTranslatePgError(t *testing.T) {
	src := pgErr("23505", `duplicate key value violates unique constraint "users_email_key"`)
	got := Translate(src)
	if got == nil {
		t.Fatal("Translate returned nil for a unique violation")
	}
	if got.Code != dberrors.CodeUniqueViolation {
		t.Errorf("Code = %q, want unique_violation", got.Code)
	}
	if got.Driver != Driver || got.NativeCode != "23505" {
		t.Errorf("Driver/NativeCode = %q/%q, want pgx/23505", got.Driver, got.NativeCode)
	}
	if got.Message != src.Message {
		t.Errorf("Message = %q, want %q", got.Message, src.Message)
	}
	var orig *pgconn.PgError
	if !errors.As(got, &orig) || orig.Code != "23505" {
		t.Error("original PgError not reachable through Unwrap")
	}
}

func TestTranslateWrappedPgError(t *testing.T) {
	wrapped := fmt.Errorf("save user: %w", pgErr("40P01", "deadlock detected"))
	got := Translate(wrapped)
	if got == nil || got.Code != dberrors.CodeDeadlock {
		t.Fatalf("Translate(wrapped) = %v, want deadlock", got)
	}
}

func TestTranslateUnknownPgError(t *testing.T) {
	if got := Translate(pgErr("XX000", "internal")); got != nil {
		t.Errorf("Translate(unknown SQLSTATE) = %v, want nil", got)
	}
}

func TestTranslateConnectError(t *testing.T) {
	// Dialing a closed local port produces a real *pgconn.ConnectError.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := pgconn.Connect(ctx, "postgres://u:p@127.0.0.1:1/db?connect_timeout=1&sslmode=disable")
	if err == nil {
		t.Fatal("expected connection error from closed port")
	}
	got := Translate(err)
	if got == nil || got.Code != dberrors.CodeConnectionFailure || got.Driver != Driver {
		t.Fatalf("Translate(connect error) = %v, want connection failure", got)
	}
}

func TestTranslateBadConn(t *testing.T) {
	got := Translate(driver.ErrBadConn)
	if got == nil || got.Code != dberrors.CodeConnectionFailure {
		t.Fatalf("Translate(ErrBadConn) = %v, want connection failure", got)
	}
	if !errors.Is(got, driver.ErrBadConn) {
		t.Error("translated bad-connection error should wrap driver.ErrBadConn")
	}
}

func TestTranslateUnrelatedError(t *testing.T) {
	if got := Translate(errors.New("something else")); got != nil {
		t.Errorf("Translate(unrelated) = %v, want nil", got)
	}
}

func TestAdapterImplementsInterface(t *testing.T) {
	var a dberrors.Adapter = Adapter{}
	got := a.Translate(pgErr("42P01", `relation "missing" does not exist`))
	if got == nil || got.Code != dberrors.CodeUndefinedTable {
		t.Fatalf("Adapter.Translate = %v, want undefined table", got)
	}
}

func TestEndToEndWithRoot(t *testing.T) {
	err := dberrors.Translate(pgErr("23503", "insert violates foreign key"), Adapter{})
	if !errors.Is(err, dberrors.ErrForeignKeyViolation) {
		t.Errorf("errors.Is(ErrForeignKeyViolation) = false for %v", err)
	}
	if errors.Is(err, dberrors.ErrUniqueViolation) {
		t.Errorf("foreign key violation must not match unique violation")
	}
}
