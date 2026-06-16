// Package cyberleninka is the library behind the cyber command: the HTTP
// client, pacing, and the typed data models for cyberleninka.ru.
//
// CyberLeninka is a Russian open-access academic library with over 4 million
// peer-reviewed scientific papers. Its public JSON search API is available
// without any API key.
//
// Note: from datacenter IPs the API may time out. All automated tests use
// httptest to avoid network dependency.
package cyberleninka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// Host is the site this client talks to.
	Host = "cyberleninka.ru"

	// BaseURL is the root every request is built from.
	BaseURL = "https://" + Host

	// DefaultUserAgent is an honest identifier for the CLI.
	DefaultUserAgent = "cyber/dev (+https://github.com/tamnd/cyberleninka-cli)"
)

// Sentinel errors returned by Client methods.
var (
	// ErrNotFound is returned when an article does not exist (404).
	ErrNotFound = errors.New("not found")

	// ErrBlocked is returned when the site blocks or times out the request.
	ErrBlocked = errors.New("blocked or timed out; check your network connection")
)

// Article holds metadata for one scientific paper from CyberLeninka.
type Article struct {
	ID        string   `json:"id"`
	Slug      string   `json:"slug,omitempty"`
	Title     string   `json:"title"`
	Lang      string   `json:"lang,omitempty"`
	Authors   []string `json:"authors,omitempty"`
	Journal   string   `json:"journal,omitempty"`
	JournalID string   `json:"journal_id,omitempty"`
	Year      int      `json:"year,omitempty"`
	Abstract  string   `json:"abstract,omitempty"`
	Keywords  []string `json:"keywords,omitempty"`
	DOI       string   `json:"doi,omitempty"`
	PDFURL    string   `json:"pdf_url,omitempty"`
	URL       string   `json:"url"`
}

// SearchResult holds the paginated result from the CyberLeninka search API.
type SearchResult struct {
	Total    int       `json:"total"`
	Pages    int       `json:"pages"`
	Articles []Article `json:"articles"`
}

// Suggestion holds one autocomplete suggestion.
type Suggestion struct {
	Text string `json:"suggestion"`
}

// Config holds constructor parameters for Client.
type Config struct {
	BaseURL   string
	UserAgent string
	Rate      time.Duration
	Retries   int
	Timeout   time.Duration
}

// DefaultConfig returns sensible defaults for cyberleninka.ru.
func DefaultConfig() Config {
	return Config{
		BaseURL:   BaseURL,
		UserAgent: DefaultUserAgent,
		Rate:      200 * time.Millisecond,
		Retries:   3,
		Timeout:   30 * time.Second,
	}
}

// Client is a rate-limited HTTP client for cyberleninka.ru.
type Client struct {
	cfg  Config
	http *http.Client
	mu   sync.Mutex
	last time.Time
}

// NewClient returns a Client configured with cfg.
func NewClient(cfg Config) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout},
	}
}

// apiSearchResponse matches the JSON returned by /api/search.
type apiSearchResponse struct {
	Total    int          `json:"total"`
	Pages    int          `json:"pages"`
	Articles []apiArticle `json:"articles"`
}

// apiArticle is the per-article object in the search response. The id and
// magazineId fields can come back as integers or strings depending on the API
// version, so we decode them as json.Number.
type apiArticle struct {
	ID         json.Number `json:"id"`
	Name       string      `json:"name"`
	Lang       string      `json:"lang"`
	Authors    []string    `json:"authors"`
	Magazine   string      `json:"magazine"`
	MagazineID json.Number `json:"magazineId"`
	Year       int         `json:"year"`
	Annotation string      `json:"annotation"`
	Keywords   []string    `json:"keywords"`
	URL        string      `json:"url"`
}

// toArticle converts an apiArticle to the public Article type.
func toArticle(a apiArticle) Article {
	slug := extractSlugFromURL(a.URL)
	pdfURL := ""
	if a.URL != "" {
		pdfURL = a.URL + "/viewer"
	}
	return Article{
		ID:        a.ID.String(),
		Slug:      slug,
		Title:     a.Name,
		Lang:      a.Lang,
		Authors:   a.Authors,
		Journal:   a.Magazine,
		JournalID: a.MagazineID.String(),
		Year:      a.Year,
		Abstract:  a.Annotation,
		Keywords:  a.Keywords,
		PDFURL:    pdfURL,
		URL:       a.URL,
	}
}

// Search executes a full-text search against the CyberLeninka API.
// page is 1-based. size is clamped to 1..20.
func (c *Client) Search(ctx context.Context, query, lang string, page, size int) (*SearchResult, error) {
	if size <= 0 {
		size = 10
	}
	if size > 20 {
		size = 20
	}
	if page <= 0 {
		page = 1
	}

	vals := url.Values{
		"q":    {query},
		"page": {fmt.Sprintf("%d", page)},
		"size": {fmt.Sprintf("%d", size)},
	}
	if lang != "" {
		vals.Set("lang", lang)
	}
	u := c.cfg.BaseURL + "/api/search?" + vals.Encode()

	body, err := c.get(ctx, u)
	if err != nil {
		return nil, err
	}

	var resp apiSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}

	articles := make([]Article, len(resp.Articles))
	for i, a := range resp.Articles {
		articles[i] = toArticle(a)
	}

	return &SearchResult{
		Total:    resp.Total,
		Pages:    resp.Pages,
		Articles: articles,
	}, nil
}

// GetArticle fetches one article by numeric ID or URL slug.
func (c *Client) GetArticle(ctx context.Context, idOrSlug string) (*Article, error) {
	idOrSlug = strings.TrimSpace(idOrSlug)

	// If it is a full URL, extract the slug.
	if strings.HasPrefix(idOrSlug, "http") {
		if u, err := url.Parse(idOrSlug); err == nil {
			idOrSlug = extractSlugFromURL(u.String())
		}
	}

	// If it looks like a numeric ID, search by ID.
	if isNumeric(idOrSlug) {
		u := c.cfg.BaseURL + "/api/search?q=" + url.QueryEscape(idOrSlug) + "&size=5"
		body, err := c.get(ctx, u)
		if err != nil {
			return nil, err
		}
		var resp apiSearchResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("decode article response: %w", err)
		}
		for _, a := range resp.Articles {
			if a.ID.String() == idOrSlug {
				art := toArticle(a)
				return &art, nil
			}
		}
		if len(resp.Articles) > 0 {
			art := toArticle(resp.Articles[0])
			return &art, nil
		}
		return nil, ErrNotFound
	}

	// Otherwise treat as a slug: fetch the article HTML page.
	u := c.cfg.BaseURL + "/article/n/" + strings.TrimPrefix(idOrSlug, "/")
	body, err := c.get(ctx, u)
	if err != nil {
		return nil, err
	}
	art := parseArticlePage(body, u)
	if art.Title == "" {
		return nil, ErrNotFound
	}
	return &art, nil
}

// Suggest returns autocomplete suggestions for the given prefix.
func (c *Client) Suggest(ctx context.Context, prefix string) ([]string, error) {
	u := c.cfg.BaseURL + "/api/suggest?q=" + url.QueryEscape(prefix)
	body, err := c.get(ctx, u)
	if err != nil {
		return nil, err
	}
	var suggestions []string
	if err := json.Unmarshal(body, &suggestions); err != nil {
		return nil, fmt.Errorf("decode suggestions: %w", err)
	}
	return suggestions, nil
}

// get fetches a URL with retries and pacing.
func (c *Client) get(ctx context.Context, rawURL string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.cfg.Retries; attempt++ {
		if attempt > 0 {
			wait := time.Duration(attempt) * 500 * time.Millisecond
			if wait > 5*time.Second {
				wait = 5 * time.Second
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}
		body, retry, err := c.do(ctx, rawURL)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, fmt.Errorf("get %s: %w", rawURL, lastErr)
}

func (c *Client) do(ctx context.Context, rawURL string) ([]byte, bool, error) {
	c.pace()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Accept", "application/json, text/html, */*")

	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, false, ErrBlocked
		}
		return nil, true, err
	}
	defer func() { _ = resp.Body.Close() }()

	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, true, err
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, false, ErrNotFound
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusServiceUnavailable {
		return nil, true, fmt.Errorf("http %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("http %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("http %d", resp.StatusCode)
	}
	return b, false, nil
}

// pace enforces the inter-request delay.
func (c *Client) pace() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cfg.Rate > 0 {
		since := time.Since(c.last)
		if since < c.cfg.Rate {
			time.Sleep(c.cfg.Rate - since)
		}
	}
	c.last = time.Now()
}

// parseArticlePage extracts article metadata from an HTML page body.
func parseArticlePage(body []byte, pageURL string) Article {
	art := Article{URL: pageURL}
	s := string(body)

	if v := extractMeta(s, "og:title"); v != "" {
		art.Title = v
	}
	if v := extractMeta(s, "og:description"); v != "" {
		art.Abstract = v
	}
	art.Slug = extractSlugFromURL(pageURL)
	return art
}

// extractMeta extracts the content of an og meta tag from HTML.
func extractMeta(html, property string) string {
	needle := `property="` + property + `"`
	idx := strings.Index(html, needle)
	if idx == -1 {
		return ""
	}
	rest := html[idx:]
	cidx := strings.Index(rest, `content="`)
	if cidx == -1 {
		return ""
	}
	rest = rest[cidx+9:]
	end := strings.Index(rest, `"`)
	if end == -1 {
		return ""
	}
	return rest[:end]
}

// extractSlugFromURL pulls the article slug from a CyberLeninka article URL.
func extractSlugFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if p == "n" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// isNumeric returns true when s consists entirely of decimal digits.
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
