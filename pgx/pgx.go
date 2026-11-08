// Package pgx translates PostgreSQL errors from pgx v5
// (github.com/jackc/pgx/v5) into portable dberrors codes.
//
// Classification uses SQLSTATE codes, which PostgreSQL keeps stable across
// versions, so the same mapping applies to PostgreSQL 18 and earlier.
package pgx

import (
	"database/sql/driver"
	"errors"
	"strings"

	dberrors "db-errors"

	pgconn "github.com/jackc/pgx/v5/pgconn"
)

// Driver is the name recorded in dberrors.Error.Driver for PostgreSQL errors.
const Driver = "pgx"

// Adapter implements dberrors.Adapter for PostgreSQL.
type Adapter struct{}

// Translate implements dberrors.Adapter.
func (Adapter) Translate(err error) *dberrors.Error {
	return Translate(err)
}

// Translate converts a pgx/pgconn error into a *dberrors.Error. It returns nil
// when err is not a recognised PostgreSQL error.
func Translate(err error) *dberrors.Error {
	if err == nil {
		return nil
	}

	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr != nil {
		code, ok := classify(pgErr.Code)
		if !ok {
			return nil
		}
		return &dberrors.Error{
			Code:       code,
			Driver:     Driver,
			NativeCode: pgErr.Code,
			Message:    pgErr.Message,
			Err:        err,
		}
	}

	if _, ok := errors.AsType[*pgconn.ConnectError](err); ok {
		return &dberrors.Error{
			Code:   dberrors.CodeConnectionFailure,
			Driver: Driver,
			Err:    err,
		}
	}

	if errors.Is(err, driver.ErrBadConn) {
		return &dberrors.Error{
			Code:   dberrors.CodeConnectionFailure,
			Driver: Driver,
			Err:    err,
		}
	}

	return nil
}

// classify maps a SQLSTATE code to a portable code.
func classify(sqlstate string) (dberrors.Code, bool) {
	switch sqlstate {
	case "23505":
		return dberrors.CodeUniqueViolation, true
	case "23503":
		return dberrors.CodeForeignKeyViolation, true
	case "23502":
		return dberrors.CodeNotNullViolation, true
	case "23514":
		return dberrors.CodeCheckViolation, true
	case "40P01":
		return dberrors.CodeDeadlock, true
	case "40001":
		return dberrors.CodeSerializationFailure, true
	case "55P03":
		return dberrors.CodeLockTimeout, true
	case "57014":
		return dberrors.CodeQueryCanceled, true
	case "57P01", "57P02", "57P03": // admin shutdown, crash shutdown, cannot connect now
		return dberrors.CodeConnectionFailure, true
	case "28P01", "28000":
		return dberrors.CodeAuthenticationFailed, true
	case "42501":
		return dberrors.CodePermissionDenied, true
	case "22001":
		return dberrors.CodeDataTooLong, true
	case "22003":
		return dberrors.CodeNumericOutOfRange, true
	case "42P01":
		return dberrors.CodeUndefinedTable, true
	case "42703":
		return dberrors.CodeUndefinedColumn, true
	case "42601":
		return dberrors.CodeSyntaxError, true
	}

	// Class 08: connection exception (08000, 08001, 08003, 08004, 08006, 08P01, ...).
	if strings.HasPrefix(sqlstate, "08") {
		return dberrors.CodeConnectionFailure, true
	}
	// Class 23: remaining integrity constraint violations (e.g. 23001 restrict).
	if strings.HasPrefix(sqlstate, "23") {
		return dberrors.CodeConstraintViolation, true
	}
	return "", false
}
