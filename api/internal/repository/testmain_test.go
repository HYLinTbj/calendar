//go:build integration

package repository_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/hylin/calendar/api/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()

	pgContainer, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("calendar"),
		tcpostgres.WithUsername("calendar"),
		tcpostgres.WithPassword("calendar"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		panic("start postgres container: " + err.Error())
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic("get connection string: " + err.Error())
	}

	testPool, err = pgxpool.New(ctx, connStr)
	if err != nil {
		panic("connect to test db: " + err.Error())
	}

	if err := db.Migrate(ctx, testPool); err != nil {
		panic("migrate test db: " + err.Error())
	}

	code := m.Run()

	testPool.Close()
	_ = pgContainer.Terminate(ctx)
	os.Exit(code)
}
