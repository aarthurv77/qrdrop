package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"qrdrop/internal/progress"
	"qrdrop/internal/server"
)

// Teste de ponta a ponta: um receptor de verdade (httptest) e o Sender
// mandando 10 arquivos com 4 goroutines.
func TestSendAllEmParalelo(t *testing.T) {
	recvDir, sendDir := t.TempDir(), t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := progress.NewHub()
	go hub.Run(ctx)
	ts := httptest.NewServer(server.New(server.Config{Dir: recvDir, Token: "tk", Hub: hub}).Handler())
	defer ts.Close()

	var files []string
	for i := 0; i < 10; i++ {
		p := filepath.Join(sendDir, fmt.Sprintf("arq%d.bin", i))
		os.WriteFile(p, bytes.Repeat([]byte{byte(i)}, 50_000*(i+1)), 0o644)
		files = append(files, p)
	}
	files = append(files, filepath.Join(sendDir, "nao-existe.txt"))

	base, err := ParseURL(ts.URL + "/?t=tk")
	if err != nil {
		t.Fatal(err)
	}
	prog := make(chan Progress, 1024)
	go func() {
		for range prog { // só esvazia o channel
		}
	}()
	s := &Sender{BaseURL: base, Parallel: 4, Progress: prog}
	results := s.SendAll(ctx, files)
	close(prog)

	for i, r := range results[:10] {
		if r.Err != nil {
			t.Errorf("%s: %v", r.File, r.Err)
			continue
		}
		want, _ := os.ReadFile(files[i])
		got, _ := os.ReadFile(filepath.Join(recvDir, r.SavedAs))
		if !bytes.Equal(want, got) {
			t.Errorf("%s chegou diferente", r.File)
		}
	}
	if results[10].Err == nil {
		t.Error("arquivo inexistente deveria dar erro")
	}
}

// Com o token errado o receptor responde 401 sem ler o corpo. SendAll tem que
// devolver o erro e só retornar depois que nenhuma goroutine vai mais mandar
// progresso: fechar o channel logo em seguida não pode causar panic.
func TestSendAllTokenErrado(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := progress.NewHub()
	go hub.Run(ctx)
	ts := httptest.NewServer(server.New(server.Config{Dir: t.TempDir(), Token: "certo", Hub: hub}).Handler())
	defer ts.Close()

	p := filepath.Join(t.TempDir(), "grande.bin")
	os.WriteFile(p, make([]byte, 8<<20), 0o644) // 8 MB

	base, _ := ParseURL(ts.URL + "/?t=errado")
	prog := make(chan Progress)
	go func() {
		for range prog {
		}
	}()
	s := &Sender{BaseURL: base, Parallel: 2, Progress: prog}
	results := s.SendAll(ctx, []string{p, p})
	close(prog) // se alguma goroutine ainda mandasse progresso, daria panic aqui

	for _, r := range results {
		if r.Err == nil || !strings.Contains(r.Err.Error(), "401") {
			t.Errorf("esperava erro 401, veio %v", r.Err)
		}
	}
}

// O pool nunca passa de Parallel envios ao mesmo tempo. O servidor de teste
// conta quantas requisições estão abertas juntas e guarda o máximo.
func TestSendAllRespeitaParallel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := progress.NewHub()
	go hub.Run(ctx)
	inner := server.New(server.Config{Dir: t.TempDir(), Token: "tk", Hub: hub}).Handler()

	var cur, peak atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := cur.Add(1)
		defer cur.Add(-1)
		for { // guarda o maior valor já visto, sem mutex (CompareAndSwap)
			m := peak.Load()
			if n <= m || peak.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond) // garante que os envios se sobreponham
		inner.ServeHTTP(w, r)
	}))
	defer ts.Close()

	dir := t.TempDir()
	var files []string
	for i := 0; i < 9; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%d.txt", i))
		os.WriteFile(p, []byte("conteudo"), 0o644)
		files = append(files, p)
	}
	base, _ := ParseURL(ts.URL + "/?t=tk")
	s := &Sender{BaseURL: base, Parallel: 3}
	for _, r := range s.SendAll(ctx, files) {
		if r.Err != nil {
			t.Fatalf("%s: %v", r.File, r.Err)
		}
	}
	if got := peak.Load(); got != 3 {
		t.Errorf("máximo de %d envios simultâneos, queria exatamente 3", got)
	}
}

func TestSendAllCancelado(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.txt")
	os.WriteFile(p, []byte("x"), 0o644)
	base, _ := ParseURL("127.0.0.1:1/?t=tk") // ninguém escutando: nem deve tentar

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // usuário apertou Ctrl+C antes de começar
	prog := make(chan Progress, 10)
	s := &Sender{BaseURL: base, Parallel: 2, Progress: prog}
	for _, r := range s.SendAll(ctx, []string{p, p, p}) {
		if !errors.Is(r.Err, context.Canceled) {
			t.Errorf("esperava context.Canceled, veio %v", r.Err)
		}
	}
	// Cada arquivo, mesmo cancelado, avisa que terminou: é o que fecha a
	// contagem "3/3 prontos" no terminal.
	close(prog)
	done := 0
	for p := range prog {
		if p.Done {
			done++
		}
	}
	if done != 3 {
		t.Errorf("esperava 3 eventos finais, vieram %d", done)
	}
}

func TestParseURLExigeToken(t *testing.T) {
	if _, err := ParseURL("192.168.0.10:8080"); err == nil {
		t.Error("URL sem token deveria ser recusada")
	}
	if _, err := ParseURL("192.168.0.10:8080/?t=abc"); err != nil {
		t.Error(err)
	}
}

// O send manda o próprio hash no cabeçalho e confere o que o receptor
// devolve. Aqui o receptor é de mentira e responde um hash errado: o send
// tem que acusar, mesmo com status 200.
func TestSendAllConfereOHash(t *testing.T) {
	data := []byte("conteúdo importante")
	p := filepath.Join(t.TempDir(), "doc.txt")
	os.WriteFile(p, data, 0o644)
	sum := sha256.Sum256(data)
	want := hex.EncodeToString(sum[:])

	headers := make(chan string, 1) // channel: o handler roda em outra goroutine
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Get("X-SHA256")
		io.Copy(io.Discard, r.Body)
		fmt.Fprintf(w, `{"name":"doc.txt","sha256":%q}`, strings.Repeat("0", 64))
	}))
	defer ts.Close()

	base, _ := ParseURL(ts.URL + "/?t=tk")
	res := (&Sender{BaseURL: base, Parallel: 1}).SendAll(context.Background(), []string{p})
	if got := <-headers; got != want {
		t.Errorf("X-SHA256 enviado = %q, queria %q", got, want)
	}
	if res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "não confere") {
		t.Errorf("esperava erro de SHA-256, veio %v", res[0].Err)
	}
}

// Parallel 0 (ou negativo) vale como 1 trabalhador: envia em vez de travar.
func TestSendAllComParallelZero(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := progress.NewHub()
	go hub.Run(ctx)
	ts := httptest.NewServer(server.New(server.Config{Dir: t.TempDir(), Token: "tk", Hub: hub}).Handler())
	defer ts.Close()

	dir := t.TempDir()
	files := []string{filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")}
	for _, f := range files {
		os.WriteFile(f, []byte(f), 0o644)
	}
	base, _ := ParseURL(ts.URL + "/?t=tk")
	done := make(chan []Result)
	go func() { done <- (&Sender{BaseURL: base, Parallel: 0}).SendAll(ctx, files) }()
	select {
	case res := <-done:
		for _, r := range res {
			if r.Err != nil {
				t.Errorf("%s: %v", r.File, r.Err)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SendAll travou com Parallel 0")
	}
}
