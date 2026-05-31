package handlers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"obsidian-recall/server/config"
	"obsidian-recall/server/crypto"
	"obsidian-recall/server/db"
)

type Dependencies struct {
	Config    config.Config
	Store     *db.Store
	SecretBox crypto.SecretBox
}

type API struct {
	cfg       config.Config
	store     *db.Store
	secretBox crypto.SecretBox
}

type session struct {
	TokenID string
	UserID  string
}

func newAPI(deps Dependencies) *API {
	return &API{
		cfg:       deps.Config,
		store:     deps.Store,
		secretBox: deps.SecretBox,
	}
}

func (api *API) now() time.Time {
	return time.Now().UTC()
}

func (api *API) tokenExpiry() time.Time {
	days, err := strconv.Atoi(api.cfg.JWTExpireDays)
	if err != nil || days <= 0 {
		days = 30
	}
	return api.now().Add(time.Duration(days) * 24 * time.Hour)
}

func readJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{
		"error":   code,
		"message": message,
	})
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func maskSecret(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 4 {
		return "****" + value
	}
	return "****" + value[len(value)-4:]
}

func isMaskedSecret(value string) bool {
	trimmed := strings.TrimSpace(value)
	return strings.HasPrefix(trimmed, "****")
}

func normalizeEmail(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}

func parseAuthToken(r *http.Request) (string, error) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		return "", errors.New("missing authorization header")
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", errors.New("authorization header must use bearer token")
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if token == "" {
		return "", errors.New("missing bearer token")
	}
	return token, nil
}

func (api *API) requireSession(r *http.Request) (session, error) {
	token, err := parseAuthToken(r)
	if err != nil {
		return session{}, err
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var current session
	row := api.store.SQL.QueryRowContext(ctx, `
		SELECT id, user_id
		FROM user_tokens
		WHERE token_hash = ?
		  AND revoked = 0
		  AND expires_at > ?
	`, hashToken(token), api.now().Format(time.RFC3339))
	if err := row.Scan(&current.TokenID, &current.UserID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return session{}, errors.New("invalid or expired token")
		}
		return session{}, fmt.Errorf("query session: %w", err)
	}

	_, _ = api.store.SQL.ExecContext(ctx, `
		UPDATE user_tokens
		SET last_seen_at = ?
		WHERE id = ?
	`, api.now().Format(time.RFC3339), current.TokenID)

	return current, nil
}
