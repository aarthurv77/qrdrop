package server

// Testes escritos com "outro olhar": em vez de usar o servidor como a página
// e o qrdrop send usam, fazemos o que um cliente esquisito (ou mal-
// intencionado) faria: hash em maiúsculas, formulário fora de ordem, conexão
// derrubada no meio. Cada um destes testes falhava antes da correção que
// veio junto com ele.

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"qrdrop/internal/share"
)

// O Get-FileHash do PowerShell escreve o SHA-256 em maiúsculas.
func TestXSHA256EmMaiusculasVale(t *testing.T) {
	e := newTestServer(t, nil)
	data := []byte("planilha")
	resp, body := upload(t, e, "notas.csv", data, map[string]string{"X-SHA256": strings.ToUpper(sum(data))})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
}

// countReader conta quantos bytes alguém leu dele.
type countReader struct {
	r io.Reader
	n int
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// Hash malformado é recusado antes de ler o corpo: não faz sentido receber
// o arquivo inteiro só para responder "não confere" no final.
func TestXSHA256MalformadoRecusaSemLerOCorpo(t *testing.T) {
	e := newTestServer(t, nil)
	// "abc": não é hexadecimal válido; "abcd": é, mas não tem 64 caracteres.
	for i, bad := range []string{"abc", "abcd", strings.Repeat("g", 64)} {
		rec, read := uploadDireto(e, map[string]string{"X-SHA256": bad})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("X-SHA256 %q: status %d, queria 400: %s", bad, rec.Code, rec.Body)
		}
		if read != 0 {
			t.Errorf("X-SHA256 %q: o servidor leu %d bytes do corpo antes de recusar", bad, read)
		}
		if st := e.srv.Stats(); st.Failed != int64(i+1) {
			t.Errorf("a recusa deveria contar como falha: %+v", st)
		}
	}
}

// uploadDireto chama o handler sem rede (httptest.NewRecorder), com um corpo
// de 1 MB que conta quanto o servidor leu dele.
func uploadDireto(e *testEnv, headers map[string]string) (*httptest.ResponseRecorder, int) {
	body := &countReader{r: bytes.NewReader(make([]byte, 1<<20))}
	req := httptest.NewRequest(http.MethodPost, "/upload?t="+token, body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	return rec, body.n
}

// Com X-File-Size acima do limite, a recusa vem antes de ler o corpo. (Sem
// essa checagem o resultado seria o mesmo 413, mas só depois de receber o
// limite inteiro: por isso o teste conta os bytes lidos.)
func TestLimitePeloCabecalhoNaoLeOCorpo(t *testing.T) {
	e := newTestServer(t, func(c *Config) { c.MaxSize = 1000 })
	rec, read := uploadDireto(e, map[string]string{"X-File-Size": "2000"})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d, queria 413", rec.Code)
	}
	if read != 0 {
		t.Errorf("o servidor leu %d bytes do corpo antes de recusar", read)
	}
}

// O progresso vai para o hub no máximo a cada 100 ms. Sem esse freio, um
// arquivo de 8 MB geraria centenas de eventos (um a cada 32 KB), e cada um
// vira uma mensagem para cada celular conectado.
func TestProgressoTemFreio(t *testing.T) {
	e := newTestServer(t, nil)
	events := e.hub.Subscribe()
	defer e.hub.Unsubscribe(events)
	counted := make(chan int)
	go func() {
		n := 0
		for ev := range events {
			if ev.ID != "grande" {
				continue
			}
			if ev.Done {
				counted <- n
				return
			}
			n++
		}
	}()

	start := time.Now()
	resp, _ := upload(t, e, "grande.bin", make([]byte, 8<<20), map[string]string{"X-Upload-ID": "grande"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	n := <-counted
	// Um evento logo no começo e depois no máximo um a cada 100 ms (+2 de folga).
	if limit := int(time.Since(start)/(100*time.Millisecond)) + 3; n > limit {
		t.Errorf("%d eventos de progresso em %s; esperava no máximo %d", n, time.Since(start).Round(time.Millisecond), limit)
	}
}

// curl -F descricao=oi -F file=@real.txt: o campo de texto vem antes do
// arquivo. Antes, o servidor salvava o campo ("oi") como se fosse o arquivo.
func TestCampoDeTextoAntesDoArquivo(t *testing.T) {
	e := newTestServer(t, nil)
	send := func(fields map[string]string, fileName string, data []byte) (*http.Response, []byte) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for k, v := range fields {
			mw.WriteField(k, v)
		}
		if fileName != "" {
			part, _ := mw.CreateFormFile("file", fileName)
			part.Write(data)
		}
		mw.Close()
		req, _ := http.NewRequest(http.MethodPost, e.url("/upload"), &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		return do(t, req)
	}

	data := []byte("o arquivo de verdade")
	if resp, body := send(map[string]string{"descricao": "oi"}, "real.txt", data); resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if got, err := os.ReadFile(filepath.Join(e.dir, "real.txt")); err != nil || !bytes.Equal(got, data) {
		t.Errorf("real.txt = %q, %v; queria %q", got, err, data)
	}

	// Formulário só com campos de texto: não há o que salvar.
	if resp, _ := send(map[string]string{"descricao": "oi"}, "", nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("sem arquivo: status %d, queria 400", resp.StatusCode)
	}
	if names := files(t, e.dir); len(names) != 1 {
		t.Errorf("esperava só real.txt na pasta, veio %v", names)
	}
}

func TestCabecalhosDeSeguranca(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pagina.html"), []byte("<script>alert(1)</script>"), 0o644)
	catalog, _ := share.Collect([]string{dir})
	e := newTestServer(t, func(c *Config) { c.Shared = catalog })

	for _, path := range []string{"/", "/info", "/download/pagina.html"} {
		resp, _ := do(t, mustGet(t, e.url(path)))
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("GET %s: X-Content-Type-Options = %q", path, got)
		}
		if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("GET %s: Referrer-Policy = %q", path, got)
		}
	}
}

// Wait só volta depois que um upload derrubado no meio apagou o que tinha
// gravado. É o que o main usa no Ctrl+C para não deixar arquivo pela metade.
func TestWaitEsperaALimpezaDoUploadInterrompido(t *testing.T) {
	e := newTestServer(t, nil)
	events := e.hub.Subscribe()
	defer e.hub.Unsubscribe(events)

	// Um envio que fica parado no meio: o corpo vem de um pipe que nunca termina.
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })
	mw := multipart.NewWriter(pw)
	go func() {
		part, _ := mw.CreateFormFile("file", "parado.bin")
		part.Write(make([]byte, 64<<10))
	}()
	req, _ := http.NewRequest(http.MethodPost, e.url("/upload"), pr)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	go func() {
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
		}
	}()

	deadline := time.Now().Add(3 * time.Second)
	for len(files(t, e.dir)) == 0 { // espera o arquivo aparecer: a gravação começou
		if time.Now().After(deadline) {
			t.Fatal("o upload não começou")
		}
		time.Sleep(5 * time.Millisecond)
	}

	e.ts.CloseClientConnections() // como o http.Server.Close do main depois do prazo
	e.srv.Wait()

	if names := files(t, e.dir); len(names) != 0 {
		t.Errorf("Wait voltou com arquivo pela metade na pasta: %v", names)
	}
	if st := e.srv.Stats(); st.Failed != 1 || st.Active != 0 {
		t.Errorf("estatísticas erradas: %+v", st)
	}
	// O terminal mostra uma mensagem que se entende, e não o erro de rede cru
	// ("read tcp 127.0.0.1:...: use of closed network connection").
	for ev := range events {
		if ev.Done {
			if ev.Err != errInterrupted.Error() {
				t.Errorf("mensagem do erro: %q", ev.Err)
			}
			break
		}
	}
}

// Se o arquivo compartilhado muda depois que o qrdrop abriu, o download usa
// a data nova. Com a data antiga, o navegador ouviria 304 ("não mudou").
func TestDownloadDepoisDeMudarOArquivo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lista.txt")
	os.WriteFile(path, []byte("versão 1"), 0o644)
	catalog, _ := share.Collect([]string{dir})
	e := newTestServer(t, func(c *Config) { c.Shared = catalog })

	old := catalog[0].ModTime
	os.WriteFile(path, []byte("versão 2"), 0o644)
	later := old.Add(time.Hour)
	os.Chtimes(path, later, later)

	req := mustGet(t, e.url("/download/lista.txt"))
	req.Header.Set("If-Modified-Since", old.UTC().Format(http.TimeFormat))
	resp, body := do(t, req)
	if resp.StatusCode != http.StatusOK || string(body) != "versão 2" {
		t.Errorf("status %d, corpo %q; queria 200 com a versão 2", resp.StatusCode, body)
	}
}

// Páginas que abrem e fecham o stream de eventos (celular que bloqueia a
// tela, aba fechada) não deixam goroutines para trás no servidor.
func TestSSENaoVazaGoroutines(t *testing.T) {
	e := newTestServer(t, nil)
	before := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.url("/events"), nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		bufio.NewReader(resp.Body).ReadString('\n') // ": conectado": o handler já está no select
		cancel()                                    // a página foi embora
		resp.Body.Close()
	}

	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > before+2 { // +2: folga para goroutines do próprio teste
		if time.Now().After(deadline) {
			t.Fatalf("goroutines: %d antes, %d depois de fechar 20 streams", before, runtime.NumGoroutine())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
