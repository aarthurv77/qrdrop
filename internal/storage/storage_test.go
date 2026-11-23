package storage

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestSafeName(t *testing.T) {
	// Teste de tabela: o jeito idiomático de testar em Go.
	cases := []struct{ in, want string }{
		{"foto.jpg", "foto.jpg"},
		{"../../etc/passwd", "passwd"},
		{`C:\Users\ana\doc.pdf`, "doc.pdf"},
		{".bashrc", "bashrc"},
		{"", "arquivo"},
		{"..", "arquivo"},
		{"/", "arquivo"},
		{"a:b?.txt", "a_b_.txt"},
		{"  férias 2026.mp4  ", "férias 2026.mp4"},
		{"relatório.pdf...", "relatório.pdf"},
		{"con.txt", "_con.txt"},
		{"NUL", "_NUL"},
		{"com1.log", "_com1.log"},
		{"COM0", "_COM0"},
		{"lpt¹.txt", "_lpt¹.txt"},      // dígito sobrescrito também é reservado
		{"com10.txt", "com10.txt"},     // só de 0 a 9
		{"console.txt", "console.txt"}, // só o nome exato é reservado
		{"linha\nnova.txt", "linha_nova.txt"},
		{"fatura\u202Egpj.exe", "fatura_gpj.exe"},     // apareceria como "faturaexe.jpg"
		{"\u009b31mvermelho.txt", "_31mvermelho.txt"}, // C1: comando de terminal
		{"a\u200Fb.txt", "a_b.txt"},                   // marca de direção invisível
		{"\xff\xfeimg.png", "\uFFFD\uFFFDimg.png"},
	}
	for _, c := range cases {
		if got := SafeName(c.in); got != c.want {
			t.Errorf("SafeName(%q) = %q, queria %q", c.in, got, c.want)
		}
	}
}

func TestSafeNameCortaNomesLongos(t *testing.T) {
	longo := strings.Repeat("á", 300) + ".jpeg" // 600 bytes de "á"
	got := SafeName(longo)
	if len(got) > MaxNameBytes {
		t.Fatalf("nome com %d bytes, limite %d", len(got), MaxNameBytes)
	}
	if !strings.HasSuffix(got, ".jpeg") || !utf8.ValidString(got) {
		t.Errorf("perdeu a extensão ou cortou um caractere ao meio: %q", got)
	}

	// Depois do último ponto vêm 30 bytes: isso não é extensão, é parte do
	// nome, e o corte simplesmente fica com os primeiros 200 bytes.
	estranho := strings.Repeat("a", 250) + "." + strings.Repeat("b", 30)
	if got := SafeName(estranho); got != strings.Repeat("a", MaxNameBytes) {
		t.Errorf("nome com \"extensão\" de 30 bytes virou %q", got)
	}
}

// FuzzSafeName usa o fuzzing nativo do Go: o `go test` roda só as sementes;
// `go test -fuzz=FuzzSafeName ./internal/storage` gera milhões de nomes
// aleatórios procurando um que quebre alguma das propriedades abaixo.
func FuzzSafeName(f *testing.F) {
	for _, seed := range []string{"foto.jpg", "../../x", `a\b\..\c`, "con", " . ", "\x00", strings.Repeat("é", 200), "a\u0085b", "x\u202ey"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := SafeName(in)
		switch {
		case got == "":
			t.Fatal("nome vazio")
		case !utf8.ValidString(got):
			t.Fatalf("UTF-8 inválido: %q", got)
		case len(got) > MaxNameBytes+1: // +1 do "_" dos nomes reservados
			t.Fatalf("nome longo demais (%d bytes)", len(got))
		case strings.ContainsAny(got, `/\<>:"|?*`):
			t.Fatalf("caractere proibido em %q", got)
		case strings.HasPrefix(got, ".") || strings.HasSuffix(got, ".") || strings.HasSuffix(got, " "):
			t.Fatalf("ponto ou espaço na ponta: %q", got)
		case path.Base(got) != got || filepath.Base(got) != got:
			t.Fatalf("ainda parece um caminho: %q", got)
		case isWindowsReserved(got):
			t.Fatalf("nome reservado do Windows: %q", got)
		case SafeName(got) != got:
			t.Fatalf("não é idempotente: %q -> %q", got, SafeName(got))
		}
		for _, r := range got {
			if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
				t.Fatalf("caractere de controle ou de direção em %q", got)
			}
		}
		if r, _ := utf8.DecodeRuneInString(got); unicode.IsSpace(r) {
			t.Fatalf("espaço no começo: %q", got)
		}
	})
}

// Exemplos também são testes: o `go test` confere a saída com o comentário
// "Output", e eles aparecem na documentação gerada pelo `go doc`.
func ExampleSafeName() {
	fmt.Println(SafeName("../../etc/passwd"))
	fmt.Println(SafeName(`C:\Users\ana\foto.jpg`))
	fmt.Println(SafeName("con.txt"))
	// Output:
	// passwd
	// foto.jpg
	// _con.txt
}

func BenchmarkSafeName(b *testing.B) {
	for i := 0; i < b.N; i++ {
		SafeName(`C:\Users\ana\Imagens\férias na praia 2026 (final).jpeg`)
	}
}

func TestCreateNaoSobrescreve(t *testing.T) {
	dir := t.TempDir()
	const n = 20
	var wg sync.WaitGroup
	names := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { // 20 goroutines criando "foto.jpg" ao mesmo tempo
			defer wg.Done()
			f, err := Create(dir, "foto.jpg")
			if err != nil {
				t.Error(err)
				return
			}
			names[i] = filepath.Base(f.Name())
			f.Close()
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			t.Fatalf("nome repetido: %s", name)
		}
		seen[name] = true
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != n {
		t.Fatalf("esperava %d arquivos, achei %d", n, len(entries))
	}
	if !seen["foto.jpg"] || !seen["foto (1).jpg"] {
		t.Errorf("nomes inesperados: %v", names)
	}
}

func TestCreatePastaInexistente(t *testing.T) {
	_, err := Create(filepath.Join(t.TempDir(), "nao-existe"), "a.txt")
	if err == nil {
		t.Fatal("esperava erro ao criar em pasta inexistente")
	}
}
