package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type pushHistoryItem struct {
	ID        string `json:"id"`
	NoteTitle string `json:"note_title"`
	NotePath  string `json:"note_path"`
	Summary   string `json:"summary"`
	PushedAt  string `json:"pushed_at"`
}

type pushHistoryResponse struct {
	Total int64             `json:"total"`
	Items []pushHistoryItem `json:"items"`
}

type pushHistoryDetailResponse struct {
	ID        string `json:"id"`
	NoteTitle string `json:"note_title"`
	NotePath  string `json:"note_path"`
	Summary   string `json:"summary"`
	Content   string `json:"content"`
	PushedAt  string `json:"pushed_at"`
}

type queueLocalRecallRequest struct {
	Items []queueLocalRecallItem `json:"items"`
}

type queueLocalRecallItem struct {
	Path          string `json:"path"`
	Title         string `json:"title"`
	Content       string `json:"content"`
	ContentHash   string `json:"content_hash"`
	NoteUpdatedAt string `json:"note_updated_at"`
	ScheduledDate string `json:"scheduled_date"`
	SlotIndex     int    `json:"slot_index"`
}

type queueLocalRecallResponse struct {
	Queued  int `json:"queued"`
	Skipped int `json:"skipped"`
}

type upcomingRecallItem struct {
	ScheduledDate string `json:"scheduled_date"`
	SlotIndex     int    `json:"slot_index"`
	Path          string `json:"path"`
}

type queueStatusResponse struct {
	DailyPushCount int                  `json:"daily_push_count"`
	Days           int                  `json:"days"`
	Items          []upcomingRecallItem `json:"items"`
}

type pushSettings struct {
	DailyCount  int
	EnableRSS   bool
	EnableCubox bool
	CuboxAPIURL string
	CuboxFolder string
	CuboxTags   []string
}

func (api *API) queueLocalRecalls(w http.ResponseWriter, r *http.Request) {
	current, err := api.requireSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}

	var req queueLocalRecallRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if len(req.Items) == 0 {
		writeJSON(w, http.StatusOK, queueLocalRecallResponse{})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	queued := 0
	skipped := 0
	now := api.now().Format(time.RFC3339)
	for _, item := range req.Items {
		item.Path = strings.TrimSpace(item.Path)
		item.Title = strings.TrimSpace(item.Title)
		item.Content = strings.TrimSpace(item.Content)
		item.ContentHash = strings.TrimSpace(item.ContentHash)
		item.ScheduledDate = strings.TrimSpace(item.ScheduledDate)
		if item.Path == "" || item.Title == "" || item.Content == "" || item.ScheduledDate == "" || item.SlotIndex <= 0 {
			skipped++
			continue
		}
		if item.ContentHash == "" {
			item.ContentHash = hashToken(item.Content)
		}
		if item.NoteUpdatedAt == "" {
			item.NoteUpdatedAt = now
		}

		var existingID string
		err := api.store.SQL.QueryRowContext(ctx, `
			SELECT id FROM scheduled_recalls
			WHERE user_id = ? AND scheduled_date = ? AND slot_index = ?
		`, current.UserID, item.ScheduledDate, item.SlotIndex).Scan(&existingID)
		if err == nil {
			skipped++
			continue
		}
		if err != nil && err != sql.ErrNoRows {
			writeError(w, http.StatusInternalServerError, "queue_lookup_failed", "failed to query queue")
			return
		}

		// avoid same note hash repeated within upcoming 30 days
		var hashCount int
		if err := api.store.SQL.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM scheduled_recalls
			WHERE user_id = ?
			  AND content_hash = ?
			  AND scheduled_date >= date('now', '-30 day')
		`, current.UserID, item.ContentHash).Scan(&hashCount); err != nil {
			writeError(w, http.StatusInternalServerError, "queue_hash_check_failed", "failed to check queue duplicates")
			return
		}
		if hashCount > 0 {
			skipped++
			continue
		}

		recallID, idErr := randomHex(16)
		if idErr != nil {
			writeError(w, http.StatusInternalServerError, "id_generation_failed", "failed to generate queue id")
			return
		}

		_, execErr := api.store.SQL.ExecContext(ctx, `
			INSERT INTO scheduled_recalls (
				id, user_id, path, title, content, content_hash,
				note_updated_at, scheduled_date, slot_index, status,
				summary, error_msg, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'queued', '', '', ?, ?)
		`, recallID, current.UserID, item.Path, item.Title, item.Content, item.ContentHash, item.NoteUpdatedAt, item.ScheduledDate, item.SlotIndex, now, now)
		if execErr != nil {
			if strings.Contains(execErr.Error(), "UNIQUE") {
				skipped++
				continue
			}
			writeError(w, http.StatusInternalServerError, "queue_save_failed", "failed to save queue item")
			return
		}
		queued++
	}

	writeJSON(w, http.StatusOK, queueLocalRecallResponse{Queued: queued, Skipped: skipped})
}

func (api *API) queueStatus(w http.ResponseWriter, r *http.Request) {
	current, err := api.requireSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}

	days := parseQueryInt(r, "days", 7)
	if days < 1 {
		days = 1
	}
	if days > 30 {
		days = 30
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	settings, err := api.loadPushSettings(ctx, current.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "settings_query_failed", "failed to read settings")
		return
	}

	rows, err := api.store.SQL.QueryContext(ctx, `
		SELECT scheduled_date, slot_index, path
		FROM scheduled_recalls
		WHERE user_id = ?
		  AND status = 'queued'
		  AND scheduled_date >= date('now')
		  AND scheduled_date < date('now', ?)
		ORDER BY scheduled_date ASC, slot_index ASC
	`, current.UserID, fmt.Sprintf("+%d day", days))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "queue_status_failed", "failed to load queue status")
		return
	}
	defer rows.Close()

	items := make([]upcomingRecallItem, 0)
	for rows.Next() {
		var item upcomingRecallItem
		if err := rows.Scan(&item.ScheduledDate, &item.SlotIndex, &item.Path); err != nil {
			writeError(w, http.StatusInternalServerError, "queue_status_scan_failed", "failed to scan queue status")
			return
		}
		items = append(items, item)
	}

	writeJSON(w, http.StatusOK, queueStatusResponse{DailyPushCount: settings.DailyCount, Days: days, Items: items})
}

func (api *API) pushHistory(w http.ResponseWriter, r *http.Request) { /* unchanged */
	current, err := api.requireSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}
	page := maxInt(parseQueryInt(r, "page", 1), 1)
	limit := parseQueryInt(r, "limit", 20)
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var total int64
	if err := api.store.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM push_history WHERE user_id = ?`, current.UserID).Scan(&total); err != nil {
		writeError(w, http.StatusInternalServerError, "push_history_count_failed", "failed to count push history")
		return
	}
	rows, err := api.store.SQL.QueryContext(ctx, `SELECT id, note_title, note_path, COALESCE(summary, ''), pushed_at FROM push_history WHERE user_id = ? ORDER BY pushed_at DESC LIMIT ? OFFSET ?`, current.UserID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "push_history_query_failed", "failed to load push history")
		return
	}
	defer rows.Close()
	items := make([]pushHistoryItem, 0, limit)
	for rows.Next() {
		var item pushHistoryItem
		if err := rows.Scan(&item.ID, &item.NoteTitle, &item.NotePath, &item.Summary, &item.PushedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "push_history_scan_failed", "failed to read push history")
			return
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, pushHistoryResponse{Total: total, Items: items})
}

func (api *API) pushHistoryDetail(w http.ResponseWriter, r *http.Request) {
	current, err := api.requireSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_push_history_id", "missing push history id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var response pushHistoryDetailResponse
	var noteID string
	row := api.store.SQL.QueryRowContext(ctx, `SELECT id, note_id, note_title, note_path, COALESCE(summary,''), pushed_at FROM push_history WHERE id = ? AND user_id = ?`, id, current.UserID)
	if err := row.Scan(&response.ID, &noteID, &response.NoteTitle, &response.NotePath, &response.Summary, &response.PushedAt); err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "push_history_not_found", "push history not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "push_history_query_failed", "failed to load push history detail")
		return
	}
	_ = api.store.SQL.QueryRowContext(ctx, `
		SELECT COALESCE(content,'')
		FROM notes
		WHERE user_id = ?
		  AND (id = ? OR path = ?)
		ORDER BY CASE WHEN id = ? THEN 0 ELSE 1 END
		LIMIT 1
	`, current.UserID, noteID, response.NotePath, noteID).Scan(&response.Content)
	writeJSON(w, http.StatusOK, response)
}

func (api *API) processDueRecalls(ctx context.Context) {
	rows, err := api.store.SQL.QueryContext(ctx, `
		SELECT u.id, COALESCE(us.timezone,'Asia/Shanghai'), COALESCE(us.push_time,'08:00')
		FROM users u
		LEFT JOIN user_settings us ON us.user_id = u.id
		WHERE u.is_active = 1
	`)
	if err != nil {
		log.Printf("worker: query users failed: %v", err)
		return
	}
	type dueUser struct {
		userID   string
		timezone string
		pushTime string
	}
	users := make([]dueUser, 0, 128)
	for rows.Next() {
		var u dueUser
		if err := rows.Scan(&u.userID, &u.timezone, &u.pushTime); err != nil {
			log.Printf("worker: scan user row failed: %v", err)
			continue
		}
		users = append(users, u)
	}
	_ = rows.Close()

	now := time.Now().UTC()
	for _, u := range users {
		userID, timezone, pushTime := u.userID, u.timezone, u.pushTime
		loc, err := time.LoadLocation(timezone)
		if err != nil {
			loc = time.FixedZone("CST", 8*3600)
		}
		localNow := now.In(loc)
		hm := strings.Split(pushTime, ":")
		if len(hm) != 2 {
			continue
		}
		h, _ := strconv.Atoi(hm[0])
		m, _ := strconv.Atoi(hm[1])
		if h < 0 || h > 23 || m < 0 || m > 59 {
			continue
		}
		currentMinutes := localNow.Hour()*60 + localNow.Minute()
		targetMinutes := h*60 + m
		// robust scheduler: once current time passes target, process all queued dates <= today
		if currentMinutes < targetMinutes {
			continue
		}
		today := localNow.Format("2006-01-02")
		log.Printf("worker: user=%s timezone=%s push_time=%s today=%s eligible", userID, timezone, pushTime, today)
		api.processUserDueDates(ctx, userID, today)
	}
}

func (api *API) processUserDueDates(ctx context.Context, userID, today string) {
	rows, err := api.store.SQL.QueryContext(ctx, `
		SELECT DISTINCT scheduled_date
		FROM scheduled_recalls
		WHERE user_id = ?
		  AND status = 'queued'
		  AND scheduled_date <= ?
		ORDER BY scheduled_date ASC
	`, userID, today)
	if err != nil {
		log.Printf("worker: query due dates failed user=%s today=%s: %v", userID, today, err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var date string
		if err := rows.Scan(&date); err != nil {
			log.Printf("worker: scan due date failed user=%s: %v", userID, err)
			continue
		}
		log.Printf("worker: process queued recalls user=%s date=%s", userID, date)
		api.processUserScheduledDate(ctx, userID, date)
	}
}

func (api *API) processUserScheduledDate(ctx context.Context, userID, date string) {
	rows, err := api.store.SQL.QueryContext(ctx, `
		SELECT id, path, title, content, content_hash
		FROM scheduled_recalls
		WHERE user_id = ? AND scheduled_date = ? AND status = 'queued'
		ORDER BY slot_index ASC
	`, userID, date)
	if err != nil {
		return
	}
	type recallItem struct {
		id          string
		path        string
		title       string
		content     string
		contentHash string
	}
	items := make([]recallItem, 0, 32)
	for rows.Next() {
		var item recallItem
		if err := rows.Scan(&item.id, &item.path, &item.title, &item.content, &item.contentHash); err != nil {
			continue
		}
		items = append(items, item)
	}
	_ = rows.Close()

	for _, item := range items {
		_ = api.processSingleRecall(ctx, userID, item.id, item.path, item.title, item.content, item.contentHash)
	}
}

func (api *API) processSingleRecall(ctx context.Context, userID, scheduledID, path, title, content, contentHash string) error {
	now := api.now().Format(time.RFC3339)
	_, _ = api.store.SQL.ExecContext(ctx, `UPDATE scheduled_recalls SET status='summarizing', updated_at=? WHERE id=?`, now, scheduledID)
	settings, err := api.loadPushSettings(ctx, userID)
	if err != nil {
		_, _ = api.store.SQL.ExecContext(ctx, `UPDATE scheduled_recalls SET status='failed', error_msg=?, updated_at=? WHERE id=?`, "settings load failed", now, scheduledID)
		return err
	}

	preview := truncateRunes(content, 500)
	_, _ = api.store.SQL.ExecContext(ctx, `UPDATE scheduled_recalls SET status='pushing', summary=?, updated_at=? WHERE id=?`, preview, now, scheduledID)

	noteID, _ := randomHex(16)
	_, _ = api.store.SQL.ExecContext(ctx, `
		INSERT INTO notes (
			id, user_id, path, title, content, size_bytes, content_hash, note_updated_at, synced_at, is_deleted
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
		ON CONFLICT(user_id, path) DO UPDATE SET
			title = excluded.title,
			content = excluded.content,
			size_bytes = excluded.size_bytes,
			content_hash = excluded.content_hash,
			note_updated_at = excluded.note_updated_at,
			synced_at = excluded.synced_at,
			is_deleted = 0
	`, noteID, userID, path, title, content, len([]byte(content)), contentHash, now, now)

	successCount := 0
	failedReasons := make([]string, 0, 2)
	if settings.EnableRSS {
		historyID, _ := randomHex(16)
		if _, err = api.store.SQL.ExecContext(ctx, `
			INSERT INTO push_history (id, user_id, note_id, note_path, note_title, summary, pushed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, historyID, userID, noteID, path, title, preview, now); err != nil {
			failedReasons = append(failedReasons, "rss_history_save_failed")
		} else {
			successCount++
		}
	}
	if settings.EnableCubox {
		if err := api.pushToCubox(ctx, settings, title, path, content); err != nil {
			failedReasons = append(failedReasons, "cubox_push_failed:"+err.Error())
		} else {
			successCount++
		}
	}
	if !settings.EnableRSS && !settings.EnableCubox {
		failedReasons = append(failedReasons, "no_channel_enabled")
	}
	if successCount == 0 {
		errMsg := strings.Join(failedReasons, "; ")
		if errMsg == "" {
			errMsg = "all channels failed"
		}
		_, _ = api.store.SQL.ExecContext(ctx, `UPDATE scheduled_recalls SET status='failed', error_msg=?, updated_at=? WHERE id=?`, truncateRunes(errMsg, 1000), now, scheduledID)
		return fmt.Errorf("%s", errMsg)
	}
	_, _ = api.store.SQL.ExecContext(ctx, `UPDATE scheduled_recalls SET status='done', updated_at=? WHERE id=?`, now, scheduledID)
	return nil
}

func parseQueryInt(r *http.Request, key string, fallback int) int {
	value := strings.TrimSpace(r.URL.Query().Get(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (api *API) loadPushSettings(ctx context.Context, userID string) (pushSettings, error) {
	settings := pushSettings{}
	var daily int
	if err := api.store.SQL.QueryRowContext(ctx, `SELECT COALESCE(daily_push_count,1) FROM user_prompt_settings WHERE user_id = ?`, userID).Scan(&daily); err != nil {
		if err != sql.ErrNoRows {
			return settings, err
		}
		daily = 1
	}
	if daily < 1 {
		daily = 1
	}
	if daily > 20 {
		daily = 20
	}
	settings.DailyCount = daily
	settings.EnableRSS = true
	var enableRSSInt, enableCuboxInt int
	var cuboxEnc, cuboxTagsJSON string
	if err := api.store.SQL.QueryRowContext(ctx, `
		SELECT
			COALESCE(enable_rss, 1),
			COALESCE(enable_cubox, 0),
			COALESCE(cubox_api_url_enc, ''),
			COALESCE(cubox_folder, ''),
			COALESCE(cubox_tags, '[]')
		FROM user_channel_settings
		WHERE user_id = ?
	`, userID).Scan(&enableRSSInt, &enableCuboxInt, &cuboxEnc, &settings.CuboxFolder, &cuboxTagsJSON); err != nil {
		if err != sql.ErrNoRows {
			return settings, err
		}
	} else {
		settings.EnableRSS = enableRSSInt != 0
		settings.EnableCubox = enableCuboxInt != 0
	}
	if cuboxEnc != "" {
		if value, err := api.secretBox.DecryptString(cuboxEnc); err == nil {
			settings.CuboxAPIURL = strings.TrimSpace(value)
		}
	}
	settings.CuboxTags, _ = decodeStringSlice(cuboxTagsJSON)
	return settings, nil
}

func (api *API) pushToCubox(ctx context.Context, settings pushSettings, title, path, content string) error {
	apiURL := strings.TrimSpace(settings.CuboxAPIURL)
	if apiURL == "" {
		return fmt.Errorf("missing cubox api url")
	}

	requestBody := map[string]any{
		"type":        "memo",
		"title":       strings.TrimSpace(title),
		"content":     truncateRunes(strings.TrimSpace(content), 3000),
		"description": strings.TrimSpace(path),
	}
	if folder := strings.TrimSpace(settings.CuboxFolder); folder != "" {
		requestBody["folder"] = folder
	}
	if len(settings.CuboxTags) > 0 {
		requestBody["tags"] = settings.CuboxTags
	}
	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("encode cubox payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("build cubox request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("call cubox api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("cubox status %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return strings.TrimSpace(string(runes[:limit]))
}
func runeCount(value string) int { return len([]rune(strings.TrimSpace(value))) }
