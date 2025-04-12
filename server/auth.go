package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type contextKey string

const (
	ctxUserIDKey  contextKey = "user_id"
	ctxIsAdminKey contextKey = "is_admin"
)

func workerAuthInterceptor(expectedToken string, db *sql.DB) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// ✅ Bypass auth for Login RPC
		if info.FullMethod == "/taskqueue.TaskQueue/Login" {
			return handler(ctx, req)
		}

		// Extract metadata
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}

		tokens := md["authorization"]
		if len(tokens) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing token")
		} else {
			if tokens[0] != "Bearer "+expectedToken {
				if strings.HasPrefix(tokens[0], "Bearer ") {
					session_id := strings.TrimPrefix(tokens[0], "Bearer ")

					// Check if the session ID is valid
					var userID int
					err := db.QueryRow("SELECT user_id FROM scitq_user_session WHERE session_id=$1 AND expires_at > now()", session_id).Scan(&userID)
					if err != nil {
						if err == sql.ErrNoRows {
							return nil, status.Error(codes.Unauthenticated, "invalid session ID")
						}
						return nil, status.Error(codes.Internal, fmt.Sprintf("failed to check session ID %s", session_id))
					}
					ctx = context.WithValue(ctx, ctxUserIDKey, userID)

					// Update the last used time for the session
					_, err = db.Exec("UPDATE scitq_user_session SET last_used=now() WHERE session_id=$1", session_id)
					if err != nil {
						log.Printf("Failed to update last used time for session ID %s: %v", session_id, err)
					}

					// Check if the user is an admin
					var isAdmin bool
					err = db.QueryRow("SELECT is_admin FROM scitq_user WHERE user_id=$1", userID).Scan(&isAdmin)
					if err != nil {
						return nil, status.Error(codes.Internal, "failed to check user role")
					}
					ctx = context.WithValue(ctx, ctxIsAdminKey, isAdmin)

				} else {
					return nil, status.Error(codes.Unauthenticated, "invalid token")
				}
			}
		}

		// ✅ Token is valid, continue
		return handler(ctx, req)
	}
}

func generateRandomPassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	//rand.Seed(time.Now().UnixNano())

	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

func checkAdminUser(db *sql.DB) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM scitq_user").Scan(&count)
	if err != nil {
		log.Fatalf("Failed to check for existing users: %v", err)
	}

	if count == 0 {
		log.Println("⚠️ No users found, creating initial admin user...")

		// Generate a random password
		rawPassword := generateRandomPassword(16)
		hashed, _ := bcrypt.GenerateFromPassword([]byte(rawPassword), bcrypt.DefaultCost)

		username := "admin"
		email := "admin@example.com"

		_, err := db.Exec(`INSERT INTO scitq_user (username, email, password, is_admin) VALUES ($1, $2, $3, TRUE)`, username, email, string(hashed))
		if err != nil {
			log.Fatalf("Failed to insert initial admin user: %v", err)
		}

		log.Printf("✅ Created initial admin user '%s' with random password: %s", username, rawPassword)
		log.Printf("⚠️ Please log in immediately and change the password.")
	}

}
