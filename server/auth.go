package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strconv"
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
const legacyLogWriterKey contextKey = "legacy-log-writer"
const tokenContextKey contextKey = "token"

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

// sessionIdle is how long a session lives without being used. Its
// expires_at stays an absolute cap (a year for Login, minutes for the
// internal sessions of chain firing).
const sessionIdle = "30 days"

// lookupSession resolves a session token to its user. The session table is
// the only proof of login: a token's JWT signature alone authenticates
// nothing, so Logout, a password change and DeleteUser revoke it everywhere.
func lookupSession(db *sql.DB, token string) (*AuthenticatedUser, error) {
	var u AuthenticatedUser
	err := db.QueryRow(`UPDATE scitq_user_session s SET last_used=now()
		FROM scitq_user u
		WHERE s.user_id=u.user_id AND s.session_id=$1 AND s.expires_at > now()
		  AND COALESCE(s.last_used, s.created_at) > now() - interval '`+sessionIdle+`'
		RETURNING u.user_id, u.username, u.is_admin`, token).Scan(&u.UserID, &u.Username, &u.IsAdmin)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// unauthenticatedMethods carry their own credentials (a password) or none.
var unauthenticatedMethods = map[string]bool{
	"Login":          true,
	"ChangePassword": true,
	"GetCertificate": true,
}

// workerMethods are the calls a worker makes. The shared worker token
// allows these and nothing else; every other call needs a user. FetchList
// stays for scitq_auth tasks started by workers older than task tokens,
// which hand those tasks the worker token.
var workerMethods = map[string]bool{
	"RegisterWorker":         true,
	"RegisterSpecifications": true,
	"PingAndTakeNewTasks":    true,
	"GetTask":                true,
	"UpdateTaskStatus":       true,
	"SendTaskLogs":           true,
	"UpdateWorker":           true,
	"UpdateWorkerStatus":     true,
	"ReportWorkerEvent":      true,
	"GetClientUpgradeInfo":   true,
	"GetRcloneConfig":        true,
	"GetDockerCredentials":   true,
	"ServerVersion":          true,
	"FetchList":              true,
}

// taskMethods are the calls `scitq file copy|list` makes from inside a
// scitq_auth task.
var taskMethods = map[string]bool{
	"GetRcloneConfig": true,
	"FetchList":       true,
	"ServerVersion":   true,
}

const taskTokenPrefix = "task-"

// taskToken derives the credential of a scitq_auth task from the server
// secret. It needs no storage: it is checked by recomputing it, and it
// stops working when the task leaves the active states.
func taskToken(secret string, taskID int32) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "scitq-task-token:%d", taskID)
	return fmt.Sprintf("%s%d-%s", taskTokenPrefix, taskID, hex.EncodeToString(mac.Sum(nil)))
}

// checkTaskToken returns whether token is the credential of a scitq_auth
// task that is still active.
func checkTaskToken(db *sql.DB, secret, token string) bool {
	idStr, _, ok := strings.Cut(strings.TrimPrefix(token, taskTokenPrefix), "-")
	if !ok {
		return false
	}
	id, err := strconv.ParseInt(idStr, 10, 32)
	if err != nil || !hmac.Equal([]byte(token), []byte(taskToken(secret, int32(id)))) {
		return false
	}
	var active bool
	err = db.QueryRow(`SELECT EXISTS (SELECT 1 FROM task WHERE task_id=$1 AND scitq_auth AND NOT hidden
		AND status IN ('A','C','D','O','R','U','V'))`, id).Scan(&active)
	return err == nil && active
}

// authenticate checks the caller of fullMethod and returns ctx with the
// user attached when the caller is a user. A worker or task token
// authenticates without a user, for its own list of methods only.
func authenticate(ctx context.Context, fullMethod, workerToken, secret string, db *sql.DB) (context.Context, error) {
	method := fullMethod[strings.LastIndex(fullMethod, "/")+1:]
	if unauthenticatedMethods[method] {
		return ctx, nil
	}
	md, _ := metadata.FromIncomingContext(ctx)
	auth := md["authorization"]
	if len(auth) == 0 && method == "SendTaskLogs" {
		// Workers before v1.0 send their log streams without a token.
		// SendTaskLogs accepts them for tasks running on such workers only.
		return context.WithValue(ctx, legacyLogWriterKey, true), nil
	}
	if len(auth) == 0 || !strings.HasPrefix(auth[0], "Bearer ") {
		return nil, status.Error(codes.Unauthenticated, "missing token")
	}
	token := strings.TrimSpace(strings.TrimPrefix(auth[0], "Bearer "))

	switch {
	case subtle.ConstantTimeCompare([]byte(token), []byte(workerToken)) == 1:
		if !workerMethods[method] {
			return nil, status.Errorf(codes.PermissionDenied, "%s needs a user login: the worker token allows worker calls only", method)
		}
		return ctx, nil
	case strings.HasPrefix(token, taskTokenPrefix):
		if !checkTaskToken(db, secret, token) {
			return nil, status.Error(codes.Unauthenticated, "invalid or expired task token")
		}
		if !taskMethods[method] {
			return nil, status.Errorf(codes.PermissionDenied, "%s is not allowed to a task token", method)
		}
		return ctx, nil
	}

	user, err := lookupSession(db, token)
	if err == sql.ErrNoRows {
		return nil, status.Error(codes.Unauthenticated, "invalid or expired session")
	} else if err != nil {
		return nil, status.Error(codes.Internal, "failed to check session")
	}
	return context.WithValue(ctx, userContextKey, user), nil
}

// legacyLogWriterAllowed returns whether a token-less log stream may write
// to taskID: the task is active on a worker that reported a version before
// v1.0, or no version at all.
func legacyLogWriterAllowed(db *sql.DB, taskID int32) bool {
	var ok bool
	err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM task t JOIN worker w ON w.worker_id=t.worker_id
		WHERE t.task_id=$1 AND t.status IN ('A','C','D','O','R','U','V')
		  AND (w.version IS NULL OR w.version LIKE 'v0.%'))`, taskID).Scan(&ok)
	return err == nil && ok
}

func authUnaryInterceptor(workerToken, secret string, db *sql.DB) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		ctx, err := authenticate(ctx, info.FullMethod, workerToken, secret, db)
		if err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

type authedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *authedStream) Context() context.Context { return s.ctx }

func authStreamInterceptor(workerToken, secret string, db *sql.DB) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, err := authenticate(ss.Context(), info.FullMethod, workerToken, secret, db)
		if err != nil {
			return err
		}
		return handler(srv, &authedStream{ss, ctx})
	}
}

func generateRandomPassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			log.Fatalf("Failed to generate a random password: %v", err)
		}
		b[i] = charset[n.Int64()]
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

		if rawPassword != "" {
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
	if ok {
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
	}

	// Fallback: token stored in context (MCP/HTTP path)
	if t, ok := ctx.Value(tokenContextKey).(string); ok && t != "" {
		log.Printf("🔑 extractTokenFromContext: found token via context key (%d chars)", len(t))
		return t
	}

	return ""
}

// requireSession guards an HTTP handler with a user session, read from the
// session_token cookie (the UI) or an Authorization bearer header.
func (s *taskQueueServer) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if c, err := r.Cookie("session_token"); err == nil && c.Value != "" {
			token = c.Value
		}
		if token == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if _, err := lookupSession(s.db, token); err != nil {
			http.Error(w, "Invalid or expired session", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
