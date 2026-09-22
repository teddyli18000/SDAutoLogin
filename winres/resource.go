// Package winres holds the Windows resources of the program: the icon, the
// version information and the application manifest that winres.json describes,
// compiled into rsrc_windows_amd64.syso next to this file.
//
// The object file is kept in its own package so that the program's own
// directory holds sources only. It is linked because this package is part of
// the build, and the blank import in the main package is there for that: the Go
// linker picks up the .syso files of every package it links, so the resources
// reach the executable without sitting in the package that produces it.
package winres
