package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"qrdrop/internal/progress"
	"qrdrop/internal/share"
)

const token = "segredo"

type testEnv struct {
	srv *Server
	ts  *httptest.Server
	dir string
	hub *progress.Hub
}

// newTestServer sobe um servidor de verdade numa porta local (httptest).
// mutate permite ajustar a Config antes de criar o Server.
func newTestServer(t *testing.T, mutate func(*Config)) *testEnv {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	hub := progress.NewHub()
	go hub.Run(ctx)

	cfg := Config{Dir: t.TempDir(), Token: token, Hub: hub}
	if mutate != nil {
		mutate(&cfg)
	}
	srv := New(cfg)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(srv.CloseEvents) // fecha os SSE antes do ts.Close esperar por eles
	return &testEnv{srv: srv, ts: ts, dir: cfg.Dir, hub: hub}
}

func (e *testEnv) url(path string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return e.ts.URL + path + sep + "t=" + token
}

// uploadReq monta um POST /upload com um arquivo no corpo multipart.
func uploadReq(t *testing.T, url, name string, data []byte, headers map[string]string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", name)
	part.Write(data)
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, url, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Upload-ID", "teste")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func do(t *testing.T, req *http.Request) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

func upload(t *testing.T, e *testEnv, name string, data []byte, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	return do(t, uploadReq(t, e.url("/upload"), name, data, headers))
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func files(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestTokenObrigatorio(t *testing.T) {
	e := newTestServer(t, nil)
	for _, path := range []string{"/", "/?t=errado", "/events", "/info", "/download/x"} {
		resp, err := http.Get(e.ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s: status %d, queria 401", path, resp.StatusCode)
		}
	}
	resp, body := do(t, mustGet(t, e.url("/")))
	if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("qrdrop")) {
		t.Errorf("com token: status %d", resp.StatusCode)
	}
	resp, _ = do(t, mustGet(t, e.ts.URL+"/healthz"))
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/healthz deveria funcionar sem token, veio %d", resp.StatusCode)
	}
}

func mustGet(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestUploadSalvaEConfereHash(t *testing.T) {
	e := newTestServer(t, nil)
	data := bytes.Repeat([]byte("qrdrop "), 100_000) // ~700 KB
	resp, body := upload(t, e, "../fora.txt", data, map[string]string{"X-SHA256": sum(data)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var res UploadResult
	json.Unmarshal(body, &res)
	if res.Name != "fora.txt" || res.SHA256 != sum(data) || res.Size != int64(len(data)) {
		t.Errorf("resposta inesperada: %+v", res)
	}
	got, err := os.ReadFile(filepath.Join(e.dir, "fora.txt"))
	if err != nil || !bytes.Equal(got, data) {
		t.Errorf("arquivo salvo diferente do enviado (err=%v)", err)
	}
}

func TestUploadHashErradoApagaArquivo(t *testing.T) {
	e := newTestServer(t, nil)
	resp, _ := upload(t, e, "a.txt", []byte("conteudo"), map[string]string{"X-SHA256": strings.Repeat("0", 64)})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, queria 422", resp.StatusCode)
	}
	if names := files(t, e.dir); len(names) != 0 {
		t.Errorf("arquivo corrompido deveria ter sido apagado, sobrou %v", names)
	}
}

func TestUploadSemArquivo(t *testing.T) {
	e := newTestServer(t, nil)
	req, _ := http.NewRequest(http.MethodPost, e.url("/upload"), strings.NewReader("oi"))
	if resp, _ := do(t, req); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("corpo sem multipart: status %d, queria 400", resp.StatusCode)
	}
}

// Dois envios sem X-Upload-ID (como os do curl) ganham IDs diferentes.
func TestUploadSemIDGanhaUm(t *testing.T) {
	e := newTestServer(t, nil)
	events := e.hub.Subscribe()
	defer e.hub.Unsubscribe(events)

	for _, name := range []string{"a.txt", "b.txt"} {
		if resp, body := upload(t, e, name, []byte(name), map[string]string{"X-Upload-ID": ""}); resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d: %s", name, resp.StatusCode, body)
		}
	}
	ids := map[string]bool{}
	for len(ids) < 2 {
		select {
		case ev := <-events:
			if ev.ID == "" {
				t.Fatalf("evento sem ID: %+v", ev)
			}
			if ev.Done {
				ids[ev.ID] = true
			}
		case <-time.After(time.Second):
			t.Fatalf("esperava 2 eventos finais com IDs diferentes, vieram %v", ids)
		}
	}
}

func TestLimiteDeTamanho(t *testing.T) {
	e := newTestServer(t, func(c *Config) { c.MaxSize = 1000 })

	// 1) O cliente avisa o tamanho: recusado antes de ler o corpo.
	resp, body := upload(t, e, "grande.bin", make([]byte, 2000), map[string]string{"X-File-Size": "2000"})
	if resp.StatusCode != http.StatusRequestEntityTooLarge || !strings.Contains(string(body), "1000 B") {
		t.Errorf("com X-File-Size: status %d (%s), queria 413", resp.StatusCode, body)
	}
	// 2) O cliente não avisa: o servidor corta no byte 1001 e apaga.
	resp, _ = upload(t, e, "grande.bin", make([]byte, 1500), nil)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("sem X-File-Size: status %d, queria 413", resp.StatusCode)
	}
	// 3) Exatamente no limite passa, avisando o tamanho ou não.
	if resp, body := upload(t, e, "certo.bin", make([]byte, 1000), map[string]string{"X-File-Size": "1000"}); resp.StatusCode != http.StatusOK {
		t.Errorf("no limite: status %d (%s), queria 200", resp.StatusCode, body)
	}
	if names := files(t, e.dir); len(names) != 1 || names[0] != "certo.bin" {
		t.Errorf("na pasta deveria estar só certo.bin, está %v", names)
	}
	if st := e.srv.Stats(); st.Received != 1 || st.Failed != 2 || st.Bytes != 1000 {
		t.Errorf("estatísticas erradas: %+v", st)
	}
}

func TestTooLargeErrorComErrorsAs(t *testing.T) {
	err := error(&TooLargeError{Limit: 2 << 30})
	wrapped := errors.Join(errors.New("contexto"), err)
	var tl *TooLargeError
	if !errors.As(wrapped, &tl) || tl.Limit != 2<<30 {
		t.Fatal("errors.As deveria achar o TooLargeError embrulhado")
	}
	if !strings.Contains(err.Error(), "2.0 GB") {
		t.Errorf("mensagem sem o limite: %q", err)
	}
}

// Com MaxConcurrent 2, seis uploads ao mesmo tempo devem passar todos,
// mas nunca mais de dois gravando juntos (o semáforo segura o resto).
func TestLimiteDeGravacoesSimultaneas(t *testing.T) {
	e := newTestServer(t, func(c *Config) { c.MaxConcurrent = 2 })
	var cur, peak atomic.Int64
	e.srv.testHookSaving = func() {
		n := cur.Add(1)
		for { // guarda o maior valor já visto, sem mutex (CompareAndSwap)
			m := peak.Load()
			if n <= m || peak.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond) // segura o slot para os outros esperarem
		cur.Add(-1)
	}

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := uploadReq(t, e.url("/upload"), "f.txt", []byte("x"), nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("status %d", resp.StatusCode)
			}
		}()
	}
	wg.Wait()

	if got := peak.Load(); got > 2 {
		t.Errorf("%d gravações ao mesmo tempo, limite 2", got)
	}
	if n := len(files(t, e.dir)); n != 6 {
		t.Errorf("esperava 6 arquivos, achei %d", n)
	}
}

// Se o celular perde a conexão no meio do envio, o arquivo pela metade é
// apagado e o hub recebe um evento de erro.
func TestDesconexaoApagaArquivoParcial(t *testing.T) {
	e := newTestServer(t, nil)
	events := e.hub.Subscribe()
	defer e.hub.Unsubscribe(events)

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, _ := mw.CreateFormFile("file", "video.mp4")
		part.Write(make([]byte, 256<<10))                        // 256 KB e...
		pw.CloseWithError(errors.New("o celular saiu do Wi-Fi")) // ...a conexão cai
	}()
	req, _ := http.NewRequest(http.MethodPost, e.url("/upload"), pr)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Upload-ID", "caiu")
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.ID == "caiu" && ev.Done {
				if ev.Err == "" {
					t.Fatal("o evento final deveria ter erro")
				}
				if names := files(t, e.dir); len(names) != 0 {
					t.Fatalf("sobrou arquivo pela metade: %v", names)
				}
				if st := e.srv.Stats(); st.Failed != 1 || st.Active != 0 {
					t.Errorf("estatísticas erradas: %+v", st)
				}
				return
			}
		case <-deadline:
			t.Fatal("o servidor não terminou o upload interrompido")
		}
	}
}

func TestEventosSSE(t *testing.T) {
	e := newTestServer(t, nil)
	resp, err := http.Get(e.url("/events"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := bufio.NewScanner(resp.Body)
	lines.Scan() // ": conectado"

	upload(t, e, "b.txt", []byte("oi"), nil)

	found := make(chan progress.Event, 1)
	go func() {
		for lines.Scan() {
			if data, ok := strings.CutPrefix(lines.Text(), "data: "); ok {
				var ev progress.Event
				json.Unmarshal([]byte(data), &ev)
				if ev.Done {
					found <- ev
					return
				}
			}
		}
	}()
	select {
	case ev := <-found:
		if ev.ID != "teste" || ev.Name != "b.txt" || ev.Bytes != 2 || ev.SHA256 != sum([]byte("oi")) {
			t.Errorf("evento inesperado: %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nenhum evento de conclusão chegou pelo SSE")
	}
}

func TestSSEPingECloseEvents(t *testing.T) {
	old := heartbeat
	heartbeat = 20 * time.Millisecond
	defer func() { heartbeat = old }()

	e := newTestServer(t, nil)
	resp, err := http.Get(e.url("/events"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := bufio.NewScanner(resp.Body)

	gotPing := make(chan struct{})
	ended := make(chan struct{})
	go func() {
		for lines.Scan() {
			if lines.Text() == ": ping" {
				select {
				case <-gotPing:
				default:
					close(gotPing)
				}
			}
		}
		close(ended) // o servidor fechou o stream
	}()

	select {
	case <-gotPing:
	case <-time.After(2 * time.Second):
		t.Fatal("nenhum ping chegou")
	}
	e.srv.CloseEvents()
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("CloseEvents não encerrou o stream")
	}
}

func TestInfo(t *testing.T) {
	shared := filepath.Join(t.TempDir(), "slides.pdf")
	os.WriteFile(shared, []byte("%PDF"), 0o644)
	catalog, err := share.Collect([]string{shared})
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Date(2026, 11, 26, 15, 30, 0, 0, time.UTC)
	e := newTestServer(t, func(c *Config) {
		c.MaxSize = 5 << 20
		c.Shared = catalog
		c.ExpiresAt = expires
	})
	upload(t, e, "a.txt", []byte("12345"), nil)

	resp, body := do(t, mustGet(t, e.url("/info")))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var info Info
	if err := json.Unmarshal(body, &info); err != nil {
		t.Fatal(err)
	}
	if info.MaxSize != 5<<20 || info.ExpiresAt == nil || !info.ExpiresAt.Equal(expires) {
		t.Errorf("info inesperada: %s", body)
	}
	if len(info.Shared) != 1 || info.Shared[0].Name != "slides.pdf" || info.Shared[0].Size != 4 {
		t.Errorf("lista de compartilhados errada: %s", body)
	}
	if strings.Contains(string(body), shared) {
		t.Error("o caminho no disco não pode aparecer para o celular")
	}
	if info.Stats.Received != 1 || info.Stats.Bytes != 5 {
		t.Errorf("estatísticas erradas: %+v", info.Stats)
	}
}

func TestInfoSemCompartilhadosDevolveListaVazia(t *testing.T) {
	e := newTestServer(t, nil)
	_, body := do(t, mustGet(t, e.url("/info")))
	if !strings.Contains(string(body), `"shared":[]`) || strings.Contains(string(body), "expiresAt") {
		t.Errorf("info inesperada: %s", body)
	}
}

func TestDownload(t *testing.T) {
	dir := t.TempDir()
	content := []byte("0123456789abcdef")
	os.WriteFile(filepath.Join(dir, "férias.txt"), content, 0o644)
	os.WriteFile(filepath.Join(t.TempDir(), "senhas.txt"), []byte("segredo"), 0o644)
	catalog, err := share.Collect([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	e := newTestServer(t, func(c *Config) { c.Shared = catalog })

	resp, body := do(t, mustGet(t, e.url("/download/f%C3%A9rias.txt")))
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, content) {
		t.Fatalf("download: status %d, corpo %q", resp.StatusCode, body)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != `attachment; filename="f_rias.txt"; filename*=UTF-8''f%C3%A9rias.txt` {
		t.Errorf("Content-Disposition inesperado: %q", cd)
	}

	// Range: retomar um download a partir do byte 10.
	req := mustGet(t, e.url("/download/f%C3%A9rias.txt"))
	req.Header.Set("Range", "bytes=10-")
	resp, body = do(t, req)
	if resp.StatusCode != http.StatusPartialContent || string(body) != "abcdef" {
		t.Errorf("Range: status %d, corpo %q", resp.StatusCode, body)
	}

	// Nada fora do catálogo, nem tentando escapar da pasta.
	for _, path := range []string{"/download/senhas.txt", "/download/..%2Fsenhas.txt", "/download/..%2F..%2Fetc%2Fpasswd"} {
		if resp, _ := do(t, mustGet(t, e.url(path))); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, queria 404", path, resp.StatusCode)
		}
	}
}

func TestContentDisposition(t *testing.T) {
	cases := map[string]string{
		"nota.txt":            `attachment; filename="nota.txt"; filename*=UTF-8''nota.txt`,
		`a"b;c.pdf`:           `attachment; filename="a_b;c.pdf"; filename*=UTF-8''a%22b%3Bc.pdf`,
		"relatório final.pdf": `attachment; filename="relat_rio final.pdf"; filename*=UTF-8''relat%C3%B3rio%20final.pdf`,
	}
	for in, want := range cases {
		if got := contentDisposition(in); got != want {
			t.Errorf("contentDisposition(%q)\n got  %s\n want %s", in, got, want)
		}
	}
}

// BenchmarkUpload mede a vazão do servidor: `go test -bench=Upload ./internal/server`.
// b.SetBytes faz o Go mostrar o resultado em MB/s.
func BenchmarkUpload(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := progress.NewHub()
	go hub.Run(ctx)
	srv := New(Config{Dir: b.TempDir(), Token: token, Hub: hub})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	data := make([]byte, 8<<20)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", "bench.bin")
	part.Write(data)
	mw.Close()
	payload := body.Bytes()

	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/upload?t="+token, bytes.NewReader(payload))
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b.Fatalf("status %d", resp.StatusCode)
		}
	}
}
