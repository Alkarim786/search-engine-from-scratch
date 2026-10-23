// Package urlnorm canonicalises URLs so the crawler's seen-set treats
// equivalent spellings of one page as one URL.
package urlnorm

import (
	"errors"
	"net/url"
	"path"
	"strings"
)

// Canonical resolves raw against base (which may be nil for absolute input)
// and returns the canonical absolute http(s) URL:
//
//   - scheme and host lower-cased, default port removed
//   - fragment and userinfo removed
//   - dot segments resolved, empty path becomes "/"
//   - query parameters sorted, utm_* tracking parameters dropped
func Canonical(raw string, base *url.URL) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("unsupported scheme: " + u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("missing host")
	}
	u.Host = strings.ToLower(u.Host)
	if h, p, ok := cutPort(u.Host); ok && ((u.Scheme == "http" && p == "80") || (u.Scheme == "https" && p == "443")) {
		u.Host = h
	}
	u.User = nil
	u.Fragment = ""
	u.RawFragment = ""
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	trailing := strings.HasSuffix(p, "/")
	p = path.Clean(p)
	if trailing && p != "/" {
		p += "/"
	}
	if dec, err := url.PathUnescape(p); err == nil {
		u.Path = dec
		u.RawPath = ""
		if p != u.EscapedPath() {
			u.RawPath = p
		}
	}
	if u.RawQuery != "" {
		q := u.Query()
		for k := range q {
			if strings.HasPrefix(strings.ToLower(k), "utm_") {
				q.Del(k)
			}
		}
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

func cutPort(host string) (h, p string, ok bool) {
	i := strings.LastIndexByte(host, ':')
	if i < 0 || strings.Contains(host[i:], "]") {
		return host, "", false
	}
	return host[:i], host[i+1:], true
}
