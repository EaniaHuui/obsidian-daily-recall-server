package handlers

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	DeviceName string `json:"device_name"`
}

type anonymousAuthRequest struct {
	ClientID   string `json:"client_id"`
	DeviceName string `json:"device_name"`
}

func (api *API) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	email := normalizeEmail(req.Email)
	if email == "" || len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "invalid_input", "email and password length must be valid")
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "password_hash_failed", "failed to hash password")
		return
	}

	userID, err := randomHex(16)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "id_generation_failed", "failed to generate user id")
		return
	}

	now := api.now().Format(time.RFC3339)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	tx, err := api.store.SQL.BeginTx(ctx, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_begin_failed", "failed to open database transaction")
		return
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (id, email, password, created_at, is_active)
		VALUES (?, ?, ?, ?, 1)
	`, userID, email, string(passwordHash), now); err != nil {
		writeError(w, http.StatusConflict, "email_exists", "email already registered")
		return
	}

	if err := initializeUserDefaults(ctx, tx, userID, now); err != nil {
		writeError(w, http.StatusInternalServerError, "settings_init_failed", "failed to initialize channel settings")
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "db_commit_failed", "failed to save user")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"user_id": userID,
		"message": "registered",
	})
}

func (api *API) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	email := normalizeEmail(req.Email)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var userID string
	var passwordHash string
	row := api.store.SQL.QueryRowContext(ctx, `
		SELECT id, password
		FROM users
		WHERE email = ?
		  AND is_active = 1
	`, email)
	if err := row.Scan(&userID, &passwordHash); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}

	token, err := randomHex(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token_generation_failed", "failed to generate token")
		return
	}
	tokenID, err := randomHex(16)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "id_generation_failed", "failed to generate token id")
		return
	}

	expiresAt := api.tokenExpiry().Format(time.RFC3339)
	now := api.now().Format(time.RFC3339)
	if _, err := api.store.SQL.ExecContext(ctx, `
		INSERT INTO user_tokens (
			id, user_id, token_hash, device_name, expires_at,
			revoked, created_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, 0, ?, ?)
	`, tokenID, userID, hashToken(token), req.DeviceName, expiresAt, now, now); err != nil {
		writeError(w, http.StatusInternalServerError, "token_save_failed", "failed to create session")
		return
	}

	_, _ = api.store.SQL.ExecContext(ctx, `
		UPDATE users SET last_login = ? WHERE id = ?
	`, now, userID)

	writeJSON(w, http.StatusOK, map[string]string{
		"token":      token,
		"expires_at": expiresAt,
	})
}

func (api *API) anonymousLogin(w http.ResponseWriter, r *http.Request) {
	var req anonymousAuthRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	clientID := strings.TrimSpace(req.ClientID)
	if clientID == "" || len(clientID) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_client_id", "client_id is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	now := api.now().Format(time.RFC3339)
	deviceName := strings.TrimSpace(req.DeviceName)

	tx, err := api.store.SQL.BeginTx(ctx, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_begin_failed", "failed to open database transaction")
		return
	}
	defer func() { _ = tx.Rollback() }()

	var userID string
	err = tx.QueryRowContext(ctx, `
		SELECT user_id FROM device_identities WHERE client_id = ?
	`, clientID).Scan(&userID)
	switch {
	case err == nil:
		_, _ = tx.ExecContext(ctx, `
			UPDATE device_identities
			SET device_name = ?, last_seen_at = ?
			WHERE client_id = ?
		`, deviceName, now, clientID)
	case err == sql.ErrNoRows:
		userID, err = randomHex(16)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "id_generation_failed", "failed to generate user id")
			return
		}
		pwdRaw, err := randomHex(16)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "id_generation_failed", "failed to generate password")
			return
		}
		passwordHash, err := bcrypt.GenerateFromPassword([]byte(pwdRaw), 12)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "password_hash_failed", "failed to hash password")
			return
		}
		email := "anon-" + userID + "@local"
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO users (id, email, password, created_at, is_active)
			VALUES (?, ?, ?, ?, 1)
		`, userID, email, string(passwordHash), now); err != nil {
			writeError(w, http.StatusInternalServerError, "user_create_failed", "failed to create anonymous user")
			return
		}
		if err := initializeUserDefaults(ctx, tx, userID, now); err != nil {
			writeError(w, http.StatusInternalServerError, "settings_init_failed", "failed to initialize user defaults")
			return
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO device_identities (client_id, user_id, device_name, created_at, last_seen_at)
			VALUES (?, ?, ?, ?, ?)
		`, clientID, userID, deviceName, now, now); err != nil {
			writeError(w, http.StatusInternalServerError, "device_identity_failed", "failed to save device identity")
			return
		}
	default:
		writeError(w, http.StatusInternalServerError, "device_lookup_failed", "failed to query device identity")
		return
	}

	token, err := randomHex(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token_generation_failed", "failed to generate token")
		return
	}
	tokenID, err := randomHex(16)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "id_generation_failed", "failed to generate token id")
		return
	}
	expiresAt := api.tokenExpiry().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_tokens (
			id, user_id, token_hash, device_name, expires_at,
			revoked, created_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, 0, ?, ?)
	`, tokenID, userID, hashToken(token), deviceName, expiresAt, now, now); err != nil {
		writeError(w, http.StatusInternalServerError, "token_save_failed", "failed to create session")
		return
	}

	_, _ = tx.ExecContext(ctx, `UPDATE users SET last_login = ? WHERE id = ?`, now, userID)
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "db_commit_failed", "failed to save session")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"token":      token,
		"expires_at": expiresAt,
		"user_id":    userID,
	})
}

func (api *API) logout(w http.ResponseWriter, r *http.Request) {
	current, err := api.requireSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if _, err := api.store.SQL.ExecContext(ctx, `
		UPDATE user_tokens
		SET revoked = 1
		WHERE id = ?
	`, current.TokenID); err != nil {
		writeError(w, http.StatusInternalServerError, "logout_failed", "failed to revoke session")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "logged_out"})
}

func initializeUserDefaults(ctx context.Context, tx *sql.Tx, userID, now string) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_settings (
			user_id, push_time, timezone, ai_model, sync_mode,
			excluded_folders, min_note_length, max_note_age_days,
			storage_quota_bytes, updated_at
		) VALUES (?, '08:00', 'Asia/Shanghai', 'deepseek-chat', 'local', '[]', 50, 0, 104857600, ?)
	`, userID, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_prompt_settings (user_id, daily_push_count, summary_prompt, updated_at)
		VALUES (?, 1, '', ?)
	`, userID, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_channel_settings (
			user_id, enable_rss, enable_cubox, cubox_api_url_enc, cubox_folder, cubox_tags, updated_at
		) VALUES (?, 1, 0, '', '', '[]', ?)
	`, userID, now); err != nil {
		return err
	}
	return nil
}

func (api *API) sessions(w http.ResponseWriter, r *http.Request) {
	current, err := api.requireSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	rows, err := api.store.SQL.QueryContext(ctx, `
		SELECT id, COALESCE(device_name, ''), created_at, COALESCE(last_seen_at, '')
		FROM user_tokens
		WHERE user_id = ?
		  AND revoked = 0
		ORDER BY created_at DESC
	`, current.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sessions_query_failed", "failed to list sessions")
		return
	}
	defer rows.Close()

	type responseItem struct {
		ID         string `json:"id"`
		DeviceName string `json:"device_name"`
		CreatedAt  string `json:"created_at"`
		LastSeenAt string `json:"last_seen_at"`
	}

	items := make([]responseItem, 0)
	for rows.Next() {
		var item responseItem
		if err := rows.Scan(&item.ID, &item.DeviceName, &item.CreatedAt, &item.LastSeenAt); err != nil {
			writeError(w, http.StatusInternalServerError, "sessions_scan_failed", "failed to read sessions")
			return
		}
		items = append(items, item)
	}

	writeJSON(w, http.StatusOK, items)
}

func (api *API) revokeSession(w http.ResponseWriter, r *http.Request) {
	current, err := api.requireSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}

	sessionID := r.PathValue("session_id")
	if strings.TrimSpace(sessionID) == "" {
		writeError(w, http.StatusBadRequest, "invalid_session_id", "missing session id")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	result, err := api.store.SQL.ExecContext(ctx, `
		UPDATE user_tokens
		SET revoked = 1
		WHERE id = ?
		  AND user_id = ?
	`, sessionID, current.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "revoke_failed", "failed to revoke session")
		return
	}

	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		writeError(w, http.StatusNotFound, "session_not_found", "session not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "session_revoked"})
}

func randomHex(byteCount int) (string, error) {
	buf := make([]byte, byteCount)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
