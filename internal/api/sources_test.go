package api

import (
	"net/http"
	"testing"
)

func TestSourcesSeedEditAndStayDeleted(t *testing.T) {
	ts, _ := newAPI(t, &stubRunner{}, nil, true)
	code, doc, raw := doJSON(t, http.MethodGet, ts.URL+"/api/v1/sources", "")
	if code != http.StatusOK {
		t.Fatalf("list %d %s", code, raw)
	}
	list, _ := doc["sources"].([]any)
	if len(list) != 5 {
		t.Fatalf("defaults %+v", doc)
	}
	first, _ := list[0].(map[string]any)
	if first["name"] != "SecurityLab" || first["enabled"] != true {
		t.Fatalf("first %+v", first)
	}

	code, doc, raw = doJSON(t, http.MethodPost, ts.URL+"/api/v1/sources",
		`{"name":"Пример","kind":"rss","url":"https://example.com/feed.xml","enabled":true,"fetch":false}`)
	if code != http.StatusCreated {
		t.Fatalf("create %d %s", code, raw)
	}
	created, _ := doc["source"].(map[string]any)
	id, _ := created["id"].(string)
	if id == "" || created["url"] != "https://example.com/feed.xml" {
		t.Fatalf("created %+v", created)
	}

	code, _, raw = doJSON(t, http.MethodPost, ts.URL+"/api/v1/sources",
		`{"name":"Локальный","kind":"rss","url":"http://127.0.0.1/feed"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("local %d %s", code, raw)
	}

	code, doc, raw = doJSON(t, http.MethodPatch, ts.URL+"/api/v1/sources/"+id,
		`{"name":"Пример 2","kind":"html","url":"https://example.com/news","enabled":false,"fetch":true,"item":"article"}`)
	if code != http.StatusOK {
		t.Fatalf("patch %d %s", code, raw)
	}
	updated, _ := doc["source"].(map[string]any)
	if updated["name"] != "Пример 2" || updated["enabled"] != false || updated["kind"] != "html" {
		t.Fatalf("updated %+v", updated)
	}

	for _, item := range list {
		row, _ := item.(map[string]any)
		sid, _ := row["id"].(string)
		code, _, raw = doJSON(t, http.MethodDelete, ts.URL+"/api/v1/sources/"+sid, "")
		if code != http.StatusNoContent {
			t.Fatalf("delete %s %d %s", sid, code, raw)
		}
	}
	code, _, raw = doJSON(t, http.MethodDelete, ts.URL+"/api/v1/sources/"+id, "")
	if code != http.StatusNoContent {
		t.Fatalf("delete added %d %s", code, raw)
	}
	code, doc, raw = doJSON(t, http.MethodGet, ts.URL+"/api/v1/sources", "")
	if code != http.StatusOK {
		t.Fatalf("again %d %s", code, raw)
	}
	again, _ := doc["sources"].([]any)
	if len(again) != 0 {
		t.Fatalf("deleted sources returned %+v", again)
	}
}
