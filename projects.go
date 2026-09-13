package main

import (
	"path/filepath"
	"sort"
	"strings"
)

func sortProjects(projects []Project, mode string) string {
	switch mode {
	case "recent", "oldest", "name", "name-desc":
	default:
		mode = "recent"
	}
	sort.Slice(projects, func(i, j int) bool {
		a, b := projects[i], projects[j]
		if (mode == "recent" || mode == "oldest") && !a.Updated.Equal(b.Updated) {
			if mode == "oldest" {
				return a.Updated.Before(b.Updated)
			}
			return a.Updated.After(b.Updated)
		}
		// Compare the name shown most prominently, then disambiguate equal names.
		if mode == "name-desc" {
			a, b = b, a
		}
		an, bn := strings.ToLower(filepath.Base(a.Name)), strings.ToLower(filepath.Base(b.Name))
		if an != bn {
			return an < bn
		}
		ap, bp := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if ap != bp {
			return ap < bp
		}
		return a.ID < b.ID
	})
	return mode
}
