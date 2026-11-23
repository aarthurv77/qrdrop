package share

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollect(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "fotos", "b.jpg"), "bb")
	write(t, filepath.Join(root, "fotos", "A.jpg"), "a")
	write(t, filepath.Join(root, "fotos", ".oculto"), "x")
	write(t, filepath.Join(root, "fotos", "sub", "ignorado.txt"), "x")
	write(t, filepath.Join(root, "outra", "b.jpg"), "outro b")
	write(t, filepath.Join(root, "slides.pdf"), "pdf")

	files, err := Collect([]string{
		filepath.Join(root, "fotos"),
		filepath.Join(root, "outra", "b.jpg"),
		filepath.Join(root, "slides.pdf"),
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []struct {
		name string
		size int64
	}{{"A.jpg", 1}, {"b (2).jpg", 7}, {"b.jpg", 2}, {"slides.pdf", 3}}
	if len(files) != len(want) {
		t.Fatalf("esperava %d arquivos, veio %+v", len(want), files)
	}
	for i, w := range want {
		if files[i].Name != w.name || files[i].Size != w.size {
			t.Errorf("posição %d: %s (%d bytes), queria %s (%d bytes)", i, files[i].Name, files[i].Size, w.name, w.size)
		}
	}
}

// Nomes que só diferem em maiúsculas ("Foto.jpg" e "foto.jpg", de pastas
// diferentes) saem sempre na mesma ordem, qualquer que seja a ordem do -share.
func TestCollectOrdemFixa(t *testing.T) {
	root := t.TempDir()
	x, y := filepath.Join(root, "x"), filepath.Join(root, "y")
	write(t, filepath.Join(x, "Foto.jpg"), "1")
	write(t, filepath.Join(y, "foto.jpg"), "2")

	for _, paths := range [][]string{{x, y}, {y, x}} {
		files, err := Collect(paths)
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 2 || files[0].Name != "Foto.jpg" || files[1].Name != "foto.jpg" {
			t.Errorf("Collect(%v) = %+v, queria Foto.jpg e depois foto.jpg", paths, files)
		}
	}
}

func TestCollectCaminhoInexistente(t *testing.T) {
	if _, err := Collect([]string{filepath.Join(t.TempDir(), "nao-existe")}); err == nil {
		t.Fatal("esperava erro para caminho inexistente")
	}
}
