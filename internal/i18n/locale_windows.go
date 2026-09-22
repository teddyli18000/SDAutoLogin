//go:build windows

package i18n

import "syscall"

// The lookup is declared here rather than shared with the tray, so this package
// keeps no dependencies. One conversion call does not justify a shared package.
var procGetUserDefaultUILanguage = syscall.NewLazyDLL("kernel32.dll").
	NewProc("GetUserDefaultUILanguage")

// detectSystemLang reads the user interface language Windows is configured
// with, not the locale of the current process.
func detectSystemLang() Lang {
	langid, _, _ := procGetUserDefaultUILanguage.Call()
	switch uint16(langid) & 0x3ff {
	case 0x04: // LANG_CHINESE
		return LangZH
	default:
		return LangEN
	}
}
