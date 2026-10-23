package parse

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
)

const doc = `<!doctype html><html><head><title> My  Page
Title </title><style>body{color:red}</style><meta name="robots" content="NOINDEX, follow">
<script>var hidden = 1;</script></head>
<body><h1>Hello</h1><p>First&nbsp;para &amp; more.</p><div>Second</div>
<script>alert("no")</script><noscript>enable js</noscript>
<a href="/abs">abs</a> <a href="rel.html#x">rel</a> <a href="https://other.org/y">ext</a>
<a href="mailto:x@y.z">mail</a> <a rel="nofollow" href="/skip">nofollow</a> <a>no href</a>
<ul><li>one</li><li>two</li></ul></body></html>`

func TestParse(t *testing.T) {
	base, _ := url.Parse("http://example.com/dir/page.html")
	p, err := Parse(base, strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "My Page Title" {
		t.Errorf("title = %q", p.Title)
	}
	if want := "Hello First para & more. Second abs rel ext mail nofollow no href one two"; p.Text != want {
		t.Errorf("text = %q\nwant   %q", p.Text, want)
	}
	if !p.NoIndex || p.NoFollow {
		t.Errorf("robots meta: noindex=%v nofollow=%v", p.NoIndex, p.NoFollow)
	}
	want := []string{"http://example.com/abs", "http://example.com/dir/rel.html#x", "https://other.org/y", "mailto:x@y.z"}
	if !reflect.DeepEqual(p.Links, want) {
		t.Errorf("links = %v", p.Links)
	}
}

func TestBaseHref(t *testing.T) {
	base, _ := url.Parse("http://example.com/a/b.html")
	p, _ := Parse(base, strings.NewReader(`<base href="/other/"><a href="x.html">x</a>`))
	if len(p.Links) != 1 || p.Links[0] != "http://example.com/other/x.html" {
		t.Errorf("links = %v", p.Links)
	}
}

func TestMalformedHTMLDoesNotPanic(t *testing.T) {
	base, _ := url.Parse("http://example.com/")
	p, err := Parse(base, strings.NewReader(`<div><p>unclosed <a href=/x>link <b>bold</div></p></a><`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Text, "unclosed link bold") || len(p.Links) != 1 {
		t.Errorf("%q %v", p.Text, p.Links)
	}
}
