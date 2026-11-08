# db-errors

Translates SQL Server (`go-mssqldb`) and PostgreSQL (`pgx` v5) errors into one
set of portable error codes, so application code can branch on
`errors.Is(err, dberrors.ErrUniqueViolation)` regardless of the database.

The root package has no driver dependencies. Each driver has its own adapter
sub-package, so you only import the drivers you use.

## Usage

```go
import (
    "errors"

    dberrors "db-errors"
    "db-errors/mssql"
    dbpgx "db-errors/pgx"
)

err := db.ExecContext(ctx, query, args...)
err = dberrors.Translate(err, mssql.Adapter{}, dbpgx.Adapter{})

switch {
case errors.Is(err, dberrors.ErrUniqueViolation):
    // duplicate key
case errors.Is(err, dberrors.ErrDeadlock), errors.Is(err, dberrors.ErrSerializationFailure):
    // transient: safe to retry the transaction
}

var de *dberrors.Error
if errors.As(err, &de) {
    log.Printf("code=%s driver=%s native=%s", de.Code, de.Driver, de.NativeCode)
}
```

`Translate` returns `nil` for `nil`, returns the input unchanged when no adapter
recognises it, and never double-translates an error it has already converted.
The original driver error is kept and reachable through `errors.Unwrap`.

## Portable codes

| Code | Sentinel | PostgreSQL (SQLSTATE) | SQL Server (error number) |
|---|---|---|---|
| `unique_violation` | `ErrUniqueViolation` | 23505 | 2627, 2601 |
| `foreign_key_violation` | `ErrForeignKeyViolation` | 23503 | — (547 is generic, see below) |
| `not_null_violation` | `ErrNotNullViolation` | 23502 | 515 |
| `check_violation` | `ErrCheckViolation` | 23514 | — (547 is generic, see below) |
| `constraint_violation` | `ErrConstraintViolation` | other 23xxx | 547 |
| `deadlock` | `ErrDeadlock` | 40P01 | 1205 |
| `serialization_failure` | `ErrSerializationFailure` | 40001 | 3960 |
| `lock_timeout` | `ErrLockTimeout` | 55P03 | 1222 |
| `query_canceled` | `ErrQueryCanceled` | 57014 | — |
| `connection_failure` | `ErrConnectionFailure` | 08xxx, 57P01–57P03, connect errors | 40197, 40613, `driver.ErrBadConn` |
| `resource_limit` | `ErrResourceLimit` | — | 40501, 49918, 49919, 49920, 10928, 10929 |
| `authentication_failed` | `ErrAuthenticationFailed` | 28P01, 28000 | 18456, 4060 |
| `permission_denied` | `ErrPermissionDenied` | 42501 | 229, 230 |
| `data_too_long` | `ErrDataTooLong` | 22001 | 8152, 2628 |
| `numeric_out_of_range` | `ErrNumericOutOfRange` | 22003 | 8115 |
| `undefined_table` | `ErrUndefinedTable` | 42P01 | 208 |
| `undefined_column` | `ErrUndefinedColumn` | 42703 | 207 |
| `syntax_error` | `ErrSyntaxError` | 42601 | 102, 156 |

SQL Server error 547 covers foreign-key, check, and other constraint conflicts.
The error number cannot tell them apart, and the message text is localised, so
the SQL Server adapter maps 547 to `constraint_violation` and does not parse
messages. Use PostgreSQL if you need to distinguish foreign-key from check
violations.

Azure SQL transient faults (40197, 40613) map to `connection_failure`, and
throttling and resource-governance errors (40501, 49918, 49919, 49920, 10928,
10929) map to `resource_limit`. Retry both kinds of error.

`query_canceled` is only produced from PostgreSQL. SQL Server reports
cancellation through `context` errors, which are not database errors.

Unrecognised errors are left unchanged. Only errors with a mapped native code
are translated.

## Tests

```sh
# Unit tests (no Docker needed)
go test -cover ./...

# Integration tests: start PostgreSQL 18 and SQL Server 2022 containers via
# testcontainers-go, then run the tagged suite. Requires a running Docker daemon.
go test -tags integration -count=1 ./...
```

The unit tests build driver error values directly and run a dial to a closed
port for the PostgreSQL connection-error path. They cover every mapping and
every branch of the adapters.

The integration tests (`integration_test.go`, build tag `integration`) start
one container per engine in `TestMain` (`containers_integration_test.go`) using
the `postgres` and `mssql` testcontainers modules, and terminate them when the
run finishes. They trigger each mapped code on a real server, including real
lock waits, deadlocks, and snapshot isolation conflicts, plus authentication
failures and a refused connection. Set `DBERRORS_PG_IMAGE` or
`DBERRORS_MSSQL_IMAGE` to test another image version.

Not covered by the integration tests: `permission_denied` on either engine
and `query_canceled` on PostgreSQL. Both are covered by the unit tests only.
