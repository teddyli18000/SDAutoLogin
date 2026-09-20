//go:build windows

package main

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
