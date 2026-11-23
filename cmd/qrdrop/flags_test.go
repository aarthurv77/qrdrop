package main

import (
	"flag"
	"testing"
)

func TestByteSize(t *testing.T) {
	ok := map[string]int64{
		"0":      0,
		"1000":   1000,
		"500MB":  500 << 20,
		"2GB":    2 << 30,
		"2g":     2 << 30,
		"1.5G":   3 << 29,
		"64KiB":  64 << 10,
		" 10 MB": 10 << 20,
	}
	for in, want := range ok {
		var b byteSize
		if err := b.Set(in); err != nil || int64(b) != want {
			t.Errorf("Set(%q) = %d, %v; queria %d", in, b, err, want)
		}
	}
	// 8388608T = 2^63 bytes: um a mais que o maior int64.
	for _, in := range []string{"", "abc", "-5MB", "10XB", "NaN", "Inf", "99999999T", "8388608T"} {
		var b byteSize
		if err := b.Set(in); err == nil {
			t.Errorf("Set(%q) deveria dar erro, deu %d", in, b)
		}
	}
}

// As flags novas funcionam com o pacote flag de verdade.
func TestFlagsPersonalizadas(t *testing.T) {
	fs := flag.NewFlagSet("teste", flag.ContinueOnError)
	var size byteSize
	var shared stringList
	fs.Var(&size, "max-size", "")
	fs.Var(&shared, "share", "")
	if err := fs.Parse([]string{"-max-size", "2GB", "-share", "fotos", "-share", "slides.pdf"}); err != nil {
		t.Fatal(err)
	}
	if size != 2<<30 || len(shared) != 2 || shared[0] != "fotos" || shared[1] != "slides.pdf" {
		t.Errorf("size=%d shared=%v", size, shared)
	}
	if size.String() != "2.0 GB" || shared.String() != "fotos, slides.pdf" {
		t.Errorf("String(): %q, %q", size.String(), shared.String())
	}
}
