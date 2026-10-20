//go:build windows

package main

import (
	"os"
	"os/signal"

	"golang.org/x/sys/windows"
)

// echoOff clears the echo bit of the console the answers come from, and hands
// back the function that puts the previous mode back. ok is false when stdin is
// not a console, or when its mode cannot be read or set, and the caller then
// reads the answer with the echo on.
//
// The mode belongs to the console and not to this program, so a run that ended
// with the echo off leaves every command typed into that console invisible.
// Ctrl+C is what this platform lets a program catch, and the mode is put back
// before the run ends. A run killed from outside - Task Manager, taskkill, a
// console window that is closed - executes no code at all, and there the console
// keeps the echo off until it is closed: that part no program can prevent.
func echoOff(stdin *os.File) (restore func(), ok bool) {
	handle := windows.Handle(stdin.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return nil, false
	}
	if err := windows.SetConsoleMode(handle, mode&^windows.ENABLE_ECHO_INPUT); err != nil {
		return nil, false
	}

	// The interrupt is handled here rather than left to the deferred restore:
	// defers run when a function returns, and an interrupt ends the process
	// instead.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	done := make(chan struct{})
	go func() {
		select {
		case <-signals:
			// The mode goes back first, and the interrupt is then left to the
			// default action Windows takes for it, which is to end the process.
			_ = windows.SetConsoleMode(handle, mode)
			signal.Reset(os.Interrupt)
			if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_C_EVENT, 0); err != nil {
				// Nothing left here can end this run the way the signal would,
				// and a console left without its echo is the worse of the two.
				os.Exit(130)
			}
		case <-done:
		}
	}()

	return func() {
		signal.Stop(signals)
		close(done)
		_ = windows.SetConsoleMode(handle, mode)
	}, true
}
