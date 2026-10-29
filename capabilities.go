package main

import "strings"

func detectTerminalCapabilities(term, noColor, locale string) (unicode, color bool) {
	term = strings.ToLower(strings.TrimSpace(term))
	locale = strings.ToLower(locale)
	unicode = strings.Contains(locale, "utf-8") || strings.Contains(locale, "utf8")
	color = term != "" && term != "dumb" && noColor == ""
	if term == "dumb" {
		unicode = false
	}
	return unicode, color
}
