package auth

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type Postgres struct{ Pool *pgxpool.Pool }

func (p Postgres) PoolStats() (int32, int32) {
	s := p.Pool.Stat()
	return s.TotalConns(), s.AcquiredConns()
}

func (p Postgres) Ping(ctx context.Context) error { return p.Pool.Ping(ctx) }
func (p Postgres) Credentials(ctx context.Context, email string) (User, string, error) {
	var u User
	var hash string
	err := p.Pool.QueryRow(ctx, `SELECT id::text,email,role,password_hash FROM "pl-wy-users" WHERE email=$1 AND active`, email).Scan(&u.ID, &u.Email, &u.Role, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrUnauthenticated
	}
	return u, hash, err
}
func (p Postgres) CreateSession(ctx context.Context, uid, token, csrf string, expires time.Time) error {
	_, err := p.Pool.Exec(ctx, `INSERT INTO "pl-sessions" (user_id,token_hash,csrf_hash,expires_at) VALUES ($1,$2,$3,$4)`, uid, token, csrf, expires)
	return err
}
func (p Postgres) Session(ctx context.Context, hash string) (Session, error) {
	var s Session
	err := p.Pool.QueryRow(ctx, `SELECT u.id::text,u.email,u.role,s.csrf_hash,s.expires_at FROM "pl-sessions" s JOIN "pl-wy-users" u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND u.active`, hash).Scan(&s.User.ID, &s.User.Email, &s.User.Role, &s.CSRFHash, &s.Expires)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrUnauthenticated
	}
	return s, err
}
func (p Postgres) Revoke(ctx context.Context, hash string) error {
	_, err := p.Pool.Exec(ctx, `UPDATE "pl-sessions" SET revoked_at=now() WHERE token_hash=$1`, hash)
	return err
}
func (p Postgres) Locations(ctx context.Context, uid string) ([]Location, error) {
	rows, err := p.Pool.Query(ctx, `SELECT l.id::text,l.name,l.kind FROM "pl-locations" l JOIN "pl-user-locations" a ON a.location_id=l.id WHERE a.user_id=$1 ORDER BY l.id`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	locations := []Location{}
	for rows.Next() {
		var l Location
		if err := rows.Scan(&l.ID, &l.Name, &l.Kind); err != nil {
			return nil, err
		}
		locations = append(locations, l)
	}
	return locations, rows.Err()
}
