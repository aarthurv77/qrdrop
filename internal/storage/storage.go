// Pacote storage grava os arquivos recebidos em disco com nomes seguros.
package storage

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxNameBytes limita o tamanho do nome salvo. A maioria dos sistemas de
// arquivos aceita no máximo 255 bytes por nome; deixamos folga para o
// sufixo " (1)" que Create acrescenta quando o nome já existe.
const MaxNameBytes = 200

// windowsReserved são nomes de dispositivos do Windows. Mesmo com extensão
// ("con.txt") eles não viram arquivos comuns, então nunca os usamos. A lista
// segue a documentação da Microsoft ("Naming Files, Paths, and Namespaces"),
// que inclui COM0, LPT0 e os dígitos sobrescritos ¹ ² ³.
var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true,
	"COM0": true, "COM1": true, "COM2": true, "COM3": true, "COM4": true,
	"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"COM¹": true, "COM²": true, "COM³": true,
	"LPT0": true, "LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
	"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
	"LPT¹": true, "LPT²": true, "LPT³": true,
}

// SafeName limpa o nome de arquivo enviado pelo cliente.
//
// O nome vem da rede, então não dá para confiar nele: "../../etc/passwd"
// escaparia da pasta de destino (path traversal). Ficamos só com a última
// parte do caminho, trocamos caracteres proibidos por "_" e evitamos nomes
// que o Windows trata de forma especial. O resultado nunca é vazio, e
// SafeName(SafeName(x)) == SafeName(x).
func SafeName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/") // caminhos do Windows
	name = path.Base(name)
	if name == "/" || name == "." || name == ".." {
		name = ""
	}
	name = strings.Map(func(r rune) rune {
		if unsafeRune(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name) // de quebra, bytes que não são UTF-8 válido viram U+FFFD
	name = trimDotsAndSpaces(name)
	if name == "" {
		return "arquivo"
	}
	name = trimDotsAndSpaces(truncate(name, MaxNameBytes))
	if isWindowsReserved(name) {
		name = "_" + name
	}
	return name
}

// unsafeRune diz se r não pode aparecer num nome salvo:
//   - caracteres de controle: os C0 (\n, \t...), o DEL e também os C1
//     (U+0080 a U+009F), que alguns terminais executam como comandos ao
//     imprimir o nome;
//   - marcas de direção do texto (Bidi_Control): com U+202E,
//     "fatura\u202Egpj.exe" aparece na tela como "faturaexe.jpg".
func unsafeRune(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r)
}

// trimDotsAndSpaces tira pontos e espaços das pontas: no começo, um ponto
// esconderia o arquivo (".bashrc"); no fim, o Windows os apaga sozinho.
func trimDotsAndSpaces(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return r == '.' || unicode.IsSpace(r) })
}

// truncate corta o nome em no máximo limit bytes, mantendo a extensão e sem
// partir um caractere UTF-8 no meio.
func truncate(name string, limit int) string {
	if len(name) <= limit {
		return name
	}
	ext := path.Ext(name)
	if len(ext) > 20 { // "extensão" estranha: tratamos como parte do nome
		ext = ""
	}
	cut := limit - len(ext)
	for cut > 0 && !utf8.RuneStart(name[cut]) {
		cut--
	}
	return name[:cut] + ext
}

func isWindowsReserved(name string) bool {
	stem, _, _ := strings.Cut(name, ".")
	return windowsReserved[strings.ToUpper(strings.TrimSpace(stem))]
}

// Create cria um arquivo novo em dir sem sobrescrever nenhum existente.
// Se "foto.jpg" já existe, tenta "foto (1).jpg", "foto (2).jpg" e assim por diante.
//
// O_EXCL faz a checagem e a criação numa operação só do sistema operacional,
// então duas goroutines recebendo arquivos com o mesmo nome ao mesmo tempo
// nunca ficam com o mesmo arquivo.
func Create(dir, name string) (*os.File, error) {
	name = SafeName(name)
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)

	for i := 0; i < 1000; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", base, i, ext)
		}
		f, err := os.OpenFile(filepath.Join(dir, candidate), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("criando %s: %w", candidate, err)
		}
	}
	return nil, fmt.Errorf("muitos arquivos chamados %q", name)
}
