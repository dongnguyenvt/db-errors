package mssql

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"

	dberrors "github.com/dongnguyenvt/db-errors"

	gomssql "github.com/microsoft/go-mssqldb"
)

func serverErr(number int32, message string) gomssql.Error {
	return gomssql.Error{Number: number, Message: message, Class: 16, State: 1}
}

func TestClassifyNumbers(t *testing.T) {
	tests := []struct {
		number int32
		want   dberrors.Code
	}{
		{2627, dberrors.CodeUniqueViolation},
		{2601, dberrors.CodeUniqueViolation},
		{547, dberrors.CodeConstraintViolation},
		{515, dberrors.CodeNotNullViolation},
		{1205, dberrors.CodeDeadlock},
		{1222, dberrors.CodeLockTimeout},
		{3960, dberrors.CodeSerializationFailure},
		{2628, dberrors.CodeDataTooLong},
		{8152, dberrors.CodeDataTooLong},
		{8115, dberrors.CodeNumericOutOfRange},
		{208, dberrors.CodeUndefinedTable},
		{207, dberrors.CodeUndefinedColumn},
		{102, dberrors.CodeSyntaxError},
		{156, dberrors.CodeSyntaxError},
		{229, dberrors.CodePermissionDenied},
		{230, dberrors.CodePermissionDenied},
		{18456, dberrors.CodeAuthenticationFailed},
		{4060, dberrors.CodeAuthenticationFailed},
		{40197, dberrors.CodeConnectionFailure},
		{40613, dberrors.CodeConnectionFailure},
		{40501, dberrors.CodeResourceLimit},
		{49918, dberrors.CodeResourceLimit},
		{49919, dberrors.CodeResourceLimit},
		{49920, dberrors.CodeResourceLimit},
		{10928, dberrors.CodeResourceLimit},
		{10929, dberrors.CodeResourceLimit},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.number), func(t *testing.T) {
			got, ok := Classify(tt.number)
			if !ok || got != tt.want {
				t.Errorf("Classify(%d) = (%q, %v), want (%q, true)", tt.number, got, ok, tt.want)
			}
		})
	}
}

// Numbers that are not SQL Server error messages must stay unmapped. 10053 and
// 10054 are OLE DB data-conversion messages, and 10060 and 10061 do not exist
// in the SQL Server catalog. Winsock failures reach the client as network
// errors, not as mssql.Error, so they are handled through driver.ErrBadConn.
func TestClassifyUnmappedNumbers(t *testing.T) {
	for _, n := range []int32{0, 10053, 10054, 10060, 10061, 999999} {
		if code, ok := Classify(n); ok || code != "" {
			t.Errorf("Classify(%d) = (%q, %v), want (\"\", false)", n, code, ok)
		}
	}
}

func TestTranslateNil(t *testing.T) {
	if got := Translate(nil); got != nil {
		t.Errorf("Translate(nil) = %v, want nil", got)
	}
}

func TestTranslateServerErrorValue(t *testing.T) {
	src := serverErr(2627, "Violation of UNIQUE KEY constraint 'UQ_email'.")
	got := Translate(src)
	if got == nil {
		t.Fatal("Translate returned nil for a unique violation")
	}
	if got.Code != dberrors.CodeUniqueViolation {
		t.Errorf("Code = %q, want unique_violation", got.Code)
	}
	if got.Driver != Driver || got.NativeCode != "2627" {
		t.Errorf("Driver/NativeCode = %q/%q, want mssql/2627", got.Driver, got.NativeCode)
	}
	if got.Message != src.Message {
		t.Errorf("Message = %q, want %q", got.Message, src.Message)
	}
	var orig gomssql.Error
	if !errors.As(got, &orig) || orig.Number != 2627 {
		t.Errorf("original driver error not reachable through Unwrap")
	}
}

func TestTranslateServerErrorPointer(t *testing.T) {
	src := serverErr(1205, "Transaction was deadlocked")
	got := Translate(&src)
	if got == nil || got.Code != dberrors.CodeDeadlock {
		t.Fatalf("Translate(*Error) = %v, want deadlock", got)
	}
}

func TestTranslateNilPointerServerError(t *testing.T) {
	var nilPtr *gomssql.Error
	if got := Translate(nilPtr); got != nil {
		t.Errorf("Translate(typed nil pointer) = %v, want nil", got)
	}
}

func TestTranslateWrapped(t *testing.T) {
	src := serverErr(547, "The INSERT statement conflicted with the FOREIGN KEY constraint")
	wrapped := fmt.Errorf("insert order: %w", src)
	got := Translate(wrapped)
	if got == nil || got.Code != dberrors.CodeConstraintViolation {
		t.Fatalf("Translate(wrapped) = %v, want constraint violation", got)
	}
}

// Error 547 maps to the same code whatever its message says. The message is
// localised, so classification must not depend on it.
func TestTranslate547IgnoresMessage(t *testing.T) {
	messages := []string{
		"The INSERT statement conflicted with the FOREIGN KEY constraint",
		"The INSERT statement conflicted with the CHECK constraint",
		"Die INSERT-Anweisung steht in Konflikt mit der FOREIGN KEY-Einschränkung",
		"",
	}
	for _, m := range messages {
		got := Translate(serverErr(547, m))
		if got == nil || got.Code != dberrors.CodeConstraintViolation {
			t.Errorf("Translate(547, %q) = %v, want constraint violation", m, got)
		}
	}
}

func TestTranslateUnknownServerError(t *testing.T) {
	got := Translate(serverErr(999999, "unknown"))
	if got == nil || got.Code != dberrors.CodeUnknown || got.Driver != Driver || got.NativeCode != "999999" {
		t.Fatalf("Translate(unknown number) = %+v, want unknown with native code 999999", got)
	}
	if !errors.Is(got, dberrors.ErrUnknown) {
		t.Errorf("errors.Is(result, ErrUnknown) = false, want true")
	}
}

func TestTranslateBadConn(t *testing.T) {
	got := Translate(driver.ErrBadConn)
	if got == nil || got.Code != dberrors.CodeConnectionFailure || got.Driver != Driver {
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
	src := serverErr(208, "Invalid object name 'missing'.")
	got := a.Translate(src)
	if got == nil || got.Code != dberrors.CodeUndefinedTable {
		t.Fatalf("Adapter.Translate = %v, want undefined table", got)
	}
}

func TestEndToEndWithRoot(t *testing.T) {
	err := dberrors.Translate(serverErr(1222, "Lock request time out period exceeded."), Adapter{})
	if !errors.Is(err, dberrors.ErrLockTimeout) {
		t.Errorf("errors.Is(ErrLockTimeout) = false for %v", err)
	}
	if errors.Is(err, dberrors.ErrDeadlock) {
		t.Errorf("lock timeout must not match deadlock")
	}
}

func TestEndToEndAzureThrottling(t *testing.T) {
	err := dberrors.Translate(serverErr(40501, "The service is currently busy."), Adapter{})
	if !errors.Is(err, dberrors.ErrResourceLimit) {
		t.Errorf("errors.Is(ErrResourceLimit) = false for %v", err)
	}
}
