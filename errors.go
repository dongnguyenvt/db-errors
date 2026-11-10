// Package dberrors maps driver-specific database errors (SQL Server via
// go-mssqldb, PostgreSQL via pgx) onto a single set of portable error codes.
//
// The root package has no driver dependencies. Each driver lives in its own
// adapter sub-package, so an application only pulls in the drivers it uses:
//
//	err = dberrors.Translate(err, mssql.Adapter{}, pgx.Adapter{})
//	if errors.Is(err, dberrors.ErrUniqueViolation) { ... }
package dberrors

import (
	"database/sql"
	"errors"
	"fmt"
)

// Code is a portable, driver-independent classification of a database error.
type Code string

// Portable error codes.
const (
	CodeUniqueViolation     Code = "unique_violation"
	CodeForeignKeyViolation Code = "foreign_key_violation"
	CodeNotNullViolation    Code = "not_null_violation"
	CodeCheckViolation      Code = "check_violation"
	// CodeConstraintViolation is a generic integrity violation whose exact
	// kind could not be determined from the native error.
	CodeConstraintViolation  Code = "constraint_violation"
	CodeDeadlock             Code = "deadlock"
	CodeSerializationFailure Code = "serialization_failure"
	CodeLockTimeout          Code = "lock_timeout"
	CodeQueryCanceled        Code = "query_canceled"
	CodeConnectionFailure    Code = "connection_failure"
	// CodeResourceLimit is a throttling or resource-governance rejection
	// (e.g. Azure SQL "service is busy" or worker limit reached). Usually
	// retryable after a delay.
	CodeResourceLimit        Code = "resource_limit"
	CodeAuthenticationFailed Code = "authentication_failed"
	CodePermissionDenied     Code = "permission_denied"
	CodeDataTooLong          Code = "data_too_long"
	CodeNumericOutOfRange    Code = "numeric_out_of_range"
	CodeUndefinedTable       Code = "undefined_table"
	CodeUndefinedColumn      Code = "undefined_column"
	CodeSyntaxError          Code = "syntax_error"
	// CodeUnknown is a database server error that the driver reported but
	// that this package has no mapping for. The native code is kept in
	// Error.NativeCode. Unknown errors are not retried automatically.
	CodeUnknown Code = "unknown"
	// CodeNotFound means a query that expected a row returned none. It is
	// produced from sql.ErrNoRows, which pgx and go-mssqldb also return.
	CodeNotFound Code = "not_found"
)

// Error is a translated database error. It keeps the original driver error
// available through Unwrap.
type Error struct {
	// Code is the portable classification.
	Code Code
	// Driver names the adapter that produced the error ("mssql" or "pgx").
	Driver string
	// NativeCode is the driver's own error code (SQLSTATE or SQL Server
	// error number), as a string.
	NativeCode string
	// Message is the server-provided message, if any.
	Message string
	// Err is the original, untranslated error.
	Err error
}

// Sentinel errors for use with errors.Is. A translated *Error matches a
// sentinel when their codes are equal, regardless of driver.
var (
	ErrUniqueViolation      = &Error{Code: CodeUniqueViolation}
	ErrForeignKeyViolation  = &Error{Code: CodeForeignKeyViolation}
	ErrNotNullViolation     = &Error{Code: CodeNotNullViolation}
	ErrCheckViolation       = &Error{Code: CodeCheckViolation}
	ErrConstraintViolation  = &Error{Code: CodeConstraintViolation}
	ErrDeadlock             = &Error{Code: CodeDeadlock}
	ErrSerializationFailure = &Error{Code: CodeSerializationFailure}
	ErrLockTimeout          = &Error{Code: CodeLockTimeout}
	ErrQueryCanceled        = &Error{Code: CodeQueryCanceled}
	ErrConnectionFailure    = &Error{Code: CodeConnectionFailure}
	ErrResourceLimit        = &Error{Code: CodeResourceLimit}
	ErrAuthenticationFailed = &Error{Code: CodeAuthenticationFailed}
	ErrPermissionDenied     = &Error{Code: CodePermissionDenied}
	ErrDataTooLong          = &Error{Code: CodeDataTooLong}
	ErrNumericOutOfRange    = &Error{Code: CodeNumericOutOfRange}
	ErrUndefinedTable       = &Error{Code: CodeUndefinedTable}
	ErrUndefinedColumn      = &Error{Code: CodeUndefinedColumn}
	ErrSyntaxError          = &Error{Code: CodeSyntaxError}
	ErrUnknown              = &Error{Code: CodeUnknown}
	ErrNotFound             = &Error{Code: CodeNotFound}
)

// Error implements the error interface.
func (e *Error) Error() string {
	s := string(e.Code)
	if e.Driver != "" {
		if e.NativeCode != "" {
			s += fmt.Sprintf(" (%s %s)", e.Driver, e.NativeCode)
		} else {
			s += fmt.Sprintf(" (%s)", e.Driver)
		}
	}
	switch {
	case e.Message != "":
		s += ": " + e.Message
	case e.Err != nil:
		s += ": " + e.Err.Error()
	}
	return s
}

// Unwrap returns the original driver error.
func (e *Error) Unwrap() error { return e.Err }

// Original returns the text of the untranslated driver error, which is what
// the error printed before translation. Use it to keep an existing message
// format in a wrapper. It returns "" when there is no original error.
func (e *Error) Original() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

// Is reports whether target is a *Error with the same Code. A target with an
// empty Driver matches errors from any driver, which is what the sentinels rely on.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok || t == nil {
		return false
	}
	return t.Code == e.Code && (t.Driver == "" || t.Driver == e.Driver)
}

// Adapter translates one driver's errors.
type Adapter interface {
	// Translate returns a *Error when err is a recognised error from the
	// adapter's driver, and nil otherwise.
	Translate(err error) *Error
}

// Translate converts err using the first adapter that recognises it.
//
// It returns nil for a nil err. If err is already a *Error, err is returned
// unchanged. An error that matches sql.ErrNoRows becomes CodeNotFound. If no
// adapter recognises any other error, err is returned unchanged.
func Translate(err error, adapters ...Adapter) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*Error](err); ok {
		return err
	}
	for _, a := range adapters {
		if a == nil {
			continue
		}
		if t := a.Translate(err); t != nil {
			return t
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return &Error{Code: CodeNotFound, Err: err}
	}
	return err
}
