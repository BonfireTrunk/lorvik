package domain

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

const (
	// MaxPageBytes is the size limit of the HTML page of a card or a note.
	MaxPageBytes = 1 << 20
	// MaxPageTextBytes caps the visible text kept for search: a Postgres
	// tsvector cannot exceed 1 MiB, and the text of a page can be larger
	// than that once it is split into words.
	MaxPageTextBytes = 256 << 10
)

// ValidatePage checks the HTML page of a card or a note. An empty page means
// there is none.
func ValidatePage(page string) error {
	switch {
	case len(page) > MaxPageBytes:
		return invalid("page", fmt.Sprintf("at most %d MiB", MaxPageBytes>>20))
	case !utf8.ValidString(page):
		return invalid("page", "must be valid UTF-8")
	case page != "" && strings.TrimSpace(page) == "":
		return invalid("page", "is only whitespace; send an empty page to have none")
	}
	return nil
}

// skippedElements hold no visible text. Their attributes are not collected
// either.
var skippedElements = map[string]bool{"script": true, "style": true, "template": true, "noscript": true}

// labelAttrs are the attributes whose values a reader sees or hears.
var labelAttrs = []string{"alt", "aria-label", "title"}

// PageText returns the text a reader of the page sees, for full-text search:
// the text nodes outside script, style, template, noscript and head, the text
// of title elements, and the alt, aria-label and title attribute values, with
// whitespace collapsed and cut to MaxPageTextBytes at a rune boundary.
func PageText(page string) string {
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return ""
	}
	var parts []string
	var walk func(n *html.Node, inHead bool)
	walk = func(n *html.Node, inHead bool) {
		switch n.Type {
		case html.TextNode:
			if !inHead {
				parts = append(parts, n.Data)
			}
			return
		case html.ElementNode:
			if skippedElements[n.Data] {
				return
			}
			if n.Data == "title" {
				parts = append(parts, textOf(n))
				return
			}
			inHead = inHead || n.Data == "head"
			if !inHead {
				for _, a := range n.Attr {
					if a.Namespace == "" && slices.Contains(labelAttrs, a.Key) {
						parts = append(parts, a.Val)
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inHead)
		}
	}
	walk(doc, false)
	text := strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
	if len(text) > MaxPageTextBytes {
		cut := MaxPageTextBytes
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = strings.TrimRight(text[:cut], " ")
	}
	return text
}

// textOf joins the text nodes below n.
func textOf(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		} else {
			b.WriteString(" " + textOf(c) + " ")
		}
	}
	return b.String()
}
