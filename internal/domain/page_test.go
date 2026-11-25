package domain

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestValidatePage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		page    string
		wantErr bool
	}{
		{"empty means none", "", false},
		{"small page", "<p>hi</p>", false},
		{"exactly the limit", strings.Repeat("a", MaxPageBytes), false},
		{"one byte over", strings.Repeat("a", MaxPageBytes+1), true},
		{"invalid utf-8", "<p>\xff\xfe</p>", true},
		{"only whitespace", " \n\t", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidatePage(tt.page)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidatePage err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				var ve *ValidationError
				if !errors.As(err, &ve) || ve.Field != "page" {
					t.Errorf("err = %v, want a ValidationError on page", err)
				}
			}
		})
	}
}

func TestPageText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		page string
		want string
	}{
		{"plain text and whitespace collapse", "<html><body><h1>Hello</h1>\n\n  <p>big   world</p></body></html>", "Hello big world"},
		{"fragment without html or body", "<p>one</p><p>two</p>", "one two"},
		{"script and style are dropped", "<body>keep<script>var secret = 1</script><style>.x{color:red}</style></body>", "keep"},
		{"template and noscript are dropped", "<body>keep<template><b>hidden</b></template><noscript>no js</noscript></body>", "keep"},
		{"head text dropped but title kept", "<html><head><title>My Title</title><meta name=x content=meta></head><body>body</body></html>", "My Title body"},
		{"alt aria-label and title attributes", `<body><img alt="a chart" src=x><button aria-label="Close dialog" class=c>x</button><span title="tip">t</span></body>`, "a chart Close dialog x tip t"},
		{"other attributes ignored", `<body><a href="http://example.com" data-x="nope">link</a></body>`, "link"},
		{"attributes inside script ignored", `<body><script title="no">1</script>ok</body>`, "ok"},
		{"svg title element", `<body><svg><title>Revenue</title><rect/></svg></body>`, "Revenue"},
		{"empty page", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := PageText(tt.page); got != tt.want {
				t.Errorf("PageText = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPageTextCapCutsAtRuneBoundary(t *testing.T) {
	t.Parallel()
	// Each rune is 3 bytes and the text starts with one ASCII byte, so the
	// cap falls inside a rune.
	page := "<p>a" + strings.Repeat("€", MaxPageTextBytes) + "</p>"
	got := PageText(page)
	if len(got) > MaxPageTextBytes {
		t.Errorf("len = %d, want at most %d", len(got), MaxPageTextBytes)
	}
	if !utf8.ValidString(got) {
		t.Errorf("text cut inside a rune")
	}
	if !strings.HasPrefix(got, "a€") {
		t.Errorf("text = %.10q, want the start of the page kept", got)
	}
}

func TestNoteNeedsBodyOrPage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		body      string
		pageBytes int
		wantErr   bool
	}{
		{"body only", "text", 0, false},
		{"page only", "", 10, false},
		{"blank body and no page", "  \n", 0, true},
		{"both", "text", 10, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			n := Note{Slug: "a-note", Title: "t", Body: tt.body, PageBytes: tt.pageBytes}
			if err := n.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("Validate err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
