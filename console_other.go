//go:build !windows

package main

// attachConsole does nothing here: the program is built as a console program on
// these platforms, so its output already goes to the terminal that started it.
func attachConsole() {}
