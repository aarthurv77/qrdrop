package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"qrdrop/internal/progress"
)

// byteSize é um tamanho em bytes que aceita "500MB", "2GB" ou "1.5G" na
// linha de comando. Ele implementa flag.Value (os métodos String e Set): é
// assim que o pacote flag aceita tipos novos, outra vez por interface
// implícita, sem nenhum "implements".
type byteSize int64

func (b *byteSize) String() string {
	if *b == 0 {
		return "0"
	}
	return progress.HumanBytes(int64(*b))
}

func (b *byteSize) Set(text string) error {
	s := strings.ToUpper(strings.TrimSpace(text))
	s = strings.TrimSuffix(s, "B") // "MB" -> "M"
	s = strings.TrimSuffix(s, "I") // "MiB" -> "M"
	mult := 1.0
	if n := len(s); n > 0 {
		switch s[n-1] {
		case 'K':
			mult = 1 << 10
		case 'M':
			mult = 1 << 20
		case 'G':
			mult = 1 << 30
		case 'T':
			mult = 1 << 40
		}
		if mult > 1 {
			s = s[:n-1]
		}
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	// float64(math.MaxInt64) arredonda para 2^63, que já não cabe num int64:
	// por isso ">=" e não ">".
	if err != nil || v < 0 || math.IsNaN(v) || v*mult >= math.MaxInt64 {
		return fmt.Errorf("tamanho inválido %q: use algo como 500MB ou 2GB", text)
	}
	*b = byteSize(v * mult)
	return nil
}

// stringList junta os valores de uma flag repetida:
// -share fotos -share slides.pdf vira ["fotos", "slides.pdf"].
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ", ") }

func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}
