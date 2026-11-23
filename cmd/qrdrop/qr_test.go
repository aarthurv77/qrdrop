package main

import (
	"bytes"
	"strings"
	"testing"
)

// As cores só embrulham cada caractere: tirando os códigos ANSI, o desenho é
// exatamente o mesmo QR sem cores.
func TestPrintQRComCores(t *testing.T) {
	const url = "http://192.168.0.12:8080/?t=0123456789abcdef0123456789abcdef"
	var plain, colored bytes.Buffer
	printQR(&plain, url, false)
	printQR(&colored, url, true)

	if strings.Contains(plain.String(), "\033") {
		t.Error("sem cores, a saída não deveria ter códigos ANSI")
	}
	stripped := strings.NewReplacer(ansiWhiteOnBlack, "", ansiReset, "").Replace(colored.String())
	if stripped != plain.String() {
		t.Error("tirando as cores, o desenho deveria ser igual ao QR sem cores")
	}
	// Nenhum bloco fica "solto", na cor padrão do terminal.
	for _, line := range strings.Split(strings.TrimRight(colored.String(), "\n"), "\n") {
		rest := strings.ReplaceAll(line, ansiWhiteOnBlack, "")
		for _, block := range []string{"█", "▀", "▄", " "} {
			rest = strings.ReplaceAll(rest, block+ansiReset, "")
		}
		if rest != "" {
			t.Fatalf("sobrou algo sem cor na linha: %q", rest)
		}
	}
}
