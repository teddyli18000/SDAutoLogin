//go:build !windows

package i18n

import (
	"os"
	"strings"
)

// detectSystemLang reads the locale from the usual environment variables.
func detectSystemLang() Lang {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(key); v != "" {
			if strings.HasPrefix(strings.ToLower(v), "zh") {
				return LangZH
			}
			return LangEN
		}
	}
	return LangEN
}
