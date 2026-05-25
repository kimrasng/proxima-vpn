package database

import (
	"context"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

// SeedAdmin creates the initial admin account if no admins exist.
// Returns without error if an admin already exists.
func SeedAdmin(ctx context.Context, pool *pgxpool.Pool) error {
	var count int
	err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM admins").Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		log.Println("admin account already exists, skipping seed")
		return nil
	}

	email := os.Getenv("ADMIN_EMAIL")
	if email == "" {
		email = "admin@example.com"
	}

	password := os.Getenv("ADMIN_PASSWORD")
	generatedPassword := false
	if password == "" {
		password = crypto.GenerateRandomPassword()
		generatedPassword = true
	}

	hash, err := crypto.HashPassword(password)
	if err != nil {
		return err
	}

	_, err = pool.Exec(ctx,
		"INSERT INTO admins (email, password_hash) VALUES ($1, $2)",
		email, hash,
	)
	if err != nil {
		return err
	}

	log.Println("========================================")
	log.Println("  INITIAL ADMIN ACCOUNT CREATED")
	log.Printf("  Email:    %s", email)
	if generatedPassword {
		log.Printf("  Password: %s", password)
		log.Println("  Please change this password immediately!")
	} else {
		log.Println("  Password: (set via ADMIN_PASSWORD env var)")
	}
	log.Println("========================================")

	return nil
}
