package main

import (
	"io"

	"github.com/mdp/qrterminal/v3"
)

// ansiWhiteOnBlack fixa texto branco sobre fundo preto; ansiReset volta ao normal.
const (
	ansiWhiteOnBlack = "\033[97;40m"
	ansiReset        = "\033[0m"
)

// printQR desenha o QR code com meio-blocos: cada caractere ("█", "▀", "▄" ou
// espaço) mostra dois módulos do código, um em cima do outro.
//
// O "█" sai na cor do texto do terminal. Num terminal de fundo escuro isso
// desenha módulos claros sobre fundo escuro, e o QR fica certo. Num terminal
// de fundo claro (o padrão do Terminal do macOS) as cores se invertem, e um
// leitor comum não lê o código invertido. Com colors, fixamos branco sobre
// preto com códigos ANSI, e o QR sai igual em qualquer tema.
func printQR(w io.Writer, text string, colors bool) {
	cfg := qrterminal.Config{
		Level:          qrterminal.L,
		Writer:         w,
		HalfBlocks:     true,
		BlackChar:      qrterminal.BLACK_BLACK,
		WhiteBlackChar: qrterminal.WHITE_BLACK,
		WhiteChar:      qrterminal.WHITE_WHITE,
		BlackWhiteChar: qrterminal.BLACK_WHITE,
		QuietZone:      qrterminal.QUIET_ZONE, // margem clara de 4 módulos, como pede o padrão do QR
	}
	if colors {
		paint := func(s string) string { return ansiWhiteOnBlack + s + ansiReset }
		cfg.BlackChar = paint(cfg.BlackChar)
		cfg.WhiteBlackChar = paint(cfg.WhiteBlackChar)
		cfg.WhiteChar = paint(cfg.WhiteChar)
		cfg.BlackWhiteChar = paint(cfg.BlackWhiteChar)
	}
	qrterminal.GenerateWithConfig(text, cfg)
}
