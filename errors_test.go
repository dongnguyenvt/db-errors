package dberrors

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
)

// stubAdapter returns a fixed result and records how often it was called.
type stubAdapter struct {
	result *Error
	calls  int
}

func (s *stubAdapter) Translate(err error) *Error {
	s.calls++
	return s.result
}

func TestCodeValues(t *testing.T) {
	// Codes are persisted and compared as strings by consumers, so their
	// spelling is part of the public contract.
	want := map[Code]string{
		CodeUniqueViolation:      "unique_violation",
		CodeForeignKeyViolation:  "foreign_key_violation",
		CodeNotNullViolation:     "not_null_violation",
		CodeCheckViolation:       "check_violation",
		CodeConstraintViolation:  "constraint_violation",
		CodeDeadlock:             "deadlock",
		CodeSerializationFailure: "serialization_failure",
		CodeLockTimeout:          "lock_timeout",
		CodeQueryCanceled:        "query_canceled",
		CodeConnectionFailure:    "connection_failure",
		CodeResourceLimit:        "resource_limit",
		CodeAuthenticationFailed: "authentication_failed",
		CodePermissionDenied:     "permission_denied",
		CodeDataTooLong:          "data_too_long",
		CodeNumericOutOfRange:    "numeric_out_of_range",
		CodeUndefinedTable:       "undefined_table",
		CodeUndefinedColumn:      "undefined_column",
		CodeSyntaxError:          "syntax_error",
	}
	for code, s := range want {
		if string(code) != s {
			t.Errorf("code %q: got spelling %q", s, string(code))
		}
	}
}

func TestErrorString(t *testing.T) {
	tests := []struct {
		name string
		err  *Error
		want string
	}{
		{
			name: "code only",
			err:  &Error{Code: CodeDeadlock},
			want: "deadlock",
		},
		{
			name: "driver without native code",
			err:  &Error{Code: CodeConnectionFailure, Driver: "pgx"},
			want: "connection_failure (pgx)",
		},
		{
			name: "driver with native code and message",
			err:  &Error{Code: CodeUniqueViolation, Driver: "mssql", NativeCode: "2627", Message: "dup key"},
			want: "unique_violation (mssql 2627): dup key",
		},
		{
			name: "message absent falls back to wrapped error",
			err:  &Error{Code: CodeDataTooLong, Driver: "pgx", NativeCode: "22001", Err: errors.New("raw failure")},
			want: "data_too_long (pgx 22001): raw failure",
		},
		{
			name: "no message and no wrapped error",
			err:  &Error{Code: CodeSyntaxError, Driver: "mssql", NativeCode: "102"},
			want: "syntax_error (mssql 102)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestErrorUnwrap(t *testing.T) {
	inner := errors.New("inner")
	e := &Error{Code: CodeDeadlock, Err: inner}
	if got := errors.Unwrap(e); got != inner {
		t.Errorf("Unwrap() = %v, want %v", got, inner)
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should reach the wrapped driver error")
	}
	if got := (&Error{Code: CodeDeadlock}).Unwrap(); got != nil {
		t.Errorf("Unwrap() with no Err = %v, want nil", got)
	}
}

func TestErrorIsSentinel(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		target error
		want   bool
	}{
		{"same code, any driver matches sentinel", &Error{Code: CodeUniqueViolation, Driver: "pgx"}, ErrUniqueViolation, true},
		{"same code, other driver matches sentinel", &Error{Code: CodeUniqueViolation, Driver: "mssql"}, ErrUniqueViolation, true},
		{"different code does not match", &Error{Code: CodeForeignKeyViolation, Driver: "pgx"}, ErrUniqueViolation, false},
		{"wrapped error still matches", fmt.Errorf("repo: %w", &Error{Code: CodeDeadlock, Driver: "mssql"}), ErrDeadlock, true},
		{"sentinel matches a driver-specific target", &Error{Code: CodeLockTimeout, Driver: "pgx"}, &Error{Code: CodeLockTimeout, Driver: "pgx"}, true},
		{"driver-specific target rejects other driver", &Error{Code: CodeLockTimeout, Driver: "mssql"}, &Error{Code: CodeLockTimeout, Driver: "pgx"}, false},
		{"non-dberrors target does not match", &Error{Code: CodeDeadlock}, errors.New("deadlock"), false},
		{"nil target does not match", &Error{Code: CodeDeadlock}, (*Error)(nil), false},
		{"sentinel target via plain error", errors.New("boom"), ErrDeadlock, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errors.Is(tt.err, tt.target); got != tt.want {
				t.Errorf("errors.Is = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSentinelsHaveCodes(t *testing.T) {
	sentinels := map[string]*Error{
		"ErrUniqueViolation":      ErrUniqueViolation,
		"ErrForeignKeyViolation":  ErrForeignKeyViolation,
		"ErrNotNullViolation":     ErrNotNullViolation,
		"ErrCheckViolation":       ErrCheckViolation,
		"ErrConstraintViolation":  ErrConstraintViolation,
		"ErrDeadlock":             ErrDeadlock,
		"ErrSerializationFailure": ErrSerializationFailure,
		"ErrLockTimeout":          ErrLockTimeout,
		"ErrQueryCanceled":        ErrQueryCanceled,
		"ErrConnectionFailure":    ErrConnectionFailure,
		"ErrResourceLimit":        ErrResourceLimit,
		"ErrAuthenticationFailed": ErrAuthenticationFailed,
		"ErrPermissionDenied":     ErrPermissionDenied,
		"ErrDataTooLong":          ErrDataTooLong,
		"ErrNumericOutOfRange":    ErrNumericOutOfRange,
		"ErrUndefinedTable":       ErrUndefinedTable,
		"ErrUndefinedColumn":      ErrUndefinedColumn,
		"ErrSyntaxError":          ErrSyntaxError,
	}
	for name, s := range sentinels {
		if s.Code == "" {
			t.Errorf("%s has an empty code", name)
		}
		if s.Driver != "" || s.Err != nil {
			t.Errorf("%s should carry only a code", name)
		}
	}
}

func TestTranslateNil(t *testing.T) {
	if got := Translate(nil); got != nil {
		t.Errorf("Translate(nil) = %v, want nil", got)
	}
	if got := Translate(nil, &stubAdapter{result: &Error{Code: CodeDeadlock}}); got != nil {
		t.Errorf("Translate(nil, adapter) = %v, want nil", got)
	}
}

func TestTranslateNoAdapters(t *testing.T) {
	orig := errors.New("plain")
	if got := Translate(orig); got != orig {
		t.Errorf("Translate with no adapters = %v, want original", got)
	}
}

func TestTranslateUsesFirstMatchingAdapter(t *testing.T) {
	first := &stubAdapter{result: &Error{Code: CodeDeadlock, Driver: "first"}}
	second := &stubAdapter{result: &Error{Code: CodeLockTimeout, Driver: "second"}}

	got := Translate(errors.New("x"), first, second)
	var de *Error
	if !errors.As(got, &de) || de.Driver != "first" {
		t.Fatalf("Translate = %v, want result of first adapter", got)
	}
	if second.calls != 0 {
		t.Errorf("second adapter called %d times, want 0", second.calls)
	}
}

func TestTranslateFallsThroughNonMatchingAdapters(t *testing.T) {
	miss := &stubAdapter{result: nil}
	hit := &stubAdapter{result: &Error{Code: CodeNotNullViolation, Driver: "hit"}}

	orig := errors.New("x")
	got := Translate(orig, miss, hit)
	var de *Error
	if !errors.As(got, &de) || de.Code != CodeNotNullViolation {
		t.Fatalf("Translate = %v, want not-null violation", got)
	}
	if miss.calls != 1 || hit.calls != 1 {
		t.Errorf("calls: miss=%d hit=%d, want 1 and 1", miss.calls, hit.calls)
	}
}

func TestTranslateReturnsOriginalWhenNoMatch(t *testing.T) {
	orig := errors.New("unknown")
	got := Translate(orig, &stubAdapter{result: nil})
	if got != orig {
		t.Errorf("Translate = %v, want original error", got)
	}
}

func TestTranslateSkipsNilAdapter(t *testing.T) {
	hit := &stubAdapter{result: &Error{Code: CodeCheckViolation}}
	got := Translate(errors.New("x"), nil, hit)
	if !errors.Is(got, ErrCheckViolation) {
		t.Errorf("Translate = %v, want check violation", got)
	}
}

func TestTranslateLeavesAlreadyTranslatedErrors(t *testing.T) {
	translated := &Error{Code: CodeDeadlock, Driver: "mssql"}
	stub := &stubAdapter{result: &Error{Code: CodeLockTimeout}}

	if got := Translate(translated, stub); got != translated {
		t.Errorf("Translate = %v, want the already-translated error unchanged", got)
	}
	if stub.calls != 0 {
		t.Errorf("adapter called %d times for an already-translated error, want 0", stub.calls)
	}

	wrapped := fmt.Errorf("layer: %w", translated)
	if got := Translate(wrapped, stub); got != wrapped {
		t.Errorf("Translate on wrapped translated error = %v, want unchanged", got)
	}
}

func TestTranslateNoRowsIsNotFound(t *testing.T) {
	got := Translate(fmt.Errorf("lookup: %w", sql.ErrNoRows))
	var de *Error
	if !errors.As(got, &de) || de.Code != CodeNotFound {
		t.Fatalf("Translate(ErrNoRows) = %v, want not_found", got)
	}
	if !errors.Is(got, ErrNotFound) {
		t.Errorf("errors.Is(result, ErrNotFound) = false, want true")
	}
	if !errors.Is(got, sql.ErrNoRows) {
		t.Errorf("errors.Is(result, sql.ErrNoRows) = false, want true")
	}
}

func TestTranslateNoRowsAdapterFirst(t *testing.T) {
	// An adapter that recognises the error wins over the generic not-found mapping.
	want := &Error{Code: CodeDeadlock, Driver: "stub"}
	got := Translate(sql.ErrNoRows, &stubAdapter{result: want})
	if got != want {
		t.Errorf("Translate = %v, want adapter result", got)
	}
}

func TestOriginalReturnsDriverText(t *testing.T) {
	e := &Error{Code: CodeUniqueViolation, Driver: "mssql", NativeCode: "2627", Message: "translated", Err: errors.New("raw driver text")}
	if got := e.Original(); got != "raw driver text" {
		t.Errorf("Original() = %q, want %q", got, "raw driver text")
	}
	if got := (&Error{Code: CodeDeadlock}).Original(); got != "" {
		t.Errorf("Original() with no Err = %q, want empty", got)
	}
}
