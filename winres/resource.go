// Package winres holds the Windows resources of the program: the icon, the version
// information and the application manifest that winres.json describes, compiled
// into rsrc_windows_amd64.syso next to this file.
//
// The object file is kept in its own package so that the program's own directory
// holds sources only. The Go linker picks up the .syso files of every package it
// links, so the blank import in the main package is what brings the resources into
// the executable.
package winres
