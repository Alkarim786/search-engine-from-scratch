package urlnorm

import (
	"net/url"
	"testing"
)

func TestCanonical(t *testing.T) {
	base, _ := url.Parse("https://Example.com/docs/guide/index.html")
	cases := []struct{ in, want string }{
		{"HTTPS://EXAMPLE.com:443/a/b#frag", "https://example.com/a/b"},
		{"http://example.com:80", "http://example.com/"},
		{"http://example.com:8080/x", "http://example.com:8080/x"},
		{"/a/./b/../c/", "https://example.com/a/c/"},
		{"../intro.html#top", "https://example.com/docs/intro.html"},
		{"?b=2&a=1&utm_source=x", "https://example.com/docs/guide/index.html?a=1&b=2"},
		{"//cdn.example.com/x.js", "https://cdn.example.com/x.js"},
		{"https://user:pw@example.com/p", "https://example.com/p"},
	}
	for _, c := range cases {
		got, err := Canonical(c.in, base)
		if err != nil || got != c.want {
			t.Errorf("Canonical(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

func TestCanonicalRejects(t *testing.T) {
	for _, in := range []string{"", "mailto:a@b.com", "javascript:void(0)", "ftp://x/y", "http:///nohost"} {
		if got, err := Canonical(in, nil); err == nil {
			t.Errorf("Canonical(%q) = %q, want error", in, got)
		}
	}
}

func TestEquivalentSpellingsCollapse(t *testing.T) {
	a, _ := Canonical("http://Example.com/a?x=1&y=2#s", nil)
	b, _ := Canonical("http://example.com:80/a?y=2&x=1", nil)
	if a != b {
		t.Fatalf("%q != %q", a, b)
	}
}
