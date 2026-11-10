// Package mssql translates go-mssqldb (github.com/microsoft/go-mssqldb) errors
// into portable dberrors codes.
//
// Classification uses the SQL Server error number (mssql.Error.Number) only.
// Message text is localised by the server and is never inspected.
package mssql

import (
	"database/sql/driver"
	"errors"
	"strconv"

	dberrors "github.com/dongnguyenvt/db-errors"

	gomssql "github.com/microsoft/go-mssqldb"
)

// Driver is the name recorded in dberrors.Error.Driver for SQL Server errors.
const Driver = "mssql"

// Adapter implements dberrors.Adapter for SQL Server.
type Adapter struct{}

// Translate implements dberrors.Adapter.
func (Adapter) Translate(err error) *dberrors.Error {
	return Translate(err)
}

// Translate converts a go-mssqldb error into a *dberrors.Error. It returns nil
// when err is not a recognised SQL Server error.
func Translate(err error) *dberrors.Error {
	if err == nil {
		return nil
	}

	var serverErr gomssql.Error
	var serverErrPtr *gomssql.Error
	switch {
	case errors.As(err, &serverErr):
	case errors.As(err, &serverErrPtr) && serverErrPtr != nil:
		serverErr = *serverErrPtr
	default:
		// go-mssqldb reports dropped or refused connections as driver.ErrBadConn
		// rather than as a server error number.
		if errors.Is(err, driver.ErrBadConn) {
			return &dberrors.Error{
				Code:   dberrors.CodeConnectionFailure,
				Driver: Driver,
				Err:    err,
			}
		}
		return nil
	}

	code, ok := Classify(serverErr.Number)
	if !ok {
		return nil
	}
	return &dberrors.Error{
		Code:       code,
		Driver:     Driver,
		NativeCode: strconv.FormatInt(int64(serverErr.Number), 10),
		Message:    serverErr.Message,
		Err:        err,
	}
}

// Classify maps a SQL Server error number to a portable code. It returns false
// for numbers it does not recognise.
//
// Numbers are taken from Microsoft's "Database Engine events and errors"
// reference and the Azure SQL transient-fault table. Error 547 covers both
// foreign-key and check violations, which the number cannot tell apart, so it
// maps to the generic constraint code.
func Classify(number int32) (dberrors.Code, bool) {
	switch number {
	case 2627, 2601: // unique constraint / unique index
		return dberrors.CodeUniqueViolation, true
	case 547: // FOREIGN KEY, CHECK, or other constraint conflict
		return dberrors.CodeConstraintViolation, true
	case 515: // cannot insert NULL
		return dberrors.CodeNotNullViolation, true
	case 1205: // deadlock victim
		return dberrors.CodeDeadlock, true
	case 1222: // lock request time out
		return dberrors.CodeLockTimeout, true
	case 3960: // snapshot isolation update conflict
		return dberrors.CodeSerializationFailure, true
	case 2628, 8152: // string or binary data would be truncated
		return dberrors.CodeDataTooLong, true
	case 8115: // arithmetic overflow
		return dberrors.CodeNumericOutOfRange, true
	case 208:
		return dberrors.CodeUndefinedTable, true
	case 207:
		return dberrors.CodeUndefinedColumn, true
	case 102, 156:
		return dberrors.CodeSyntaxError, true
	case 229, 230:
		return dberrors.CodePermissionDenied, true
	case 18456, 4060: // login failed / cannot open requested database
		return dberrors.CodeAuthenticationFailed, true

	// Azure SQL transient faults: the connection or service was reconfigured
	// or failed over, so reconnecting is the remedy.
	case 40197, 40613:
		return dberrors.CodeConnectionFailure, true

	// Azure SQL throttling and resource governance.
	case 40501, 49918, 49919, 49920, 10928, 10929:
		return dberrors.CodeResourceLimit, true
	}
	return "", false
}
