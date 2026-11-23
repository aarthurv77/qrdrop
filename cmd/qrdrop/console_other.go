//go:build !windows

package main

import "os"

// waitIfOwnConsole só faz algo no Windows (veja console_windows.go): no Linux
// e no macOS o programa roda num terminal que continua aberto depois.
func waitIfOwnConsole() {}

// enableANSI: os terminais de Linux e macOS entendem cores ANSI. Só não vale
// mandar códigos de cor quando a saída foi redirecionada para um arquivo ou
// um pipe (aí ela não é um "dispositivo de caracteres").
func enableANSI() bool {
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
