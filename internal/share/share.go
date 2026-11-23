// Pacote share monta a lista de arquivos que o PC oferece para download
// (sentido inverso: do computador para o celular).
package share

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// File é um arquivo oferecido para download.
type File struct {
	Name    string    `json:"name"` // nome único, mostrado na página e usado na URL
	Size    int64     `json:"size"`
	Path    string    `json:"-"` // caminho no disco: nunca vai para o cliente
	ModTime time.Time `json:"-"`
}

// Collect monta o catálogo a partir dos caminhos passados em -share. Uma
// pasta vale pelos arquivos que estão direto nela (sem subpastas nem
// arquivos ocultos). Nomes repetidos ganham sufixo: "foto.jpg", "foto (2).jpg".
func Collect(paths []string) ([]File, error) {
	var files []File
	used := map[string]bool{}
	add := func(path string, info os.FileInfo) {
		name := info.Name()
		ext := filepath.Ext(name)
		for i := 2; used[name]; i++ {
			name = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(info.Name(), ext), i, ext)
		}
		used[name] = true
		files = append(files, File{Name: name, Size: info.Size(), Path: path, ModTime: info.ModTime()})
	}

	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("compartilhando %s: %w", p, err)
		}
		if !info.IsDir() {
			add(p, info)
			continue
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil, fmt.Errorf("lendo a pasta %s: %w", p, err)
		}
		for _, e := range entries {
			if !e.Type().IsRegular() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue // o arquivo sumiu entre o ReadDir e o Info: ignoramos
			}
			add(filepath.Join(p, e.Name()), info)
		}
	}

	// sort.Slice recebe uma função de comparação: funções como valores.
	sort.Slice(files, func(i, j int) bool {
		a, b := strings.ToLower(files[i].Name), strings.ToLower(files[j].Name)
		if a != b {
			return a < b
		}
		return files[i].Name < files[j].Name // "Foto.jpg" e "foto.jpg": ordem fixa
	})
	return files, nil
}
