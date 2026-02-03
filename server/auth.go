package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"strings"

	"github.com/scitq/scitq/server/config"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type contextKey string

const userContextKey contextKey = "user"

type AuthenticatedUser struct {
	UserID   int
	Username string
	IsAdmin  bool
}

func GetUserFromContext(ctx context.Context) *AuthenticatedUser {
	if u, ok := ctx.Value(userContextKey).(*AuthenticatedUser); ok {
		return u
	}
	return nil
}

func IsAdmin(ctx context.Context) bool {
	user := GetUserFromContext(ctx)
	if user == nil {
		return false
	}
	return user.IsAdmin
}

func workerAuthInterceptor(expectedToken string, db *sql.DB) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// ✅ Bypass auth for Login RPC
		if info.FullMethod == "/taskqueue.TaskQueue/Login" ||
			info.FullMethod == "/taskqueue.TaskQueue/ChangePassword" ||
			info.FullMethod == "/taskqueue.TaskQueue/GetCertificate" {
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
					var (
						userID   int
						username string
						isAdmin  bool
					)
					err := db.QueryRow(`SELECT u.user_id, u.username, u.is_admin FROM scitq_user_session s JOIN
						 scitq_user u ON s.user_id=u.user_id WHERE s.session_id=$1 AND s.expires_at > now()`,
						session_id).Scan(&userID, &username, &isAdmin)
					if err != nil {
						if err == sql.ErrNoRows {
							return nil, status.Error(codes.Unauthenticated, "invalid session ID")
						}
						return nil, status.Error(codes.Internal, fmt.Sprintf("failed to check session ID %s", session_id))
					}
					ctx = context.WithValue(ctx, userContextKey, &AuthenticatedUser{
						UserID:   userID,
						Username: username,
						IsAdmin:  isAdmin,
					})

					// Update the last used time for the session
					_, err = db.Exec("UPDATE scitq_user_session SET last_used=now() WHERE session_id=$1", session_id)
					if err != nil {
						log.Printf("Failed to update last used time for session ID %s: %v", session_id, err)
					}

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

func checkAdminUser(db *sql.DB, cfg *config.Config) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM scitq_user").Scan(&count)
	if err != nil {
		log.Fatalf("Failed to check for existing users: %v", err)
	}

	if count == 0 {
		log.Println("⚠️ No users found, creating initial admin user...")

		username := cfg.Scitq.AdminUser
		email := cfg.Scitq.AdminEmail
		rawPassword := ""
		hashed := []byte(cfg.Scitq.AdminHashedPassword)
		if cfg.Scitq.AdminHashedPassword == "" {
			// Generate a random password
			rawPassword = generateRandomPassword(16)
			hashed, _ = bcrypt.GenerateFromPassword([]byte(rawPassword), bcrypt.DefaultCost)
		}

		_, err := db.Exec(`INSERT INTO scitq_user (username, email, password, is_admin) VALUES ($1, $2, $3, TRUE)`, username, email, string(hashed))
		if err != nil {
			log.Fatalf("Failed to insert initial admin user: %v", err)
		}

		if rawPassword == "" {
			log.Printf("✅ Created initial admin user '%s' with random password: %s", username, rawPassword)
			log.Printf("⚠️ Please log in immediately and change the password.")
		} else {
			log.Printf("✅ Created initial admin user '%s' with provided hashed password.", username)
		}
	}

}

func grpcMetadataFromContext(ctx context.Context) (map[string][]string, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	return md, ok
}

func extractTokenFromContext(ctx context.Context) string {
	md, ok := grpcMetadataFromContext(ctx)
	if !ok {
		return ""
	}

	// Prefer "authorization: Bearer TOKEN"
	authHeaders := md["authorization"]
	for _, h := range authHeaders {
		if strings.HasPrefix(h, "Bearer ") {
			return strings.TrimSpace(h[len("Bearer "):])
		}
	}

	// Fallback: raw token in header (if stored differently)
	if tokenList, ok := md["scitq-token"]; ok && len(tokenList) > 0 {
		return tokenList[0]
	}

	return ""
}
