package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const SessionDuration = 30 * 24 * time.Hour

type User struct {
	ID       [16]byte
	Username string
	IsAdmin  bool
}

type UserRow struct {
	ID        [16]byte
	Username  string
	IsAdmin   bool
	CreatedAt time.Time
}

func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func CreateUser(ctx context.Context, pool *pgxpool.Pool, username, password string, isAdmin bool) (User, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	var u User
	err = pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, is_admin) VALUES ($1, $2, $3)
		 RETURNING id, username, is_admin`,
		username, hash, isAdmin,
	).Scan(&u.ID, &u.Username, &u.IsAdmin)
	return u, err
}

func GetUserByUsername(ctx context.Context, pool *pgxpool.Pool, username string) (User, string, error) {
	var u User
	var hash string
	err := pool.QueryRow(ctx,
		`SELECT id, username, password_hash, is_admin FROM users WHERE username = $1`,
		username,
	).Scan(&u.ID, &u.Username, &hash, &u.IsAdmin)
	return u, hash, err
}

func GetUserBySession(ctx context.Context, pool *pgxpool.Pool, token string) (User, error) {
	var u User
	err := pool.QueryRow(ctx,
		`SELECT u.id, u.username, u.is_admin
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token = $1 AND s.expires_at > NOW()`,
		token,
	).Scan(&u.ID, &u.Username, &u.IsAdmin)
	if err == pgx.ErrNoRows {
		return User{}, pgx.ErrNoRows
	}
	return u, err
}

func CreateSession(ctx context.Context, pool *pgxpool.Pool, userID [16]byte) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO sessions (token, user_id, expires_at) VALUES ($1, $2, $3)`,
		token, userID, time.Now().Add(SessionDuration),
	)
	return token, err
}

func DeleteSession(ctx context.Context, pool *pgxpool.Pool, token string) error {
	_, err := pool.Exec(ctx, `DELETE FROM sessions WHERE token = $1`, token)
	return err
}

func EnsureAdmin(ctx context.Context, pool *pgxpool.Pool, username, password string) error {
	var count int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE is_admin = true`).Scan(&count)
	if count > 0 {
		return nil
	}
	_, err := CreateUser(ctx, pool, username, password, true)
	if err != nil {
		return err
	}
	slog.Info("admin created", "username", username)
	return nil
}

func ListUsers(ctx context.Context, pool *pgxpool.Pool) ([]UserRow, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, username, is_admin, created_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []UserRow
	for rows.Next() {
		var u UserRow
		if err := rows.Scan(&u.ID, &u.Username, &u.IsAdmin, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func DeleteUser(ctx context.Context, pool *pgxpool.Pool, id [16]byte) error {
	_, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	return err
}

func UpdateUser(ctx context.Context, pool *pgxpool.Pool, id [16]byte, newUsername, newPassword string, isAdmin *bool) error {
	if newUsername != "" {
		if _, err := pool.Exec(ctx, `UPDATE users SET username=$2 WHERE id=$1`, id, newUsername); err != nil {
			return err
		}
	}
	if newPassword != "" {
		hash, err := HashPassword(newPassword)
		if err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1`, id, hash); err != nil {
			return err
		}
	}
	if isAdmin != nil {
		if _, err := pool.Exec(ctx, `UPDATE users SET is_admin=$2 WHERE id=$1`, id, *isAdmin); err != nil {
			return err
		}
	}
	return nil
}
