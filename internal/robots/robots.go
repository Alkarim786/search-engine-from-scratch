// Package robots parses robots.txt following RFC 9309: user-agent groups,
// longest-match Allow/Disallow with * and $ wildcards, and Crawl-delay.
package robots

import (
	"strconv"
	"strings"
	"time"
)

type rule struct {
	allow   bool
	pattern string
}

// Rules is the parsed rule set that applies to one user agent.
type Rules struct {
	rules []rule
	delay time.Duration
}

// AllowAll is the rule set for a missing (404) robots.txt.
func AllowAll() *Rules { return &Rules{} }

// DisallowAll is used when robots.txt could not be fetched reliably (5xx).
func DisallowAll() *Rules { return &Rules{rules: []rule{{false, "/"}}} }

type group struct {
	agents []string
	rules  []rule
	delay  time.Duration
}

// Parse extracts the group that best matches agent (the product token, e.g.
// "SearchEngineBot"); it falls back to the "*" group.
func Parse(body, agent string) *Rules {
	agent = strings.ToLower(agent)
	var groups []*group
	var cur *group
	lastWasAgent := false
	for _, line := range strings.Split(body, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, val := strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch key {
		case "user-agent":
			if !lastWasAgent || cur == nil {
				cur = &group{}
				groups = append(groups, cur)
			}
			cur.agents = append(cur.agents, strings.ToLower(val))
			lastWasAgent = true
			continue
		case "allow", "disallow":
			if cur != nil && (val != "" || key == "allow") {
				cur.rules = append(cur.rules, rule{key == "allow", val})
			}
		case "crawl-delay":
			if cur != nil {
				if f, err := strconv.ParseFloat(val, 64); err == nil && f >= 0 {
					cur.delay = time.Duration(f * float64(time.Second))
				}
			}
		}
		lastWasAgent = false
	}
	var best, star *group
	bestLen := 0
	for _, g := range groups {
		for _, a := range g.agents {
			switch {
			case a == "*":
				if star == nil {
					star = g
				}
			case strings.Contains(agent, a) && len(a) > bestLen:
				best, bestLen = g, len(a)
			}
		}
	}
	if best == nil {
		best = star
	}
	if best == nil {
		return AllowAll()
	}
	return &Rules{rules: best.rules, delay: best.delay}
}

// CrawlDelay returns the Crawl-delay directive (0 if absent).
func (r *Rules) CrawlDelay() time.Duration { return r.delay }

// Allowed reports whether the URL path (including ?query) may be fetched.
// The most specific (longest) matching rule wins; Allow wins ties.
func (r *Rules) Allowed(path string) bool {
	if path == "" {
		path = "/"
	}
	allowed, bestLen := true, -1
	for _, ru := range r.rules {
		if !match(ru.pattern, path) {
			continue
		}
		l := len(ru.pattern)
		if l > bestLen || (l == bestLen && ru.allow) {
			allowed, bestLen = ru.allow, l
		}
	}
	return allowed
}

// match reports whether path starts with pattern, where '*' matches any
// sequence and a trailing '$' anchors the end.
func match(pattern, path string) bool {
	anchored := strings.HasSuffix(pattern, "$")
	if anchored {
		pattern = pattern[:len(pattern)-1]
	}
	parts := strings.Split(pattern, "*")
	if !strings.HasPrefix(path, parts[0]) {
		return false
	}
	pos := len(parts[0])
	for _, p := range parts[1:] {
		i := strings.Index(path[pos:], p)
		if i < 0 {
			return false
		}
		pos += i + len(p)
	}
	if anchored {
		// Last literal part must end the path.
		if len(parts) == 1 {
			return pos == len(path)
		}
		last := parts[len(parts)-1]
		return strings.HasSuffix(path, last)
	}
	return true
}
