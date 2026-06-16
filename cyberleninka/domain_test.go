package cyberleninka_test

import (
	"testing"

	"github.com/tamnd/cyberleninka-cli/cyberleninka"
)

func TestDomainInfo(t *testing.T) {
	info := cyberleninka.Domain{}.Info()
	if info.Scheme != "cyberleninka" {
		t.Errorf("Scheme = %q, want cyberleninka", info.Scheme)
	}
	if len(info.Hosts) == 0 || info.Hosts[0] != cyberleninka.Host {
		t.Errorf("Hosts = %v, want [%s]", info.Hosts, cyberleninka.Host)
	}
	if info.Identity.Binary != "cyber" {
		t.Errorf("Identity.Binary = %q, want cyber", info.Identity.Binary)
	}
}

func TestClassifyArticle(t *testing.T) {
	cases := []struct {
		input   string
		wantTyp string
		wantID  string
		wantErr bool
	}{
		{"9879640", "article", "9879640", false},
		{"machine-learning-in-healthcare", "article", "machine-learning-in-healthcare", false},
		{"https://cyberleninka.ru/article/n/ml-in-healthcare", "article", "ml-in-healthcare", false},
		{"", "", "", true},
	}
	for _, tc := range cases {
		typ, id, err := cyberleninka.Domain{}.Classify(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Classify(%q) expected error, got nil", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("Classify(%q) unexpected error: %v", tc.input, err)
			continue
		}
		if typ != tc.wantTyp || id != tc.wantID {
			t.Errorf("Classify(%q) = (%q, %q), want (%q, %q)",
				tc.input, typ, id, tc.wantTyp, tc.wantID)
		}
	}
}

func TestLocate(t *testing.T) {
	cases := []struct {
		uriType string
		id      string
		want    string
		wantErr bool
	}{
		{"article", "ml-in-healthcare", "https://cyberleninka.ru/article/n/ml-in-healthcare", false},
		{"article", "9879640", "https://cyberleninka.ru/api/search?q=9879640", false},
		{"unknown", "1", "", true},
	}
	for _, tc := range cases {
		got, err := cyberleninka.Domain{}.Locate(tc.uriType, tc.id)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Locate(%q, %q) expected error, got %q", tc.uriType, tc.id, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Locate(%q, %q) unexpected error: %v", tc.uriType, tc.id, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Locate(%q, %q) = %q, want %q", tc.uriType, tc.id, got, tc.want)
		}
	}
}
