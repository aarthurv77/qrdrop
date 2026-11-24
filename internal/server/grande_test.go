package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/rand"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// bigSize é maior que 100 MB, como pede a issue do upload. Nada desse
// tamanho chega a existir na memória: o arquivo é gerado aos pedaços
// enquanto é enviado.
const bigSize = 150 << 20

// patternReader gera size bytes repetindo um bloco pseudoaleatório fixo.
// Bloco aleatório, e não zeros, para que um erro de deslocamento (um pedaço
// gravado fora de ordem, por exemplo) mude o SHA-256.
type patternReader struct {
	block []byte
	left  int64
	off   int
}

func newPatternReader(size int64) *patternReader {
	block := make([]byte, 64<<10)
	rand.New(rand.NewSource(1)).Read(block)
	return &patternReader{block: block, left: size}
}

func (p *patternReader) Read(b []byte) (int, error) {
	if p.left == 0 {
		return 0, io.EOF
	}
	if int64(len(b)) > p.left {
		b = b[:p.left]
	}
	n := copy(b, p.block[p.off:])
	p.off = (p.off + n) % len(p.block)
	p.left -= int64(n)
	return n, nil
}

// heapPeak mede, até stop ser chamada, o maior HeapInuse visto.
func heapPeak() (stop func() uint64) {
	var peak atomic.Uint64
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		var m runtime.MemStats
		t := time.NewTicker(20 * time.Millisecond)
		defer t.Stop()
		for {
			runtime.ReadMemStats(&m)
			if m.HeapInuse > peak.Load() {
				peak.Store(m.HeapInuse)
			}
			select {
			case <-done:
				return
			case <-t.C:
			}
		}
	}()
	return func() uint64 {
		close(done)
		<-finished
		return peak.Load()
	}
}

// Um arquivo de 150 MB chega inteiro e íntegro, e o servidor não o carrega
// na memória: a leitura é em streaming (MultipartReader + io.Copy). Se alguém
// trocar por r.ParseMultipartForm ou io.ReadAll, o pico de memória passa de
// 150 MB e o teste falha.
func TestUploadArquivoGrandeEmStreaming(t *testing.T) {
	if testing.Short() {
		t.Skip("envia 150 MB; pulado com -short")
	}
	e := newTestServer(t, nil)

	// O corpo multipart é escrito por uma goroutine numa ponta do io.Pipe e
	// lido pelo cliente HTTP na outra. O hash é calculado no caminho
	// (TeeReader), sem guardar o arquivo.
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	h := sha256.New()
	go func() {
		part, err := mw.CreateFormFile("file", "video.mp4")
		if err == nil {
			_, err = io.Copy(part, io.TeeReader(newPatternReader(bigSize), h))
		}
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err) // nil fecha normalmente
	}()

	req, _ := http.NewRequest(http.MethodPost, e.url("/upload"), pr)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-File-Size", strconv.Itoa(bigSize))

	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	stop := heapPeak()
	resp, body := do(t, req)
	peak := stop()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var res UploadResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("resposta %q: %v", body, err)
	}
	want := hex.EncodeToString(h.Sum(nil))
	if res.Size != bigSize || res.SHA256 != want {
		t.Errorf("resposta %+v; queria %d bytes e SHA-256 %s", res, bigSize, want)
	}
	info, err := os.Stat(filepath.Join(e.dir, "video.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != bigSize {
		t.Errorf("no disco: %d bytes, queria %d", info.Size(), bigSize)
	}

	// Folga de 32 MB para buffers, o detector de corrida e o coletor de lixo.
	// Bem abaixo dos 150 MB que o arquivo ocuparia se fosse lido inteiro.
	const slack = 32 << 20
	if grew := int64(peak) - int64(base.HeapInuse); grew > slack {
		t.Errorf("o heap cresceu %d MB durante o upload: o arquivo não está sendo lido em streaming", grew>>20)
	}
}
