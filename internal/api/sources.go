package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"mono/internal/domain"
	"mono/internal/storage"
)

const maxSources = 30

func defaultSources() []storage.Source {
	return []storage.Source{
		{Name: "SecurityLab", Kind: "rss", URL: "https://www.securitylab.ru/_services/export/rss/news/", Enabled: true, Fetch: true},
		{Name: "Хакер", Kind: "rss", URL: "https://xakep.ru/feed/", Enabled: true, Fetch: true},
		{Name: "CNews", Kind: "rss", URL: "https://www.cnews.ru/inc/rss/news.xml", Enabled: true, Fetch: true},
		{Name: "ComNews", Kind: "rss", URL: "https://www.comnews.ru/rss", Enabled: true, Fetch: true, Article: ".field-name-body"},
		{Name: "Positive Technologies", Kind: "api", URL: "https://www.ptsecurity.com/api/news", Enabled: true, Fetch: true, Items: "items"},
	}
}

func (s *Server) ensureSources(ctx context.Context, userID string) ([]storage.Source, error) {
	defaults := defaultSources()
	for i := range defaults {
		id, err := randomToken()
		if err != nil {
			return nil, err
		}
		defaults[i].ID = id
	}
	return s.store.EnsureSources(ctx, userID, defaults)
}

func (s *Server) listSources(w http.ResponseWriter, r *http.Request) {
	list, err := s.ensureSources(r.Context(), currentUser(r).ID)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": list})
}

func (s *Server) createSource(w http.ResponseWriter, r *http.Request) {
	var body sourceBody
	if !decodeSmallJSON(w, r, &body) {
		return
	}
	item, err := normalizeSource(body)
	if err != nil {
		writeInput(w, err)
		return
	}
	existing, err := s.ensureSources(r.Context(), currentUser(r).ID)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	if len(existing) >= maxSources {
		writeErr(w, http.StatusBadRequest, "можно сохранить не больше 30 источников")
		return
	}
	id, err := randomToken()
	if err != nil {
		s.databaseError(w, err)
		return
	}
	item.ID = id
	created, err := s.store.CreateSource(r.Context(), currentUser(r).ID, item)
	if errors.Is(err, storage.ErrSourceTaken) {
		writeErr(w, http.StatusConflict, "такой адрес уже есть в списке")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"source": created})
}

func (s *Server) updateSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idOK(id) {
		writeErr(w, http.StatusNotFound, "источник не найден")
		return
	}
	var body sourceBody
	if !decodeSmallJSON(w, r, &body) {
		return
	}
	item, err := normalizeSource(body)
	if err != nil {
		writeInput(w, err)
		return
	}
	item.ID = id
	updated, err := s.store.UpdateSource(r.Context(), currentUser(r).ID, item)
	if errors.Is(err, storage.ErrSourceTaken) {
		writeErr(w, http.StatusConflict, "такой адрес уже есть в списке")
		return
	}
	if errors.Is(err, storage.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "источник не найден")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": updated})
}

func (s *Server) deleteSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idOK(id) {
		writeErr(w, http.StatusNotFound, "источник не найден")
		return
	}
	err := s.store.DeleteSource(r.Context(), currentUser(r).ID, id)
	if errors.Is(err, storage.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "источник не найден")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) enabledLiveSources(ctx context.Context, userID string) ([]domain.LiveSource, error) {
	list, err := s.ensureSources(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.LiveSource, 0, len(list))
	for _, item := range list {
		if !item.Enabled {
			continue
		}
		out = append(out, domain.LiveSource{
			ID: item.ID, Name: item.Name, Kind: item.Kind, URL: item.URL,
			Item: item.Item, Title: item.Title, Link: item.Link, Date: item.Date, Text: item.Text,
			Items: item.Items, Fetch: item.Fetch, Article: item.Article,
		})
	}
	return out, nil
}

type sourceBody struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	URL     string `json:"url"`
	Enabled *bool  `json:"enabled"`
	Fetch   *bool  `json:"fetch"`
	Article string `json:"article"`
	Item    string `json:"item"`
	Title   string `json:"title"`
	Link    string `json:"link"`
	Date    string `json:"date"`
	Text    string `json:"text"`
	Items   string `json:"items"`
}

func normalizeSource(body sourceBody) (storage.Source, error) {
	item := storage.Source{
		Name:    strings.TrimSpace(body.Name),
		Kind:    strings.ToLower(strings.TrimSpace(body.Kind)),
		Article: strings.TrimSpace(body.Article),
		Item:    strings.TrimSpace(body.Item),
		Title:   strings.TrimSpace(body.Title),
		Link:    strings.TrimSpace(body.Link),
		Date:    strings.TrimSpace(body.Date),
		Text:    strings.TrimSpace(body.Text),
		Items:   strings.TrimSpace(body.Items),
		Enabled: body.Enabled == nil || *body.Enabled,
		Fetch:   body.Fetch == nil || *body.Fetch,
	}
	if item.Name == "" || utf8.RuneCountInString(item.Name) > 80 {
		return storage.Source{}, &inputError{msg: "укажите название источника до 80 символов"}
	}
	switch item.Kind {
	case "rss", "html", "api":
	default:
		return storage.Source{}, &inputError{msg: "тип источника: rss, html или api"}
	}
	rawURL, err := publicSiteURL(body.URL)
	if err != nil {
		return storage.Source{}, &inputError{msg: "укажите публичный адрес http или https"}
	}
	item.URL = rawURL
	for _, field := range []string{item.Article, item.Item, item.Title, item.Link, item.Date, item.Text, item.Items} {
		if utf8.RuneCountInString(field) > 200 {
			return storage.Source{}, &inputError{msg: "поле селектора длиннее 200 символов"}
		}
	}
	return item, nil
}

func publicSiteURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("адрес")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("схема")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return "", errors.New("хост")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !publicIP(ip) {
			return "", errors.New("адрес")
		}
		return parsed.String(), nil
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return "", errors.New("имя")
	}
	for _, ip := range ips {
		if !publicIP(ip) {
			return "", errors.New("адрес")
		}
	}
	return parsed.String(), nil
}

func publicIP(ip net.IP) bool {
	return ip != nil && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsUnspecified() && !ip.IsMulticast()
}
