package handlers

import (
	"net/http"
)

func RegisterRoutes(mux *http.ServeMux, deps Dependencies) {
	api := newAPI(deps)

	mux.HandleFunc("GET /healthz", healthz)

	mux.HandleFunc("POST /api/v1/auth/register", api.register)
	mux.HandleFunc("POST /api/v1/auth/login", api.login)
	mux.HandleFunc("POST /api/v1/auth/anonymous", api.anonymousLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", api.logout)
	mux.HandleFunc("GET /api/v1/auth/sessions", api.sessions)
	mux.HandleFunc("DELETE /api/v1/auth/sessions/{session_id}", api.revokeSession)

	mux.HandleFunc("GET /api/v1/user/settings", api.getUserSettings)
	mux.HandleFunc("PUT /api/v1/user/settings", api.putUserSettings)
	mux.HandleFunc("GET /api/v1/user/rss", api.getUserRSS)
	mux.HandleFunc("POST /api/v1/user/rss/reset", api.resetUserRSS)
	mux.HandleFunc("DELETE /api/v1/user/account", notImplemented("user.account.delete"))
	mux.HandleFunc("GET /api/v1/user/export", notImplemented("user.export"))

	mux.HandleFunc("POST /api/v1/notes/sync", api.syncNotes)
	mux.HandleFunc("POST /api/v1/notes/deleted", api.markDeletedNotes)
	mux.HandleFunc("GET /api/v1/notes/hashes", api.noteHashes)

	mux.HandleFunc("GET /api/v1/push/history", api.pushHistory)
	mux.HandleFunc("GET /api/v1/push/history/{id}", api.pushHistoryDetail)
	mux.HandleFunc("POST /api/v1/recalls/queue", api.queueLocalRecalls)
	mux.HandleFunc("GET /api/v1/recalls/queue/status", api.queueStatus)

	mux.HandleFunc("GET /api/v1/rss/{token}", api.publicRSS)
	mux.HandleFunc("GET /api/v1/rss/{token}/item/{id}", api.publicRSSItem)
	mux.HandleFunc("GET /api/v1/rss/{token}/day/{day}", api.publicRSSDay)
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func notImplemented(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"error":   "not_implemented",
			"handler": name,
		})
	}
}
