package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"qrdrop/internal/client"
	"qrdrop/internal/progress"
	"qrdrop/internal/server"
)

func TestProgressLine(t *testing.T) {
	ok := client.Progress{Sent: 1, Total: 1}
	cases := []struct {
		name     string
		finished int
		active   map[string]client.Progress
		want     string
	}{
		{"nada em andamento", 2, nil, "2/5 prontos"},
		{"ordem alfabética, sem as pastas", 0, map[string]client.Progress{
			"fotos/b.mp4": {Sent: 50, Total: 100},
			"fotos/a.jpg": {Sent: 1, Total: 3},
		}, "0/5 prontos | a.jpg  33% | b.mp4  50%"},
		{"arquivo vazio conta como 100%", 0, map[string]client.Progress{"vazio.txt": {}}, "0/5 prontos | vazio.txt 100%"},
		{"mais de 3 vira +N", 0, map[string]client.Progress{"a": ok, "b": ok, "c": ok, "d": ok, "e": ok},
			"0/5 prontos | a 100% | b 100% | c 100% | +2"},
	}
	for _, c := range cases {
		if got := progressLine(c.finished, 5, c.active); got != c.want {
			t.Errorf("%s:\n got  %q\n want %q", c.name, got, c.want)
		}
	}

	// Nomes compridos: a linha é cortada em 79 caracteres para caber no terminal.
	long := map[string]client.Progress{}
	for _, n := range []string{"aaaaaaaaaaaaaaaaaaaaaaaa.bin", "bbbbbbbbbbbbbbbbbbbbbbbbb.bin", "cccccccccccccccccccccccc.bin", "d.bin"} {
		long[n] = client.Progress{Sent: 5, Total: 10}
	}
	if n := utf8.RuneCountInString(progressLine(10, 10, long)); n != 79 {
		t.Errorf("linha com %d caracteres, queria 79", n)
	}
}

func TestShort(t *testing.T) {
	cases := map[string]string{
		"foto.jpg":                      "foto.jpg",
		"/home/ana/fotos/foto.jpg":      "foto.jpg",
		`C:\Users\ana\foto.jpg`:         "foto.jpg",
		"123456789012345678":            "123456789012345678", // 18: ainda cabe
		"relatório-financeiro-2026.pdf": "relatório-finan...", // corta em runas, não em bytes
	}
	for in, want := range cases {
		if got := short(in); got != want {
			t.Errorf("short(%q) = %q, queria %q", in, got, want)
		}
	}
}

func TestPrintProgress(t *testing.T) {
	var out bytes.Buffer
	in := make(chan client.Progress)
	done := make(chan struct{})
	go printProgress(&out, in, 2, done)

	in <- client.Progress{File: "a.jpg", Sent: 5, Total: 10}
	in <- client.Progress{File: "a.jpg", Done: true}
	in <- client.Progress{File: "b.jpg", Done: true}
	close(in)
	<-done // depois disto a goroutine não escreve mais: dá para ler out sem corrida

	s := out.String()
	if !strings.HasSuffix(s, "\n") {
		t.Errorf("a saída deveria terminar com quebra de linha: %q", s)
	}
	last := s[strings.LastIndex(s, "\r")+1:] // o que ficou visível no terminal
	if got := strings.TrimRight(last, " \n"); got != "2/2 prontos" {
		t.Errorf("última linha = %q, queria %q", got, "2/2 prontos")
	}
}

func TestPickIP(t *testing.T) {
	ip, err := pickIP("192.168.0.7")
	if err != nil || ip.String() != "192.168.0.7" || len(ip) != net.IPv4len {
		t.Errorf("pickIP(192.168.0.7) = %v, %v", ip, err)
	}
	for _, bad := range []string{"abc", "300.1.1.1", "::1", "192.168.0"} {
		if _, err := pickIP(bad); err == nil {
			t.Errorf("pickIP(%q) deveria dar erro", bad)
		}
	}
}

func TestNewToken(t *testing.T) {
	a, err1 := newToken()
	b, err2 := newToken()
	if err1 != nil || err2 != nil || len(a) != 32 || a == b {
		t.Errorf("tokens %q e %q (%v, %v): queria 32 caracteres hex, diferentes", a, b, err1, err2)
	}
}

// Com a porta padrão ocupada, o qrdrop usa outra; com -port, devolve o erro.
func TestListenComAPortaOcupada(t *testing.T) {
	busy, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port

	ln, err := listen(port, false)
	if err != nil {
		t.Fatalf("sem -port deveria achar outra porta: %v", err)
	}
	if got := ln.Addr().(*net.TCPAddr).Port; got == port {
		t.Errorf("usou a porta ocupada %d", got)
	}
	ln.Close()

	if ln, err := listen(port, true); err == nil {
		ln.Close()
		t.Error("com -port explícito deveria devolver o erro")
	}
}

// Ctrl+C com um envio que não termina no prazo: shutdown derruba a conexão,
// e o arquivo pela metade é apagado antes de shutdown voltar. Antes, o
// qrdrop saía depois de 5 s e deixava o pedaço na pasta de recebidos.
func TestShutdownApagaEnvioQueNaoTerminou(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := progress.NewHub()
	go hub.Run(ctx)
	srv := server.New(server.Config{Dir: dir, Token: "tk", Hub: hub})
	httpSrv := &http.Server{Handler: srv.Handler()}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go httpSrv.Serve(ln)

	// Um envio que para no meio: o corpo vem de um pipe que nunca termina.
	pr, pw := io.Pipe()
	defer pw.Close()
	mw := multipart.NewWriter(pw)
	go func() {
		part, _ := mw.CreateFormFile("file", "lento.bin")
		part.Write(make([]byte, 64<<10))
	}()
	req, _ := http.NewRequest(http.MethodPost, "http://"+ln.Addr().String()+"/upload?t=tk", pr)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	go func() {
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for srv.Stats().Active == 0 { // espera a gravação começar
		if time.Now().After(deadline) {
			t.Fatal("o upload não começou")
		}
		time.Sleep(5 * time.Millisecond)
	}

	stopped := make(chan struct{})
	go func() { shutdown(httpSrv, srv, 100*time.Millisecond); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown travou esperando um envio que nunca termina")
	}

	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("sobrou arquivo pela metade: %s", entries[0].Name())
	}
	if st := srv.Stats(); st.Failed != 1 || st.Active != 0 {
		t.Errorf("estatísticas erradas: %+v", st)
	}
}

// TestPontaAPonta compila o qrdrop de verdade e usa os dois lados como uma
// pessoa usaria no terminal: sobe o receptor, manda arquivos com "qrdrop
// send" e aperta Ctrl+C. Cobre o que os testes de pacote não pegam: as
// flags, a URL impressa, o desligamento e o resumo final.
func TestPontaAPonta(t *testing.T) {
	if testing.Short() {
		t.Skip("compila o programa; pulado com -short")
	}
	if runtime.GOOS == "windows" {
		t.Skip("no Windows não dá para mandar Ctrl+C (os.Interrupt) a outro processo")
	}
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "qrdrop")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	// Receptor numa porta livre (-port 0) e com IP fixo, para não depender da rede.
	dest := filepath.Join(tmp, "recebidos")
	recv := exec.Command(bin, "-port", "0", "-ip", "127.0.0.1", "-dir", dest)
	stdout, err := recv.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := recv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { recv.Process.Kill() }) // só faz algo se o teste parar no meio

	// Uma goroutine lê a saída do receptor e entrega linha por linha.
	lines := make(chan string, 1000)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	url := waitLine(t, lines, func(l string) bool { return strings.HasPrefix(l, "http://127.0.0.1:") })

	// Cinco arquivos, de 0 a 600 KB, três de cada vez.
	var paths []string
	var total int64
	for i := 0; i < 5; i++ {
		data := bytes.Repeat([]byte{byte('a' + i)}, i*150_000)
		p := filepath.Join(tmp, fmt.Sprintf("arquivo%d.bin", i))
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
		total += int64(len(data))
	}
	out, err := exec.Command(bin, append([]string{"send", "-p", "3", url}, paths...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("qrdrop send: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "5 de 5 arquivos enviados") {
		t.Errorf("saída do send:\n%s", out)
	}
	for _, p := range paths {
		want, _ := os.ReadFile(p)
		got, err := os.ReadFile(filepath.Join(dest, filepath.Base(p)))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s não chegou igual (%v)", filepath.Base(p), err)
		}
	}

	// Com o token errado o send falha (código de saída 1) e diz o motivo.
	wrong := strings.Replace(url, "?t=", "?t=errado", 1)
	out, err = exec.Command(bin, "send", wrong, paths[1]).CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || !strings.Contains(string(out), "401") {
		t.Errorf("com token errado: err=%v\n%s", err, out)
	}

	// Ctrl+C: o receptor imprime uma linha OK por arquivo e depois o resumo.
	if err := recv.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	oks := 0
	summary := waitLine(t, lines, func(l string) bool {
		if strings.Contains(l, " OK ") {
			oks++
		}
		return strings.HasPrefix(l, "Resumo:")
	})
	if oks != 5 {
		t.Errorf("o terminal do receptor mostrou %d linhas OK, queria 5", oks)
	}
	if want := fmt.Sprintf("Resumo: 5 arquivo(s) recebido(s), %s. Tchau!", progress.HumanBytes(total)); summary != want {
		t.Errorf("resumo:\n got  %q\n want %q", summary, want)
	}
	for range lines { // StdoutPipe: ler tudo antes de chamar Wait
	}
	if err := recv.Wait(); err != nil {
		t.Errorf("o receptor deveria sair com código 0: %v", err)
	}
}

// waitLine lê linhas até achar uma em que ok devolve true, por até 10 s.
func waitLine(t *testing.T, lines <-chan string, ok func(string) bool) string {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case l, open := <-lines:
			if !open {
				t.Fatal("o receptor fechou a saída antes da linha esperada")
			}
			if ok(l) {
				return l
			}
		case <-timeout:
			t.Fatal("a linha esperada não apareceu em 10 s")
		}
	}
}
