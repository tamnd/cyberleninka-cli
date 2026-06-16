package cyberleninka

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/tamnd/any-cli/kit"
	"github.com/tamnd/any-cli/kit/errs"
)

// domain.go registers the cyberleninka kit Domain so a blank import in a
// multi-domain host enables the driver:
//
//	import _ "github.com/tamnd/cyberleninka-cli/cyberleninka"
func init() { kit.Register(Domain{}) }

// Domain is the cyberleninka.ru driver.
type Domain struct{}

// Info describes the scheme and the identity the single-site binary inherits.
func (Domain) Info() kit.DomainInfo {
	return kit.DomainInfo{
		Scheme:  "cyberleninka",
		Aliases: []string{"cyber"},
		Hosts:   []string{Host},
		Identity: kit.Identity{
			Binary: "cyber",
			Short:  "Search scientific papers on CyberLeninka",
			Long: `cyber is a command-line tool for CyberLeninka (cyberleninka.ru), a Russian
open-access academic library with over 4 million peer-reviewed scientific papers.

It searches the public JSON API and prints clean records. No API key needed.

Quick start:
  cyber search "machine learning"
  cyber search "нейронные сети" --lang ru
  cyber article 9879640
  cyber suggest "machine"`,
			Site: Host,
			Repo: "https://github.com/tamnd/cyberleninka-cli",
		},
	}
}

// Register installs the client factory and the three cyberleninka operations onto app.
func (Domain) Register(app *kit.App) {
	app.SetClient(newClient)

	kit.Handle(app, kit.OpMeta{
		Name:    "search",
		Group:   "search",
		Summary: "Search scientific papers by keyword",
		Args:    []kit.Arg{{Name: "query", Help: "search keyword"}},
	}, searchOp)

	kit.Handle(app, kit.OpMeta{
		Name:     "article",
		Group:    "read",
		Single:   true,
		Resolver: true,
		URIType:  "article",
		Summary:  "Fetch metadata for one article",
		Args:     []kit.Arg{{Name: "id", Help: "article ID, slug, or URL"}},
	}, articleOp)

	kit.Handle(app, kit.OpMeta{
		Name:    "suggest",
		Group:   "search",
		Summary: "Autocomplete suggestions for a search prefix",
		Args:    []kit.Arg{{Name: "prefix", Help: "search prefix"}},
	}, suggestOp)
}

// newClient builds a Client from the kit Config.
func newClient(_ context.Context, cfg kit.Config) (any, error) {
	c := DefaultConfig()
	if cfg.Rate > 0 {
		c.Rate = cfg.Rate
	}
	if cfg.Retries > 0 {
		c.Retries = cfg.Retries
	}
	if cfg.Timeout > 0 {
		c.Timeout = cfg.Timeout
	}
	if cfg.UserAgent != "" {
		c.UserAgent = cfg.UserAgent
	}
	return NewClient(c), nil
}

// --- input structs ---

type searchInput struct {
	Query  string  `kit:"arg"          help:"search keyword"`
	Lang   string  `kit:"flag"         help:"language filter: ru or en (empty = both)"`
	Page   int     `kit:"flag"         help:"page number (1-based)"    default:"1"`
	Limit  int     `kit:"flag,inherit" help:"results per page (max 20)" default:"10"`
	Client *Client `kit:"inject"`
}

type articleInput struct {
	ID     string  `kit:"arg"   help:"article ID, slug, or URL"`
	Client *Client `kit:"inject"`
}

type suggestInput struct {
	Prefix string  `kit:"arg"   help:"search prefix"`
	Client *Client `kit:"inject"`
}

// --- handlers ---

func searchOp(ctx context.Context, in searchInput, emit func(Article) error) error {
	result, err := in.Client.Search(ctx, in.Query, in.Lang, in.Page, in.Limit)
	if err != nil {
		return mapErr(err)
	}
	if len(result.Articles) == 0 {
		return errs.NoResults("no results for %q", in.Query)
	}
	for _, a := range result.Articles {
		if err := emit(a); err != nil {
			return err
		}
	}
	return nil
}

func articleOp(ctx context.Context, in articleInput, emit func(*Article) error) error {
	a, err := in.Client.GetArticle(ctx, in.ID)
	if err != nil {
		return mapErr(err)
	}
	return emit(a)
}

func suggestOp(ctx context.Context, in suggestInput, emit func(Suggestion) error) error {
	suggestions, err := in.Client.Suggest(ctx, in.Prefix)
	if err != nil {
		return mapErr(err)
	}
	if len(suggestions) == 0 {
		return errs.NoResults("no suggestions for %q", in.Prefix)
	}
	for _, s := range suggestions {
		if err := emit(Suggestion{Text: s}); err != nil {
			return err
		}
	}
	return nil
}

// --- Resolver ---

// Classify turns a numeric ID or CyberLeninka URL into the canonical (uriType, id).
func (Domain) Classify(input string) (uriType, id string, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", errs.Usage("cyberleninka: empty input")
	}
	// Full URL.
	if u, err := url.Parse(input); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 3 && parts[0] == "article" && parts[1] == "n" {
			return "article", parts[2], nil
		}
		return "", "", errs.Usage("cyberleninka: unrecognized URL: %q", input)
	}
	// Numeric ID or bare slug.
	if isNumeric(input) || strings.Contains(input, "-") || strings.Contains(input, "_") {
		return "article", input, nil
	}
	return "", "", errs.Usage("cyberleninka: unrecognized reference: %q (pass an article ID, slug, or URL)", input)
}

// Locate returns the canonical cyberleninka.ru URL for a (uriType, id).
func (Domain) Locate(uriType, id string) (string, error) {
	switch uriType {
	case "article":
		if isNumeric(id) {
			return BaseURL + "/api/search?q=" + id, nil
		}
		return BaseURL + "/article/n/" + id, nil
	default:
		return "", errs.Usage("cyberleninka has no resource type %q", uriType)
	}
}

// mapErr converts library errors into kit error kinds with appropriate exit codes.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) {
		return errs.NotFound("%s", err.Error())
	}
	if errors.Is(err, ErrBlocked) {
		return errs.RateLimited("%s", err.Error())
	}
	return err
}
