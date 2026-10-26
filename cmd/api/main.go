package main

import (
	"context"
	"eczanet/internal/auth"
	"eczanet/internal/platform"
	"eczanet/internal/stock"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) > 1 && os.Args[1] == "probe" {
		client := http.Client{Timeout: 2 * time.Second}
		resp, err := client.Get("http://127.0.0.1:8080/health")
		if err != nil {
			os.Exit(1)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	if err := run(log); err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}
func run(log *slog.Logger) error {
	c, err := platform.ReadConfig()
	if err != nil {
		return err
	}
	pc, err := pgxpool.ParseConfig(c.DatabaseURL)
	if err != nil {
		return errors.New("veritabani yapilandirmasi gecersiz")
	}
	pc.MaxConns = c.PoolMax
	pc.ConnConfig.ConnectTimeout = time.Second
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return errors.New("veritabani havuzu acilamadi")
	}
	defer pool.Close()
	ready, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = pool.Ping(ready)
	cancel()
	if err != nil {
		return errors.New("veritabani hazir degil")
	}
	app := platform.NewServer(c, auth.Postgres{Pool: pool}, log)
	app.Stock = &stock.Service{Pool: pool, LockTimeout: c.StockLockTimeout, StatementTimeout: c.StockStatementTimeout}
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweep, cancel := context.WithTimeout(ctx, 8*time.Second)
				if e := app.Stock.ExpireCounts(sweep); e != nil && ctx.Err() == nil {
					log.Warn("count_expiry_sweep_failed")
				}
				cancel()
			}
		}
	}()
	srv := http.Server{Addr: c.Address, Handler: app.Handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: c.RequestTimeout, WriteTimeout: c.RequestTimeout + time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), c.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		_ = srv.Close()
		return errors.New("HTTP kapanis zaman asimi")
	}
	log.Info("shutdown_complete")
	return nil
}
