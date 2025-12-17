package web

import (
	"net/url"
	"strconv"
	"strings"
)

func withWindow(href string, window int) string {
	if window == 0 || window == 60 {
		return href
	}

	u, err := url.Parse(href)
	if err != nil {
		sep := "?"
		if strings.Contains(href, "?") {
			sep = "&"
		}
		return href + sep + "window=" + strconv.Itoa(window)
	}

	q := u.Query()
	if q.Get("window") == "" {
		q.Set("window", strconv.Itoa(window))
		u.RawQuery = q.Encode()
	}

	return u.String()
}

func pathEscape(s string) string {
	return url.PathEscape(s)
}
