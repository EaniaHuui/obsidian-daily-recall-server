package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const summaryPromptDisabledSentinel = "__SUMMARY_DISABLED__"

type userSettingsResponse struct {
	PushTime          string   `json:"push_time"`
	Timezone          string   `json:"timezone"`
	EnableRSS         bool     `json:"enable_rss"`
	EnableCubox       bool     `json:"enable_cubox"`
	CuboxAPIURL       string   `json:"cubox_api_url"`
	CuboxFolder       string   `json:"cubox_folder"`
	CuboxTags         []string `json:"cubox_tags"`
	SyncMode          string   `json:"sync_mode"`
	DailyPushCount    int      `json:"daily_push_count"`
	ExcludedFolders   []string `json:"excluded_folders"`
	MinNoteLength     int      `json:"min_note_length"`
	StorageUsedBytes  int64    `json:"storage_used_bytes"`
	StorageQuotaBytes int64    `json:"storage_quota_bytes"`
}

type userSettingsUpdateRequest struct {
	PushTime        string   `json:"push_time"`
	Timezone        string   `json:"timezone"`
	EnableRSS       bool     `json:"enable_rss"`
	EnableCubox     bool     `json:"enable_cubox"`
	CuboxAPIURL     string   `json:"cubox_api_url"`
	CuboxFolder     string   `json:"cubox_folder"`
	CuboxTags       []string `json:"cubox_tags"`
	SyncMode        string   `json:"sync_mode"`
	DailyPushCount  int      `json:"daily_push_count"`
	ExcludedFolders []string `json:"excluded_folders"`
	MinNoteLength   int      `json:"min_note_length"`
}

func (api *API) getUserSettings(w http.ResponseWriter, r *http.Request) {
	current, err := api.requireSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var cuboxURLenc, cuboxTagsJSON string
	enableRSSInt := 1
	enableCuboxInt := 0
	var response userSettingsResponse
	var excludedJSON string
	row := api.store.SQL.QueryRowContext(ctx, `
		SELECT
			COALESCE(push_time, '08:00'),
			COALESCE(timezone, 'Asia/Shanghai'),
			COALESCE(sync_mode, 'local'),
			COALESCE(excluded_folders, '[]'),
			COALESCE(min_note_length, 50),
			COALESCE(storage_quota_bytes, 104857600)
		FROM user_settings
		WHERE user_id = ?
	`, current.UserID)
	if err := row.Scan(
		&response.PushTime,
		&response.Timezone,
		&response.SyncMode,
		&excludedJSON,
		&response.MinNoteLength,
		&response.StorageQuotaBytes,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "settings_query_failed", "failed to load settings")
		return
	}

	response.ExcludedFolders, _ = decodeStringSlice(excludedJSON)
	cuboxTagsJSON = "[]"
	_ = api.store.SQL.QueryRowContext(ctx, `
		SELECT
			COALESCE(enable_rss, 1),
			COALESCE(enable_cubox, 0),
			COALESCE(cubox_api_url_enc, ''),
			COALESCE(cubox_folder, ''),
			COALESCE(cubox_tags, '[]')
		FROM user_channel_settings
		WHERE user_id = ?
	`, current.UserID).Scan(&enableRSSInt, &enableCuboxInt, &cuboxURLenc, &response.CuboxFolder, &cuboxTagsJSON)
	response.EnableRSS = enableRSSInt != 0
	response.EnableCubox = enableCuboxInt != 0
	if cuboxURLenc != "" {
		if cuboxURL, err := api.secretBox.DecryptString(cuboxURLenc); err == nil {
			response.CuboxAPIURL = maskSecret(cuboxURL)
		}
	}
	response.CuboxTags, _ = decodeStringSlice(cuboxTagsJSON)
	_ = api.store.SQL.QueryRowContext(ctx, `
		SELECT COALESCE(daily_push_count, 1)
		FROM user_prompt_settings
		WHERE user_id = ?
	`, current.UserID).Scan(&response.DailyPushCount)
	if response.DailyPushCount < 1 {
		response.DailyPushCount = 1
	}
	if response.DailyPushCount > 20 {
		response.DailyPushCount = 20
	}

	if err := api.store.SQL.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(size_bytes), 0)
		FROM notes
		WHERE user_id = ?
		  AND is_deleted = 0
	`, current.UserID).Scan(&response.StorageUsedBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "settings_usage_failed", "failed to calculate storage usage")
		return
	}

	writeJSON(w, http.StatusOK, response)
}

func (api *API) putUserSettings(w http.ResponseWriter, r *http.Request) {
	current, err := api.requireSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}

	var req userSettingsUpdateRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	if req.PushTime == "" {
		req.PushTime = "08:00"
	}
	if req.Timezone == "" {
		req.Timezone = "Asia/Shanghai"
	}
	req.SyncMode = "local"
	if req.MinNoteLength <= 0 {
		req.MinNoteLength = 50
	}
	if req.DailyPushCount < 1 {
		req.DailyPushCount = 1
	}
	if req.DailyPushCount > 20 {
		req.DailyPushCount = 20
	}

	excludedJSON, err := encodeStringSlice(req.ExcludedFolders)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_excluded_folders", "failed to encode excluded folders")
		return
	}

	cuboxTagsJSON, err := encodeStringSlice(req.CuboxTags)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_cubox_tags", "failed to encode cubox tags")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var cuboxURLenc string
	if isMaskedSecret(req.CuboxAPIURL) {
		_ = api.store.SQL.QueryRowContext(ctx, `
			SELECT COALESCE(cubox_api_url_enc, '')
			FROM user_channel_settings
			WHERE user_id = ?
		`, current.UserID).Scan(&cuboxURLenc)
	}
	if cuboxURLenc == "" {
		cuboxURLenc, err = api.secretBox.EncryptString(strings.TrimSpace(req.CuboxAPIURL))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "cubox_encrypt_failed", "failed to encrypt cubox api url")
			return
		}
	}

	_, err = api.store.SQL.ExecContext(ctx, `
		INSERT INTO user_settings (
			user_id, push_time, timezone, sync_mode,
			excluded_folders, min_note_length, max_note_age_days,
			storage_quota_bytes, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, 0, 104857600, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			push_time = excluded.push_time,
			timezone = excluded.timezone,
			sync_mode = excluded.sync_mode,
			excluded_folders = excluded.excluded_folders,
			min_note_length = excluded.min_note_length,
			updated_at = excluded.updated_at
	`, current.UserID, req.PushTime, req.Timezone, req.SyncMode, excludedJSON, req.MinNoteLength, api.now().Format(time.RFC3339))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "settings_save_failed", "failed to save settings")
		return
	}
	_, _ = api.store.SQL.ExecContext(ctx, `
		INSERT INTO user_prompt_settings (user_id, daily_push_count, summary_prompt, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			daily_push_count = excluded.daily_push_count,
			summary_prompt = excluded.summary_prompt,
			updated_at = excluded.updated_at
	`, current.UserID, req.DailyPushCount, summaryPromptDisabledSentinel, api.now().Format(time.RFC3339))
	enableRSS := 0
	if req.EnableRSS {
		enableRSS = 1
	}
	enableCubox := 0
	if req.EnableCubox {
		enableCubox = 1
	}
	_, _ = api.store.SQL.ExecContext(ctx, `
		INSERT INTO user_channel_settings (
			user_id, enable_rss, enable_cubox, cubox_api_url_enc, cubox_folder, cubox_tags, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			enable_rss = excluded.enable_rss,
			enable_cubox = excluded.enable_cubox,
			cubox_api_url_enc = excluded.cubox_api_url_enc,
			cubox_folder = excluded.cubox_folder,
			cubox_tags = excluded.cubox_tags,
			updated_at = excluded.updated_at
	`, current.UserID, enableRSS, enableCubox, cuboxURLenc, strings.TrimSpace(req.CuboxFolder), cuboxTagsJSON, api.now().Format(time.RFC3339))

	api.getUserSettings(w, r)
}

func encodeStringSlice(values []string) (string, error) {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		normalized = append(normalized, trimmed)
	}
	bytes, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func decodeStringSlice(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return []string{}, nil
	}
	var result []string
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		return []string{}, err
	}
	return result, nil
}
