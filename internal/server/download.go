package server

import (
	"fmt"
	"net/http"
	"os"
	"strings"
)

// handleDownload serve um arquivo compartilhado com -share (PC -> celular).
//
// Só serve o que está no catálogo, buscando pelo nome exato: um pedido como
// "/download/..%2F..%2Fsenhas.txt" simplesmente não acha nada (404).
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	f, ok := s.shared[r.PathValue("name")] // r.PathValue: o {name} da rota, já decodificado
	if !ok {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(f.Path)
	if err != nil {
		http.Error(w, "arquivo indisponível no computador", http.StatusGone)
		return
	}
	defer file.Close()
	modTime := f.ModTime
	if info, err := file.Stat(); err == nil {
		// O arquivo pode ter mudado depois que o qrdrop abriu: com a data
		// antiga, o navegador ouviria "não mudou" (304) e ficaria com a cópia velha.
		modTime = info.ModTime()
	}

	w.Header().Set("Content-Disposition", contentDisposition(f.Name))
	// ServeContent aceita qualquer io.ReadSeeker (*os.File é um) e cuida de
	// Range, que permite retomar um download interrompido, e de Content-Type.
	http.ServeContent(w, r, f.Name, modTime, file)
}

// contentDisposition diz ao navegador para baixar o arquivo com o nome certo.
// Vão duas versões do nome: filename, só com ASCII, para navegadores antigos,
// e filename* (RFC 6266/5987), com o nome real em UTF-8 ("apresentação.pdf").
func contentDisposition(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	var enc strings.Builder
	for i := 0; i < len(name); i++ { // byte a byte: cada byte de UTF-8 vira %XX
		c := name[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("!#$&+-.^_`|~", c) >= 0 {
			enc.WriteByte(c)
		} else {
			fmt.Fprintf(&enc, "%%%02X", c)
		}
	}
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii, enc.String())
}
