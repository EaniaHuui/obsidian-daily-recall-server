package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"
)

type syncRequest struct {
	Notes        []syncNote `json:"notes"`
	BatchIndex   int        `json:"batch_index"`
	TotalBatches int        `json:"total_batches"`
}

type syncNote struct {
	Path          string `json:"path"`
	Title         string `json:"title"`
	Content       string `json:"content"`
	ContentHash   string `json:"content_hash"`
	NoteUpdatedAt string `json:"note_updated_at"`
}

type deletedRequest struct {
	Paths []string `json:"paths"`
}

func (api *API) syncNotes(w http.ResponseWriter, r *http.Request) {
	current, err := api.requireSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}

	var req syncRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if len(req.Notes) > 50 {
		writeError(w, http.StatusBadRequest, "too_many_notes", "maximum 50 notes per batch")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	tx, err := api.store.SQL.BeginTx(ctx, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_begin_failed", "failed to open database transaction")
		return
	}
	defer func() {
		_ = tx.Rollback()
	}()

	now := api.now().Format(time.RFC3339)
	synced := 0
	skipped := 0
	for _, note := range req.Notes {
		path := strings.TrimSpace(note.Path)
		if path == "" || strings.TrimSpace(note.ContentHash) == "" {
			continue
		}
		noteUpdatedAt := strings.TrimSpace(note.NoteUpdatedAt)
		if noteUpdatedAt == "" {
			noteUpdatedAt = now
		}

		var existingHash string
		err := tx.QueryRowContext(ctx, `
			SELECT content_hash
			FROM notes
			WHERE user_id = ?
			  AND path = ?
		`, current.UserID, path).Scan(&existingHash)
		if err == nil && existingHash == note.ContentHash {
			skipped++
			continue
		}

		noteID := ""
		switch {
		case err == nil:
			if err := tx.QueryRowContext(ctx, `
				SELECT id FROM notes WHERE user_id = ? AND path = ?
			`, current.UserID, path).Scan(&noteID); err != nil {
				writeError(w, http.StatusInternalServerError, "note_lookup_failed", "failed to read existing note")
				return
			}
		case err == sql.ErrNoRows:
			noteID, err = randomHex(16)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "id_generation_failed", "failed to generate note id")
				return
			}
		default:
			writeError(w, http.StatusInternalServerError, "note_lookup_failed", "failed to read existing note")
			return
		}

		sizeBytes := len([]byte(note.Content))
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO notes (
				id, user_id, path, title, content, size_bytes,
				content_hash, note_updated_at, synced_at, is_deleted
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
			ON CONFLICT(user_id, path) DO UPDATE SET
				title = excluded.title,
				content = excluded.content,
				size_bytes = excluded.size_bytes,
				content_hash = excluded.content_hash,
				note_updated_at = excluded.note_updated_at,
				synced_at = excluded.synced_at,
				is_deleted = 0
		`, noteID, current.UserID, path, strings.TrimSpace(note.Title), note.Content, sizeBytes, note.ContentHash, noteUpdatedAt, now); err != nil {
			writeError(w, http.StatusInternalServerError, "note_save_failed", "failed to save note")
			return
		}
		synced++
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "db_commit_failed", "failed to save notes")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"synced":      synced,
		"skipped":     skipped,
		"batch_index": req.BatchIndex,
	})
}

func (api *API) markDeletedNotes(w http.ResponseWriter, r *http.Request) {
	current, err := api.requireSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}

	var req deletedRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	now := api.now().Format(time.RFC3339)
	for _, path := range req.Paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		if _, err := api.store.SQL.ExecContext(ctx, `
			UPDATE notes
			SET is_deleted = 1, synced_at = ?
			WHERE user_id = ?
			  AND path = ?
		`, now, current.UserID, trimmed); err != nil {
			writeError(w, http.StatusInternalServerError, "note_delete_failed", "failed to mark deleted notes")
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"deleted": len(req.Paths),
	})
}

func (api *API) noteHashes(w http.ResponseWriter, r *http.Request) {
	current, err := api.requireSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	rows, err := api.store.SQL.QueryContext(ctx, `
		SELECT path, content_hash
		FROM notes
		WHERE user_id = ?
		  AND is_deleted = 0
		ORDER BY path ASC
	`, current.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "note_hashes_failed", "failed to list note hashes")
		return
	}
	defer rows.Close()

	type item struct {
		Path        string `json:"path"`
		ContentHash string `json:"content_hash"`
	}
	items := make([]item, 0)
	for rows.Next() {
		var currentItem item
		if err := rows.Scan(&currentItem.Path, &currentItem.ContentHash); err != nil {
			writeError(w, http.StatusInternalServerError, "note_hash_scan_failed", "failed to read note hashes")
			return
		}
		items = append(items, currentItem)
	}

	writeJSON(w, http.StatusOK, items)
}
