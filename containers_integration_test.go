//go:build integration

package dberrors_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mssql"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Container images under test. Override with DBERRORS_PG_IMAGE and
// DBERRORS_MSSQL_IMAGE to try another version.
const (
	defaultPGImage    = "postgres:18"
	defaultMSSQLImage = "mcr.microsoft.com/mssql/server:2022-latest"

	pgPassword    = "pg-test-password"
	mssqlPassword = "Dberrors-Test-2026"
)

// pgDSN and msDSN are the connection strings of the containers started in
// TestMain. msDSN targets master; tests switch to the test database.
var pgDSN, msDSN string

// TestMain starts one PostgreSQL and one SQL Server container for the whole
// package, runs the tests, and terminates the containers afterwards.
func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	pgC, msC, err := startContainers(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	terminate := func(c testcontainers.Container) {
		if c == nil {
			return
		}
		tctx, tcancel := context.WithTimeout(context.Background(), time.Minute)
		defer tcancel()
		if err := c.Terminate(tctx); err != nil {
			log.Printf("terminate container: %v", err)
		}
	}
	terminate(pgC)
	terminate(msC)
	os.Exit(code)
}

// startContainers starts both engines and records their connection strings.
// If the second container fails, the first is terminated before returning.
func startContainers(ctx context.Context) (*postgres.PostgresContainer, *mssql.MSSQLServerContainer, error) {
	pgImage := envOr("DBERRORS_PG_IMAGE", defaultPGImage)
	msImage := envOr("DBERRORS_MSSQL_IMAGE", defaultMSSQLImage)

	pgC, err := postgres.Run(ctx, pgImage,
		postgres.WithDatabase("postgres"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword(pgPassword),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("start postgres (%s): %w", pgImage, err)
	}
	pgDSN, err = pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = pgC.Terminate(ctx)
		return nil, nil, fmt.Errorf("postgres connection string: %w", err)
	}

	msC, err := mssql.Run(ctx, msImage,
		mssql.WithAcceptEULA(),
		mssql.WithPassword(mssqlPassword),
	)
	if err != nil {
		_ = pgC.Terminate(ctx)
		return nil, nil, fmt.Errorf("start sql server (%s): %w", msImage, err)
	}
	msDSN, err = msC.ConnectionString(ctx, "database=master", "encrypt=disable")
	if err != nil {
		_ = pgC.Terminate(ctx)
		_ = msC.Terminate(ctx)
		return nil, nil, fmt.Errorf("sql server connection string: %w", err)
	}

	return pgC, msC, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
