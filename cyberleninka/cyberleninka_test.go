package cyberleninka_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tamnd/cyberleninka-cli/cyberleninka"
)

func TestDefaultConfig(t *testing.T) {
	cfg := cyberleninka.DefaultConfig()
	if cfg.Rate <= 0 {
		t.Errorf("Rate = %v, want > 0", cfg.Rate)
	}
	if cfg.Retries <= 0 {
		t.Errorf("Retries = %d, want > 0", cfg.Retries)
	}
	if cfg.Timeout <= 0 {
		t.Errorf("Timeout = %v, want > 0", cfg.Timeout)
	}
	if cfg.UserAgent == "" {
		t.Error("UserAgent is empty")
	}
}

func TestNewClientNotNil(t *testing.T) {
	c := cyberleninka.NewClient(cyberleninka.DefaultConfig())
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
}

func TestArticleRoundTrip(t *testing.T) {
	want := cyberleninka.Article{
		ID:      "9879640",
		Title:   "Machine Learning in Healthcare",
		Lang:    "en",
		Year:    2023,
		Journal: "Journal of Medical Systems",
		URL:     "https://cyberleninka.ru/article/n/machine-learning",
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got cyberleninka.Article
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.Title != want.Title || got.Year != want.Year {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestSearchResultRoundTrip(t *testing.T) {
	want := cyberleninka.SearchResult{
		Total: 100,
		Pages: 10,
		Articles: []cyberleninka.Article{
			{ID: "1", Title: "Paper A", Year: 2022},
		},
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got cyberleninka.SearchResult
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != want.Total || got.Pages != want.Pages || len(got.Articles) != 1 {
		t.Errorf("round-trip mismatch: got %+v", got)
	}
}

func TestClientSearchFromTestServer(t *testing.T) {
	payload := map[string]any{
		"total": 42,
		"pages": 5,
		"articles": []map[string]any{
			{
				"id":         "9879640",
				"name":       "Machine Learning in Healthcare",
				"lang":       "en",
				"authors":    []string{"Ivanov A.V."},
				"magazine":   "Med Systems",
				"magazineId": 123,
				"year":       2023,
				"annotation": "A great paper.",
				"keywords":   []string{"ml", "health"},
				"url":        "https://cyberleninka.ru/article/n/ml-health",
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	cfg := cyberleninka.DefaultConfig()
	cfg.Rate = 0
	cfg.BaseURL = srv.URL
	c := cyberleninka.NewClient(cfg)

	result, err := c.Search(context.Background(), "machine learning", "en", 1, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Total != 42 {
		t.Errorf("Total = %d, want 42", result.Total)
	}
	if len(result.Articles) != 1 {
		t.Fatalf("got %d articles, want 1", len(result.Articles))
	}
	a := result.Articles[0]
	if a.ID != "9879640" {
		t.Errorf("ID = %q, want 9879640", a.ID)
	}
	if a.Title != "Machine Learning in Healthcare" {
		t.Errorf("Title = %q", a.Title)
	}
}

func TestClientSuggestFromTestServer(t *testing.T) {
	suggestions := []string{"machine learning", "machine translation", "machine vision"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(suggestions)
	}))
	defer srv.Close()

	cfg := cyberleninka.DefaultConfig()
	cfg.Rate = 0
	cfg.BaseURL = srv.URL
	c := cyberleninka.NewClient(cfg)

	got, err := c.Suggest(context.Background(), "machine")
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d suggestions, want 3", len(got))
	}
	if got[0] != "machine learning" {
		t.Errorf("first suggestion = %q", got[0])
	}
}

func TestNotFoundResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := cyberleninka.DefaultConfig()
	cfg.Rate = 0
	cfg.Retries = 0
	cfg.BaseURL = srv.URL
	c := cyberleninka.NewClient(cfg)

	_, err := c.Search(context.Background(), "test", "", 1, 5)
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}

func TestGetRetriesOn503(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total": 0, "pages": 0, "articles": []any{},
		})
	}))
	defer srv.Close()

	cfg := cyberleninka.DefaultConfig()
	cfg.Rate = 0
	cfg.Retries = 5
	cfg.BaseURL = srv.URL
	c := cyberleninka.NewClient(cfg)

	start := time.Now()
	_, _ = c.Search(context.Background(), "test", "", 1, 5)
	if hits < 3 {
		t.Errorf("server saw %d hits, want >= 3", hits)
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Error("retries did not back off")
	}
}

func TestHostConstant(t *testing.T) {
	if cyberleninka.Host != "cyberleninka.ru" {
		t.Errorf("Host = %q, want cyberleninka.ru", cyberleninka.Host)
	}
}

func TestErrSentinels(t *testing.T) {
	if cyberleninka.ErrNotFound == nil {
		t.Error("ErrNotFound is nil")
	}
	if cyberleninka.ErrBlocked == nil {
		t.Error("ErrBlocked is nil")
	}
}
