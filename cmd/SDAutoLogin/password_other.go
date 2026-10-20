//go:build !windows

package main

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// echoOff turns the echo off on the terminal the answers come from and returns
// the function that turns it back on. The setting is the terminal's, so a run
// that died with it off would leave the shell blind. ok is false when stdin is
// not a terminal, and the caller then reads with the echo on.
func echoOff(stdin *os.File) (restore func(), ok bool) {
	if err := stty(stdin, "-echo"); err != nil {
		return nil, false
	}

	// A signal that ends the process would leave the terminal blind, so the echo
	// is put back first and the signal is then sent to this process again, which
	// ends the run the way it would have with the echo on. SIGQUIT is part of the
	// list: Ctrl+\ ends a Go program with a stack dump of every goroutine, and a
	// terminal left without its echo is worse than the dump on its own.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-signals:
			_ = stty(stdin, "echo")
			signal.Reset(sig)
			if s, isSignal := sig.(syscall.Signal); isSignal {
				_ = syscall.Kill(syscall.Getpid(), s)
			}
		case <-done:
		}
	}()

	return func() {
		signal.Stop(signals)
		close(done)
		_ = stty(stdin, "echo")
	}, true
}

// stty runs one stty command on the terminal. It is part of coreutils and of
// busybox, so a system that has a shell has it.
func stty(terminal *os.File, arg string) error {
	cmd := exec.Command("stty", arg)
	cmd.Stdin = terminal
	return cmd.Run()
}
