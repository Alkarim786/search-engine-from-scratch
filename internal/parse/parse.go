// Package parse extracts the title, visible text and outgoing links from HTML.
package parse

import (
	"io"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// Page is the extracted content of one HTML document.
type Page struct {
	Title    string
	Text     string
	Links    []string // absolute URLs, in document order, not yet canonicalised
	NoIndex  bool     // <meta name=robots content=noindex>
	NoFollow bool     // <meta name=robots content=nofollow>
}

var skipText = map[string]bool{"script": true, "style": true, "noscript": true, "template": true, "svg": true, "head": true}

var blockTags = map[string]bool{
	"p": true, "div": true, "br": true, "li": true, "ul": true, "ol": true, "tr": true, "td": true, "th": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "pre": true, "section": true,
	"article": true, "header": true, "footer": true, "table": true, "dt": true, "dd": true, "blockquote": true,
}

// Parse reads HTML from r. Relative links are resolved against base (or a
// <base href> if present).
func Parse(base *url.URL, r io.Reader) (*Page, error) {
	z := html.NewTokenizer(r)
	p := &Page{}
	var text strings.Builder
	var title strings.Builder
	skipDepth := map[string]int{}
	inTitle := false
	skipping := func() bool {
		for _, n := range skipDepth {
			if n > 0 {
				return true
			}
		}
		return false
	}
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if err := z.Err(); err != io.EOF {
				return nil, err
			}
			p.Title = strings.Join(strings.Fields(title.String()), " ")
			p.Text = strings.Join(strings.Fields(text.String()), " ")
			return p, nil
		case html.StartTagToken, html.SelfClosingTagToken:
			tn, hasAttr := z.TagName()
			name := string(tn)
			var attrs map[string]string
			if hasAttr {
				attrs = map[string]string{}
				for {
					k, v, more := z.TagAttr()
					attrs[string(k)] = string(v)
					if !more {
						break
					}
				}
			}
			switch name {
			case "title":
				if tt == html.StartTagToken {
					inTitle = true
				}
			case "base":
				if h := attrs["href"]; h != "" {
					if u, err := url.Parse(h); err == nil {
						base = base.ResolveReference(u)
					}
				}
			case "meta":
				if strings.EqualFold(attrs["name"], "robots") {
					c := strings.ToLower(attrs["content"])
					p.NoIndex = p.NoIndex || strings.Contains(c, "noindex")
					p.NoFollow = p.NoFollow || strings.Contains(c, "nofollow")
				}
			case "a":
				if h := strings.TrimSpace(attrs["href"]); h != "" && !strings.Contains(strings.ToLower(attrs["rel"]), "nofollow") {
					if u, err := url.Parse(h); err == nil {
						p.Links = append(p.Links, base.ResolveReference(u).String())
					}
				}
			}
			if tt == html.StartTagToken && skipText[name] && name != "head" {
				skipDepth[name]++
			}
			if name == "head" && tt == html.StartTagToken {
				skipDepth["head"]++
			}
			if blockTags[name] {
				text.WriteByte(' ')
			}
		case html.EndTagToken:
			tn, _ := z.TagName()
			name := string(tn)
			if name == "title" {
				inTitle = false
			}
			if skipText[name] && skipDepth[name] > 0 {
				skipDepth[name]--
			}
			if blockTags[name] {
				text.WriteByte(' ')
			}
		case html.TextToken:
			t := string(z.Text())
			if inTitle {
				title.WriteString(t)
			} else if !skipping() {
				text.WriteString(t)
			}
		}
	}
}
