//go:build integration

// Integration tests run against real PostgreSQL 18 and SQL Server 2022
// containers, started by TestMain in containers_integration_test.go.
// Run them with:
//
//	go test -tags integration ./...
package dberrors_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	dberrors "db-errors"
	"db-errors/mssql"
	dbpgx "db-errors/pgx"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
)

const (
	pgDriver    = "pgx"
	mssqlDriver = "sqlserver"
	mssqlTestDB = "dberr_test"
	opTimeout   = 30 * time.Second
)

// engine describes one database under test and the dialect-specific pieces
// the shared scenarios need.
type engine struct {
	name       string
	driver     string
	dsn        string
	adapter    dberrors.Adapter
	driverName string // value expected in dberrors.Error.Driver
	// isolation is the level used to force a serialization conflict.
	isolation sql.IsolationLevel
	// lockTimeoutSQL sets a short lock wait on a session.
	lockTimeoutSQL string
}

var nameSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

// tableName derives a unique, safe table name for a (sub)test.
func tableName(t *testing.T) string {
	t.Helper()
	s := nameSanitizer.ReplaceAllString(strings.ToLower(t.Name()), "_")
	if len(s) > 80 {
		s = s[:80]
	}
	return "dberr_" + strings.Trim(s, "_")
}

// openReady opens a pool and retries Ping until the server accepts connections.
func openReady(t *testing.T, driver, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatalf("sql.Open(%s): %v", driver, err)
	}
	deadline := time.Now().Add(3 * time.Minute)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = db.PingContext(ctx)
		cancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = db.Close()
			t.Fatalf("%s not ready: %v", driver, err)
		}
		time.Sleep(time.Second)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// mssqlTestDSN creates the test database with snapshot isolation enabled and
// returns a DSN pointing at it.
func mssqlTestDSN(t *testing.T, base string) string {
	t.Helper()
	admin := openReady(t, mssqlDriver, base)
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()

	var exists int
	err := admin.QueryRowContext(ctx, "SELECT COUNT(*) FROM sys.databases WHERE name = @p1", mssqlTestDB).Scan(&exists)
	if err != nil {
		t.Fatalf("check test database: %v", err)
	}
	if exists == 0 {
		if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+mssqlTestDB); err != nil {
			t.Fatalf("create test database: %v", err)
		}
	}
	if _, err := admin.ExecContext(ctx, "ALTER DATABASE "+mssqlTestDB+" SET ALLOW_SNAPSHOT_ISOLATION ON"); err != nil {
		t.Fatalf("enable snapshot isolation: %v", err)
	}

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse MSSQL DSN: %v", err)
	}
	q := u.Query()
	q.Set("database", mssqlTestDB)
	u.RawQuery = q.Encode()
	return u.String()
}

func engines() []engine {
	return []engine{
		{
			name:           "postgres",
			driver:         pgDriver,
			dsn:            pgDSN,
			adapter:        dbpgx.Adapter{},
			driverName:     dbpgx.Driver,
			isolation:      sql.LevelRepeatableRead,
			lockTimeoutSQL: "SET lock_timeout = '200ms'",
		},
		{
			name:           "mssql",
			driver:         mssqlDriver,
			dsn:            msDSN, // replaced with the test database DSN in TestIntegration
			adapter:        mssql.Adapter{},
			driverName:     mssql.Driver,
			isolation:      sql.LevelSnapshot,
			lockTimeoutSQL: "SET LOCK_TIMEOUT 200",
		},
	}
}

// exec runs a statement and returns its error.
func exec(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, stmt string) error {
	_, err := q.ExecContext(ctx, stmt)
	return err
}

// scenario is one failing operation and the portable code it must produce.
type scenario struct {
	name  string
	setup []string // run before the failing statement; %[1]s is the table name
	// run performs the failing operation and returns its error.
	run  func(ctx context.Context, e engine, db *sql.DB, tbl string) error
	want error
	// mssqlWant overrides want on SQL Server when the server error number
	// cannot express the portable distinction (547 covers FK and CHECK).
	mssqlWant error
}

// stmt builds a scenario that runs a single statement after setup.
func stmt(name string, setup []string, sqlText string, want error) scenario {
	return scenario{
		name:  name,
		setup: setup,
		run: func(ctx context.Context, _ engine, db *sql.DB, tbl string) error {
			// ReplaceAll, not Sprintf: some statements have no table placeholder.
			return exec(ctx, db, strings.ReplaceAll(sqlText, "%[1]s", tbl))
		},
		want: want,
	}
}

// withMSSQLWant sets the SQL Server expectation for a scenario.
func withMSSQLWant(s scenario, want error) scenario {
	s.mssqlWant = want
	return s
}

func scenarios() []scenario {
	return []scenario{
		stmt("unique violation",
			[]string{"CREATE TABLE %[1]s (id INT PRIMARY KEY)", "INSERT INTO %[1]s (id) VALUES (1)"},
			"INSERT INTO %[1]s (id) VALUES (1)", dberrors.ErrUniqueViolation),

		withMSSQLWant(stmt("foreign key violation",
			[]string{"CREATE TABLE %[1]s_parent (id INT PRIMARY KEY)", "CREATE TABLE %[1]s (pid INT REFERENCES %[1]s_parent (id))"},
			"INSERT INTO %[1]s (pid) VALUES (99)", dberrors.ErrForeignKeyViolation), dberrors.ErrConstraintViolation),

		stmt("not null violation",
			[]string{"CREATE TABLE %[1]s (v INT NOT NULL)"},
			"INSERT INTO %[1]s (v) VALUES (NULL)", dberrors.ErrNotNullViolation),

		withMSSQLWant(stmt("check violation",
			[]string{"CREATE TABLE %[1]s (v INT CHECK (v > 0))"},
			"INSERT INTO %[1]s (v) VALUES (-1)", dberrors.ErrCheckViolation), dberrors.ErrConstraintViolation),

		stmt("data too long",
			[]string{"CREATE TABLE %[1]s (v VARCHAR(3))"},
			"INSERT INTO %[1]s (v) VALUES ('abcdef')", dberrors.ErrDataTooLong),

		stmt("numeric out of range",
			nil,
			"SELECT CAST(2147483647 AS INT) + 1", dberrors.ErrNumericOutOfRange),

		stmt("undefined table",
			nil,
			"SELECT * FROM %[1]s_missing", dberrors.ErrUndefinedTable),

		stmt("undefined column",
			[]string{"CREATE TABLE %[1]s (id INT)"},
			"SELECT nope_column FROM %[1]s", dberrors.ErrUndefinedColumn),

		stmt("syntax error",
			nil,
			"SELECT * FROM WHERE", dberrors.ErrSyntaxError),

		{
			name: "lock timeout",
			setup: []string{
				"CREATE TABLE %[1]s (id INT PRIMARY KEY, v INT)",
				"INSERT INTO %[1]s (id, v) VALUES (1, 0)",
			},
			run: func(ctx context.Context, e engine, db *sql.DB, tbl string) error {
				holder, err := db.BeginTx(ctx, nil)
				if err != nil {
					return fmt.Errorf("begin holder: %w", err)
				}
				defer func() { _ = holder.Rollback() }()
				if err := exec(ctx, holder, fmt.Sprintf("UPDATE %s SET v = v + 1 WHERE id = 1", tbl)); err != nil {
					return fmt.Errorf("holder update: %w", err)
				}

				// The lock timeout is a session setting, so the waiter gets its own
				// single-connection pool. Otherwise the setting would leak into
				// later scenarios that reuse a pooled connection.
				waiterDB, err := sql.Open(e.driver, e.dsn)
				if err != nil {
					return fmt.Errorf("open waiter pool: %w", err)
				}
				defer waiterDB.Close()
				waiterDB.SetMaxOpenConns(1)
				waiter, err := waiterDB.Conn(ctx)
				if err != nil {
					return fmt.Errorf("open waiter: %w", err)
				}
				defer waiter.Close()
				if err := exec(ctx, waiter, e.lockTimeoutSQL); err != nil {
					return fmt.Errorf("set lock timeout: %w", err)
				}
				return exec(ctx, waiter, fmt.Sprintf("UPDATE %s SET v = v + 1 WHERE id = 1", tbl))
			},
			want: dberrors.ErrLockTimeout,
		},

		{
			name: "serialization failure",
			setup: []string{
				"CREATE TABLE %[1]s (id INT PRIMARY KEY, v INT)",
				"INSERT INTO %[1]s (id, v) VALUES (1, 0)",
			},
			run: func(ctx context.Context, e engine, db *sql.DB, tbl string) error {
				tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: e.isolation})
				if err != nil {
					return fmt.Errorf("begin: %w", err)
				}
				defer func() { _ = tx.Rollback() }()
				// Establish the transaction's snapshot.
				var v int
				if err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT v FROM %s WHERE id = 1", tbl)).Scan(&v); err != nil {
					return fmt.Errorf("snapshot read: %w", err)
				}
				// A concurrent committed update to the same row.
				if err := exec(ctx, db, fmt.Sprintf("UPDATE %s SET v = v + 1 WHERE id = 1", tbl)); err != nil {
					return fmt.Errorf("concurrent update: %w", err)
				}
				return exec(ctx, tx, fmt.Sprintf("UPDATE %s SET v = v + 10 WHERE id = 1", tbl))
			},
			want: dberrors.ErrSerializationFailure,
		},

		{
			name: "deadlock",
			setup: []string{
				"CREATE TABLE %[1]s (id INT PRIMARY KEY, v INT)",
				"INSERT INTO %[1]s (id, v) VALUES (1, 0), (2, 0)",
			},
			run: func(ctx context.Context, _ engine, db *sql.DB, tbl string) error {
				a, err := db.BeginTx(ctx, nil)
				if err != nil {
					return fmt.Errorf("begin a: %w", err)
				}
				defer func() { _ = a.Rollback() }()
				b, err := db.BeginTx(ctx, nil)
				if err != nil {
					return fmt.Errorf("begin b: %w", err)
				}
				defer func() { _ = b.Rollback() }()

				// Each transaction locks one row, then reaches for the other.
				if err := exec(ctx, a, fmt.Sprintf("UPDATE %s SET v = 1 WHERE id = 1", tbl)); err != nil {
					return fmt.Errorf("a lock row 1: %w", err)
				}
				if err := exec(ctx, b, fmt.Sprintf("UPDATE %s SET v = 1 WHERE id = 2", tbl)); err != nil {
					return fmt.Errorf("b lock row 2: %w", err)
				}

				results := make(chan error, 2)
				go func() {
					results <- exec(ctx, a, fmt.Sprintf("UPDATE %s SET v = 2 WHERE id = 2", tbl))
				}()
				go func() {
					results <- exec(ctx, b, fmt.Sprintf("UPDATE %s SET v = 2 WHERE id = 1", tbl))
				}()

				// The server picks one victim; the other transaction succeeds.
				var deadlock error
				for i := 0; i < 2; i++ {
					if err := <-results; err != nil && deadlock == nil {
						deadlock = err
					}
				}
				return deadlock
			},
			want: dberrors.ErrDeadlock,
		},
	}
}

func TestIntegration(t *testing.T) {
	for _, e := range engines() {
		e := e
		if e.name == "mssql" {
			e.dsn = mssqlTestDSN(t, e.dsn)
		}
		t.Run(e.name, func(t *testing.T) {
			db := openReady(t, e.driver, e.dsn)

			for _, sc := range scenarios() {
				sc := sc
				t.Run(sc.name, func(t *testing.T) {
					tbl := tableName(t)
					ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
					defer cancel()

					dropAll(t, db, tbl)
					t.Cleanup(func() { dropAll(t, db, tbl) })

					for _, s := range sc.setup {
						if err := exec(ctx, db, fmt.Sprintf(s, tbl)); err != nil {
							t.Fatalf("setup %q: %v", s, err)
						}
					}

					want := sc.want
					if e.name == "mssql" && sc.mssqlWant != nil {
						want = sc.mssqlWant
					}
					err := sc.run(ctx, e, db, tbl)
					assertServerError(t, err, e, want)
				})
			}
		})
	}
}

// dropAll removes the scenario's tables, child before parent.
func dropAll(t *testing.T, db *sql.DB, tbl string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	for _, name := range []string{tbl, tbl + "_parent"} {
		if err := exec(ctx, db, "DROP TABLE IF EXISTS "+name); err != nil {
			t.Fatalf("drop %s: %v", name, err)
		}
	}
}

// assertTranslated checks that err translates to want, carries the engine's
// driver name, and still exposes the original driver error.
func assertTranslated(t *testing.T, err error, e engine, want error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error matching %v, got nil", want)
	}
	translated := dberrors.Translate(err, e.adapter)
	if !errors.Is(translated, want) {
		t.Fatalf("Translate(%v) = %v; want errors.Is %v", err, translated, want)
	}

	var de *dberrors.Error
	if !errors.As(translated, &de) {
		t.Fatalf("translated error is not *dberrors.Error: %T", translated)
	}
	if de.Driver != e.driverName {
		t.Errorf("Driver = %q, want %q", de.Driver, e.driverName)
	}
	if de.Err == nil {
		t.Errorf("original error not retained")
	}
	t.Logf("translated: %v", de)
}

// assertServerError is assertTranslated plus a check that the error came from
// the server and so carries a native SQLSTATE or error number.
func assertServerError(t *testing.T, err error, e engine, want error) {
	t.Helper()
	assertTranslated(t, err, e, want)
	var de *dberrors.Error
	errors.As(dberrors.Translate(err, e.adapter), &de)
	if de.NativeCode == "" {
		t.Errorf("NativeCode is empty for server error %v", de)
	}
}

// TestPostgresAuthenticationFailure uses a wrong password against the server.
func TestPostgresAuthenticationFailure(t *testing.T) {
	u, err := url.Parse(pgDSN)
	if err != nil {
		t.Fatalf("parse PG DSN: %v", err)
	}
	u.User = url.UserPassword(u.User.Username(), "wrong-password-for-test")

	db, err := sql.Open(pgDriver, u.String())
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()

	err = db.PingContext(ctx)
	assertServerError(t, err, engine{adapter: dbpgx.Adapter{}, driverName: dbpgx.Driver}, dberrors.ErrAuthenticationFailed)
}

// TestPostgresConnectionFailure dials a closed port.
func TestPostgresConnectionFailure(t *testing.T) {
	db, err := sql.Open(pgDriver, "postgres://postgres:x@127.0.0.1:1/postgres?sslmode=disable&connect_timeout=2")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()

	err = db.PingContext(ctx)
	assertTranslated(t, err, engine{adapter: dbpgx.Adapter{}, driverName: dbpgx.Driver}, dberrors.ErrConnectionFailure)
}

// TestMSSQLAuthenticationFailure uses a wrong password for sa.
func TestMSSQLAuthenticationFailure(t *testing.T) {
	u, err := url.Parse(msDSN)
	if err != nil {
		t.Fatalf("parse MSSQL DSN: %v", err)
	}
	u.User = url.UserPassword(u.User.Username(), "Wrong-Password-For-Test1")

	// Wait until the server is up so the failure is a login error, not a dial error.
	openReady(t, mssqlDriver, msDSN)

	db, err := sql.Open(mssqlDriver, u.String())
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()

	err = db.PingContext(ctx)
	assertTranslated(t, err, engine{adapter: mssql.Adapter{}, driverName: mssql.Driver}, dberrors.ErrAuthenticationFailed)
}
