package robots

import (
	"testing"
	"time"
)

const sample = `
# comment
User-agent: *
Disallow: /private/
Disallow: /*.pdf$
Allow: /private/public/
Crawl-delay: 2

User-agent: GoodBot
User-agent: OtherBot
Disallow: /nobots
Crawl-delay: 0.5

User-agent: BadBot
Disallow: /
`

func TestWildcardGroup(t *testing.T) {
	r := Parse(sample, "SearchBot/1.0")
	checks := map[string]bool{
		"/":                      true,
		"/docs/a.html":           true,
		"/private/x":             false,
		"/private/public/x":      true, // longer Allow beats shorter Disallow
		"/files/report.pdf":      false,
		"/files/report.pdf?dl=1": true, // $ anchors the end
		"/files/report.pdfx":     true,
		"/nobots":                true, // belongs to GoodBot's group
	}
	for p, want := range checks {
		if got := r.Allowed(p); got != want {
			t.Errorf("Allowed(%q) = %v, want %v", p, got, want)
		}
	}
	if r.CrawlDelay() != 2*time.Second {
		t.Errorf("delay = %v", r.CrawlDelay())
	}
}

func TestSpecificGroupWinsAndSharesAgents(t *testing.T) {
	for _, ua := range []string{"GoodBot/2", "otherbot"} {
		r := Parse(sample, ua)
		if r.Allowed("/nobots/x") {
			t.Errorf("%s should be blocked from /nobots", ua)
		}
		if !r.Allowed("/private/x") {
			t.Errorf("%s has its own group, * rules must not apply", ua)
		}
		if r.CrawlDelay() != 500*time.Millisecond {
			t.Errorf("%s delay = %v", ua, r.CrawlDelay())
		}
	}
	if Parse(sample, "BadBot").Allowed("/anything") {
		t.Error("BadBot is fully disallowed")
	}
}

func TestEmptyDisallowAndMissingFile(t *testing.T) {
	r := Parse("User-agent: *\nDisallow:\n", "x")
	if !r.Allowed("/anything") {
		t.Error("empty Disallow allows everything")
	}
	if !AllowAll().Allowed("/x") || DisallowAll().Allowed("/x") {
		t.Error("AllowAll/DisallowAll wrong")
	}
	if !Parse("garbage", "x").Allowed("/x") {
		t.Error("no groups -> allow")
	}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"/a", "/abc", true},
		{"/a$", "/abc", false},
		{"/a$", "/a", true},
		{"/*/b", "/x/y/b", true},
		{"/*/b$", "/x/y/b", true},
		{"/*/b$", "/x/y/bc", false},
		{"/fish*.php", "/fishheads/yummy.php", true},
		{"/fish*.php", "/Fish.PHP", false},
	}
	for _, c := range cases {
		if got := match(c.pat, c.path); got != c.want {
			t.Errorf("match(%q,%q) = %v", c.pat, c.path, got)
		}
	}
}
