package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/xml"
	"html"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
)

type rssChannel struct {
	XMLName       xml.Name  `xml:"channel"`
	Title         string    `xml:"title"`
	Link          string    `xml:"link"`
	Description   string    `xml:"description"`
	Language      string    `xml:"language"`
	LastBuildDate string    `xml:"lastBuildDate,omitempty"`
	Items         []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
}

type rssDoc struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type userRSSResponse struct {
	RSSURL string `json:"rss_url"`
}

func (api *API) getUserRSS(w http.ResponseWriter, r *http.Request) {
	current, err := api.requireSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	token, err := api.ensureUserRSSFeedToken(ctx, current.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "rss_token_failed", "failed to prepare rss feed token")
		return
	}

	writeJSON(w, http.StatusOK, userRSSResponse{
		RSSURL: api.rssFeedURL(token),
	})
}

func (api *API) resetUserRSS(w http.ResponseWriter, r *http.Request) {
	current, err := api.requireSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	token, err := api.rotateUserRSSFeedToken(ctx, current.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "rss_token_rotate_failed", "failed to reset rss feed token")
		return
	}

	writeJSON(w, http.StatusOK, userRSSResponse{
		RSSURL: api.rssFeedURL(token),
	})
}

func (api *API) publicRSS(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.PathValue("token"))
	if token == "" {
		http.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var userID string
	row := api.store.SQL.QueryRowContext(ctx, `
		SELECT user_id
		FROM rss_feeds
		WHERE token_hash = ?
	`, hashToken(token))
	if err := row.Scan(&userID); err != nil {
		http.NotFound(w, r)
		return
	}
	if !api.isRSSEnabled(ctx, userID) {
		http.NotFound(w, r)
		return
	}

	rows, err := api.store.SQL.QueryContext(ctx, `
		SELECT id, note_title, note_path, COALESCE(summary, ''), pushed_at
		FROM push_history
		WHERE user_id = ?
		ORDER BY pushed_at DESC
		LIMIT 100
	`, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "rss_query_failed", "failed to read rss data")
		return
	}
	defer rows.Close()

	items := make([]rssItem, 0, 100)
	lastBuild := ""
	for rows.Next() {
		var id, title, path, summary, pushedAt string
		if err := rows.Scan(&id, &title, &path, &summary, &pushedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "rss_scan_failed", "failed to read rss items")
			return
		}
		if lastBuild == "" {
			lastBuild = pushedAt
		}
		items = append(items, rssItem{
			Title:       strings.TrimSpace(title),
			Link:        api.rssItemURL(token, id),
			GUID:        id,
			Description: truncateRunes(strings.TrimSpace(summary), 2000),
			PubDate:     pushedAt,
		})
	}

	channel := rssChannel{
		Title:         "Obsidian 每日回顾",
		Link:          strings.TrimRight(api.cfg.BaseURL, "/"),
		Description:   "Obsidian 每日回顾私有订阅",
		Language:      "zh-CN",
		LastBuildDate: lastBuild,
		Items:         items,
	}
	doc := rssDoc{
		Version: "2.0",
		Channel: channel,
	}

	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(doc)
}

func (api *API) publicRSSDay(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.PathValue("token"))
	day := strings.TrimSpace(r.PathValue("day"))
	if token == "" || day == "" {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var userID string
	row := api.store.SQL.QueryRowContext(ctx, `
		SELECT user_id
		FROM rss_feeds
		WHERE token_hash = ?
	`, hashToken(token))
	if err := row.Scan(&userID); err != nil {
		http.NotFound(w, r)
		return
	}
	if !api.isRSSEnabled(ctx, userID) {
		http.NotFound(w, r)
		return
	}

	rows, err := api.store.SQL.QueryContext(ctx, `
		SELECT ph.id, ph.note_title, ph.note_path, COALESCE(ph.summary, ''), ph.pushed_at
		FROM push_history ph
		WHERE ph.user_id = ?
		  AND substr(ph.pushed_at, 1, 10) = ?
		ORDER BY ph.pushed_at DESC
	`, userID, day)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer rows.Close()

	type rowItem struct {
		ID       string
		Title    string
		Path     string
		Summary  string
		Preview  string
		Link     string
		PushedAt string
	}
	items := make([]rowItem, 0, 20)
	for rows.Next() {
		var id, title, path, summary, pushedAt string
		if err := rows.Scan(&id, &title, &path, &summary, &pushedAt); err != nil {
			continue
		}
		items = append(items, rowItem{
			ID:       id,
			Title:    title,
			Path:     path,
			Summary:  html.UnescapeString(summary),
			Preview:  summaryPreview(summary, 160),
			Link:     api.rssItemURL(token, id),
			PushedAt: pushedAt,
		})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	const page = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"/><meta name="viewport" content="width=device-width,initial-scale=1"/><title>每日回顾 {{.Day}}</title>
<style>
body{margin:0;background:#f3f4f6;color:#111827;font:16px/1.55 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,PingFang SC,Hiragino Sans GB,Microsoft YaHei,sans-serif}
.wrap{max-width:860px;margin:0 auto;padding:14px}
.header{margin:2px 2px 12px}
.title{font-size:34px;line-height:1.18;font-weight:800;letter-spacing:0;margin:0}
.sub{color:#6b7280;font-size:13px;margin-top:4px}
.list{display:grid;gap:10px}
.row{display:block;background:#fff;border:1px solid #e8eaef;border-radius:12px;padding:12px 13px;text-decoration:none;color:inherit}
.row:active{transform:scale(.998)}
.row-title{font-size:22px;line-height:1.3;font-weight:700;margin:0 0 6px}
.meta{font-size:12px;color:#6b7280;margin-bottom:6px;word-break:break-all}
.excerpt{margin:0;color:#374151;font-size:14px;line-height:1.5}
@media (max-width:768px){.wrap{padding:10px}.title{font-size:26px}.row{padding:11px 12px}.row-title{font-size:18px}.excerpt{font-size:13px}}
</style></head>
<body><div class="wrap"><div class="header"><h1 class="title">每日回顾 {{.Day}}</h1><div class="sub">共 {{len .Items}} 条</div></div><section class="list">{{range .Items}}<a class="row" href="{{.Link}}"><h3 class="row-title">{{.Title}}</h3><div class="meta">{{.Path}} · {{.PushedAt}}</div><p class="excerpt">{{.Preview}}</p></a>{{end}}</section></div></body></html>`
	tpl := template.Must(template.New("rss-day").Parse(page))
	_ = tpl.Execute(w, map[string]any{"Day": day, "Items": items})
}

type rssPublicItemView struct {
	Title       string
	Path        string
	Summary     string
	SummaryHTML template.HTML
	ContentHTML template.HTML
	PushedAt    string
}

func (api *API) publicRSSItem(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.PathValue("token"))
	id := strings.TrimSpace(r.PathValue("id"))
	if token == "" || id == "" {
		http.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var userID string
	row := api.store.SQL.QueryRowContext(ctx, `
		SELECT user_id
		FROM rss_feeds
		WHERE token_hash = ?
	`, hashToken(token))
	if err := row.Scan(&userID); err != nil {
		http.NotFound(w, r)
		return
	}
	if !api.isRSSEnabled(ctx, userID) {
		http.NotFound(w, r)
		return
	}

	var noteID string
	var view rssPublicItemView
	itemRow := api.store.SQL.QueryRowContext(ctx, `
		SELECT id, note_id, note_title, note_path, COALESCE(summary, ''), pushed_at
		FROM push_history
		WHERE id = ?
		  AND user_id = ?
	`, id, userID)
	if err := itemRow.Scan(new(string), &noteID, &view.Title, &view.Path, &view.Summary, &view.PushedAt); err != nil {
		http.NotFound(w, r)
		return
	}

	var rawContent string
	_ = api.store.SQL.QueryRowContext(ctx, `
		SELECT COALESCE(content, '')
		FROM notes
		WHERE user_id = ?
		  AND (id = ? OR path = ?)
		ORDER BY CASE WHEN id = ? THEN 0 ELSE 1 END
		LIMIT 1
	`, userID, noteID, view.Path, noteID).Scan(&rawContent)

	content := strings.TrimSpace(rawContent)
	view.Summary = ""
	view.SummaryHTML = ""
	view.ContentHTML = renderMarkdownHTML(content)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	const page = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width,initial-scale=1" />
  <title>{{.Title}}</title>
  <style>
    :root { color-scheme: light; }
    body { margin:0; background:#f5f7fb; color:#111827; font:16px/1.72 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,PingFang SC,Hiragino Sans GB,Microsoft YaHei,sans-serif; }
    .wrap { max-width:880px; margin:0 auto; padding:24px 16px 40px; }
    .card { background:#fff; border:1px solid #e6e8ee; border-radius:12px; padding:22px 22px 18px; box-shadow:0 10px 28px rgba(17,24,39,.05); }
    .title { margin:0 0 10px; font-size:30px; line-height:1.28; letter-spacing:0; }
    .meta { color:#6b7280; font-size:14px; margin:0 0 16px; word-break:break-all; }
    .summary { background:#f0f7ff; border:1px solid #d8e9ff; border-left:4px solid #3b82f6; padding:11px 12px; border-radius:8px; margin-bottom:16px; white-space:pre-wrap; color:#1f2937; }
    .md { color:#111827; }
    .md > *:first-child { margin-top:0; }
    .md h1, .md h2, .md h3, .md h4 { margin:1.2em 0 .45em; line-height:1.34; }
    .md h1 { font-size:1.7rem; }
    .md h2 { font-size:1.35rem; padding-bottom:.2em; border-bottom:1px solid #edf0f5; }
    .md h3 { font-size:1.13rem; }
    .md p { margin:.66em 0; }
    .md ul, .md ol { margin:.62em 0 .62em 1.35em; padding:0; }
    .md li { margin:.2em 0; }
    .md blockquote { margin:.9em 0; padding:.3em .9em; border-left:4px solid #d6deeb; color:#4b5563; background:#f8fafc; border-radius:6px; }
    .md code { font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace; font-size:.92em; background:#f3f4f6; padding:.1em .35em; border-radius:5px; }
    .md pre { margin:.9em 0; padding:13px 14px; border-radius:10px; overflow:auto; background:#0b1220; color:#e5e7eb; }
    .md pre code { background:transparent; padding:0; color:inherit; }
    .md a { color:#2563eb; text-decoration:none; border-bottom:1px solid rgba(37,99,235,.28); }
    .md a:hover { border-bottom-color:#2563eb; }
    .md hr { border:none; border-top:1px solid #e6e8ee; margin:1.2em 0; }
    .md table { border-collapse:collapse; width:100%; display:block; overflow:auto; }
    .md th, .md td { border:1px solid #e5e7eb; padding:8px 10px; text-align:left; }
    .md img { max-width:100%; height:auto; border-radius:8px; }
    @media (max-width: 768px) {
      body { font-size:15px; line-height:1.68; }
      .wrap { padding:10px 10px 20px; }
      .card { border-radius:10px; padding:14px 14px 12px; box-shadow:0 6px 18px rgba(17,24,39,.05); }
      .title { font-size:24px; line-height:1.32; margin-bottom:8px; }
      .meta { font-size:12px; margin-bottom:12px; }
      .summary { font-size:14px; padding:10px; margin-bottom:12px; }
      .md h1 { font-size:1.45rem; }
      .md h2 { font-size:1.2rem; }
      .md h3 { font-size:1.05rem; }
      .md p { margin:.58em 0; }
      .md ul, .md ol { margin:.55em 0 .55em 1.15em; }
      .md pre { padding:10px 11px; border-radius:8px; font-size:13px; line-height:1.55; -webkit-overflow-scrolling:touch; }
      .md code { font-size:.9em; }
      .md table { font-size:13px; }
      .md th, .md td { padding:6px 8px; }
    }
    @media (max-width: 390px) {
      .wrap { padding:8px 8px 16px; }
      .card { padding:12px 11px 10px; }
      .title { font-size:21px; }
    }
  </style>
</head>
<body>
  <div class="wrap">
    <article class="card">
      <h1 class="title">{{.Title}}</h1>
      <div class="meta">{{.Path}} · {{.PushedAt}}</div>
      <div class="md">{{.ContentHTML}}</div>
    </article>
  </div>
</body>
</html>`
	tpl := template.Must(template.New("rss-item").Parse(page))
	_ = tpl.Execute(w, view)
}

func renderMarkdownHTML(markdownText string) template.HTML {
	src := strings.TrimSpace(markdownText)
	if src == "" {
		return template.HTML("<p>(empty)</p>")
	}
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.Strikethrough,
			extension.Table,
			extension.TaskList,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
	)
	var out bytes.Buffer
	if err := md.Convert([]byte(src), &out); err != nil {
		return template.HTML("<pre>" + html.EscapeString(src) + "</pre>")
	}
	return template.HTML(out.String())
}

func summaryPreview(input string, limit int) string {
	s := html.UnescapeString(strings.TrimSpace(input))
	replacer := strings.NewReplacer(
		"\r", " ",
		"\n", " ",
		"\t", " ",
		"#", "",
		"*", "",
		"`", "",
		">", "",
		"|", " ",
	)
	s = replacer.Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	return truncateRunes(s, limit)
}

func (api *API) rssFeedURL(token string) string {
	base := strings.TrimRight(api.cfg.BaseURL, "/")
	return base + "/api/v1/rss/" + token
}

func (api *API) rssItemURL(token, historyID string) string {
	base := strings.TrimRight(api.cfg.BaseURL, "/")
	return base + "/api/v1/rss/" + token + "/item/" + historyID
}

func (api *API) rssDayURL(token, day string) string {
	base := strings.TrimRight(api.cfg.BaseURL, "/")
	return base + "/api/v1/rss/" + token + "/day/" + day
}

func (api *API) isRSSEnabled(ctx context.Context, userID string) bool {
	var enabled int
	err := api.store.SQL.QueryRowContext(ctx, `
		SELECT COALESCE(enable_rss, 1)
		FROM user_channel_settings
		WHERE user_id = ?
	`, userID).Scan(&enabled)
	if err != nil {
		return true
	}
	return enabled != 0
}

func (api *API) ensureUserRSSFeedToken(ctx context.Context, userID string) (string, error) {
	var tokenEnc string
	err := api.store.SQL.QueryRowContext(ctx, `
		SELECT token_enc
		FROM rss_feeds
		WHERE user_id = ?
	`, userID).Scan(&tokenEnc)
	switch {
	case err == nil:
		token, decryptErr := api.secretBox.DecryptString(tokenEnc)
		if decryptErr != nil {
			return api.rotateUserRSSFeedToken(ctx, userID)
		}
		return strings.TrimSpace(token), nil
	case err == sql.ErrNoRows:
		return api.rotateUserRSSFeedToken(ctx, userID)
	default:
		return "", err
	}
}

func (api *API) rotateUserRSSFeedToken(ctx context.Context, userID string) (string, error) {
	token, err := randomHex(32)
	if err != nil {
		return "", err
	}
	tokenEnc, err := api.secretBox.EncryptString(token)
	if err != nil {
		return "", err
	}

	now := api.now().Format(time.RFC3339)
	_, err = api.store.SQL.ExecContext(ctx, `
		INSERT INTO rss_feeds (user_id, token_hash, token_enc, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			token_hash = excluded.token_hash,
			token_enc = excluded.token_enc,
			updated_at = excluded.updated_at
	`, userID, hashToken(token), tokenEnc, now, now)
	if err != nil {
		return "", err
	}
	return token, nil
}
