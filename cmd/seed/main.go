package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"golang.org/x/crypto/bcrypt"
	"log/slog"
	"os"
	"time"
)

var errConfig = errors.New("DATABASE_URL, DEMO_PASSWORD (12..72 bytes), DEMO_DATE (YYYY-MM-DD), DEMO_SEED required")
var errSeedMismatch = errors.New("seed version/date/seed mismatch: explicit reset required")

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		var pgErr *pgconn.PgError
		code := "non_postgres_error"
		if errors.As(err, &pgErr) {
			code = pgErr.Code
		}
		message := "migration/seed failed; inspect configuration and schema version"
		if errors.Is(err, errConfig) || errors.Is(err, errSeedMismatch) {
			message = err.Error()
		}
		slog.Error(message, "class", code)
		os.Exit(1)
	}
}
func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	dsn := os.Getenv("DATABASE_URL")
	password := os.Getenv("DEMO_PASSWORD")
	date := os.Getenv("DEMO_DATE")
	seed := os.Getenv("DEMO_SEED")
	if dsn == "" || len(password) < 12 || len(password) > 72 || seed == "" {
		return errConfig
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return errConfig
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	goose.SetLogger(migrationLog{})
	// Goose interpolates this SQL identifier; hyphens require explicit quoting.
	goose.SetTableName(`"pl-goose-db-version"`)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(33906)`); err != nil {
		return err
	}
	var currentVersion, currentDate, currentSeed string
	err = tx.QueryRowContext(ctx, `SELECT version,demo_date::text,demo_seed FROM "pl-seed-versions" LIMIT 1`).Scan(&currentVersion, &currentDate, &currentSeed)
	if err == nil {
		if currentVersion != "a1-v1" || currentDate != date || currentSeed != seed {
			return errSeedMismatch
		}
		if err = seedA3(ctx, tx, date, seed); err != nil {
			return err
		}
		slog.Info("seed unchanged; A3 fixtures ensured", "version", currentVersion)
		if err = tx.Commit(); err != nil {
			return err
		}
		if err = seedHistory(ctx, db, dsn, date, seed); err != nil {
			return err
		}
		return seedFull(ctx, db, date, seed)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for i := 1; i <= 21; i++ {
		kind := "branch"
		name := "Sube " + time.Date(2000, 1, i, 0, 0, 0, 0, time.UTC).Format("02")
		if i == 21 {
			kind = "warehouse"
			name = "Merkez depo"
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO "pl-locations"(id,name,kind) VALUES ($1,$2,$3)`, i, name, kind); err != nil {
			return err
		}
	}
	accounts := []struct {
		Email, Role string
		Location    int
	}{
		{"seed.yildiz@ddo-case.invalid", "pharmacist", 1},
		{"teknisyen@ddo-case.invalid", "technician", 1},
		{"depo@ddo-case.invalid", "warehouse", 21},
		{"mudur@ddo-case.invalid", "manager", 1},
		{"muhasebe@ddo-case.invalid", "accountant", 0},
		{"eczaci.b@ddo-case.invalid", "pharmacist", 2},
		{"simulator@ddo-case.invalid", "simulator", 0},
	}
	for i, a := range accounts {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO "pl-wy-users"(id,email,role,password_hash) VALUES ($1,$2,$3,$4)`, i+1, a.Email, a.Role, string(hash)); err != nil {
			return err
		}
		if a.Location != 0 {
			if _, err = tx.ExecContext(ctx, `INSERT INTO "pl-user-locations" VALUES ($1,$2)`, i+1, a.Location); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence('"pl-wy-users"','id'),7),setval(pg_get_serial_sequence('"pl-locations"','id'),21)`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO "pl-seed-versions" VALUES ('a1-v1',$1,$2)`, date, seed); err != nil {
		return err
	}
	if err = seedA3(ctx, tx, date, seed); err != nil {
		return err
	}
	if err = tx.Commit(); err == nil {
		slog.Info("seed ready", "version", "a1-v1", "users", 7, "locations", 21)
	}
	if err != nil {
		return err
	}
	if err = seedHistory(ctx, db, dsn, date, seed); err != nil {
		return err
	}
	return seedFull(ctx, db, date, seed)
}

// Migration errors are returned and classified; do not log raw SQL/DSN details.
type migrationLog struct{}

func (migrationLog) Printf(string, ...interface{}) { slog.Info("migration_progress") }
func (migrationLog) Fatalf(string, ...interface{}) { slog.Error("migration_fatal") }
