// Command createuser provisions a password-auth account in Redis.
//
// Usage:
//
//	REDIS_URL=redis://localhost:6379 \
//	  go run ./cmd/createuser \
//	    -email user@example.com \
//	    -name "Alice" \
//	    -password "s3cret"
//
// The command reads REDIS_URL from the environment (defaulting to
// redis://localhost:6379) and writes the following keys:
//
//	users:{uuid}            Hash   — canonical user record
//	users:{uuid}:password   String — bcrypt hash (cost 12)
//	email_index:{email}     String — uuid, for login lookups
//
// It exits non-zero if the email is already registered or if any Redis
// operation fails.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/emilhauk/msg/internal/model"
	redisclient "github.com/emilhauk/msg/internal/redis"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12

func main() {
	email := flag.String("email", "", "Email address (required)")
	name := flag.String("name", "", "Display name (required)")
	password := flag.String("password", "", "Password (required)")
	flag.Parse()

	// Validate flags.
	if *email == "" || *name == "" || *password == "" {
		fmt.Fprintln(os.Stderr, "usage: createuser -email <email> -name <name> -password <password>")
		os.Exit(1)
	}

	normalizedEmail := strings.ToLower(strings.TrimSpace(*email))

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}

	rc, err := redisclient.New(redisURL)
	if err != nil {
		log.Fatalf("connect to redis: %v", err)
	}

	ctx := context.Background()

	// Reject duplicate email addresses.
	existing, err := rc.GetUserByEmail(ctx, normalizedEmail)
	if err != nil {
		log.Fatalf("check existing user: %v", err)
	}
	if existing != nil {
		fmt.Fprintf(os.Stderr, "error: email %q is already registered (user ID: %s)\n", normalizedEmail, existing.ID)
		os.Exit(1)
	}

	// Hash the password.
	hash, err := bcrypt.GenerateFromPassword([]byte(*password), bcryptCost)
	if err != nil {
		log.Fatalf("hash password: %v", err)
	}

	// Create the canonical user record.
	userID := uuid.New().String()
	trimmedName := strings.TrimSpace(*name)

	// Claim the display name. Fail if already taken.
	claimed, err := rc.ClaimNameIndex(ctx, trimmedName, userID)
	if err != nil {
		log.Fatalf("claim name index: %v", err)
	}
	if !claimed {
		fmt.Fprintf(os.Stderr, "error: display name %q is already taken\n", trimmedName)
		os.Exit(1)
	}

	user := model.User{
		ID:        userID,
		Name:      trimmedName,
		Email:     normalizedEmail,
		CreatedAt: strconv.FormatInt(time.Now().UnixMilli(), 10),
	}
	if err := rc.CreateUser(ctx, user); err != nil {
		log.Fatalf("create user: %v", err)
	}

	// Write the bcrypt hash.
	if err := rc.SetUserPassword(ctx, userID, string(hash)); err != nil {
		log.Fatalf("set user password: %v", err)
	}

	// Write the email → uuid index so login lookups work.
	if err := rc.SetEmailIndex(ctx, normalizedEmail, userID); err != nil {
		log.Fatalf("set email index: %v", err)
	}

	fmt.Printf("created user %s (%s)\n", user.Name, userID)
}
