// Comando qrdrop: transfere arquivos na rede local.
//
//	qrdrop [opções]                    recebe (mostra o QR code)
//	qrdrop send [-p 4] URL arquivo...  envia para outro PC
//
// Veja docs/arquitetura.md.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"qrdrop/internal/client"
	"qrdrop/internal/netutil"
	"qrdrop/internal/progress"
	"qrdrop/internal/server"
	"qrdrop/internal/share"
)

// version é trocada na hora do build pelo scripts/build.sh:
// go build -ldflags "-X main.version=v1.0.0"
var version = "dev"

func main() {
	var err error
	if len(os.Args) > 1 && os.Args[1] == "send" {
		err = runSend(os.Args[2:])
	} else {
		err = runReceive(os.Args[1:])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "qrdrop:", err)
	}
	waitIfOwnConsole() // Windows, dois cliques no .exe: deixa ler antes de a janela fechar
	if err != nil {
		os.Exit(1)
	}
}

// ---------- receber ----------

func runReceive(args []string) error {
	fs := flag.NewFlagSet("qrdrop", flag.ExitOnError)
	dir := fs.String("dir", "recebidos", "pasta onde os arquivos recebidos são salvos")
	port := fs.Int("port", 8080, "porta do servidor HTTP (0 = qualquer porta livre)")
	ipFlag := fs.String("ip", "", "IP que vai no QR code (padrão: detecta sozinho)")
	var maxSize byteSize
	fs.Var(&maxSize, "max-size", "tamanho máximo por arquivo, ex.: 500MB, 2GB (0 = sem limite)")
	maxUploads := fs.Int("max-uploads", server.DefaultMaxConcurrent, "quantos arquivos podem estar gravando ao mesmo tempo")
	var shared stringList
	fs.Var(&shared, "share", "arquivo ou pasta que o celular pode baixar (pode repetir)")
	ttl := fs.Duration("ttl", 0, "validade do link, ex.: 30m, 2h (0 = até apertar Ctrl+C)")
	showVersion := fs.Bool("version", false, "mostra a versão e sai")
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintln(out, "uso:")
		fmt.Fprintln(out, "  qrdrop [opções]                    recebe arquivos (mostra o QR code)")
		fmt.Fprintln(out, "  qrdrop send [-p 4] URL arquivo...  envia arquivos para outro PC")
		fmt.Fprintln(out, "\nopções para receber:")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if *showVersion {
		fmt.Println("qrdrop", version)
		return nil
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("argumento inesperado %q (para enviar, use: qrdrop send URL arquivo...)", fs.Arg(0))
	}

	if err := os.MkdirAll(*dir, 0o755); err != nil {
		return fmt.Errorf("criando a pasta %s: %w", *dir, err)
	}
	catalog, err := share.Collect(shared)
	if err != nil {
		return err
	}
	token, err := newToken()
	if err != nil {
		return err
	}
	ip, err := pickIP(*ipFlag)
	if err != nil {
		return err
	}
	portSet := false // o usuário escreveu -port? fs.Visit só passa pelas flags usadas
	fs.Visit(func(f *flag.Flag) { portSet = portSet || f.Name == "port" })
	ln, err := listen(*port, portSet)
	if err != nil {
		return fmt.Errorf("abrindo a porta %d (já tem outro programa nela?): %w", *port, err)
	}
	actualPort := ln.Addr().(*net.TCPAddr).Port // com -port 0, o sistema escolheu
	if *port != 0 && actualPort != *port {
		fmt.Printf("A porta %d já estava em uso; usando a %d.\n\n", *port, actualPort)
	}

	// Ctrl+C cancela o contexto. Com -ttl, o prazo também cancela.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var expiresAt time.Time
	if *ttl > 0 {
		expiresAt = time.Now().Add(*ttl)
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, expiresAt) // contexto filho: o que vencer primeiro
		defer cancel()
	}

	// O hub tem contexto próprio: continua de pé durante o desligamento, para
	// o terminal ainda mostrar os envios que terminarem nesses segundos.
	hubCtx, stopHub := context.WithCancel(context.Background())
	defer stopHub()
	hub := progress.NewHub()
	go hub.Run(hubCtx)

	reports := hub.Subscribe()
	reporterDone := make(chan struct{})
	go func() {
		defer close(reporterDone)
		(&progress.Reporter{Out: os.Stdout}).Run(reports)
	}()

	srv := server.New(server.Config{
		Dir:           *dir,
		Token:         token,
		Hub:           hub,
		MaxSize:       int64(maxSize),
		MaxConcurrent: *maxUploads,
		Shared:        catalog,
		ExpiresAt:     expiresAt,
	})
	// ReadHeaderTimeout protege contra conexões que nunca terminam de mandar
	// o cabeçalho. Não usamos ReadTimeout: cortaria vídeos grandes no meio.
	httpSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	httpSrv.RegisterOnShutdown(srv.CloseEvents) // derruba os SSE, senão o Shutdown espera por eles

	url := fmt.Sprintf("http://%s/?t=%s", net.JoinHostPort(ip.String(), strconv.Itoa(actualPort)), token)
	printBanner(url, ip, *dir, catalog, int64(maxSize), expiresAt)

	// Desligamento gracioso: no Ctrl+C (ou quando o -ttl vence) o servidor
	// para de aceitar conexões e espera até 5 s os envios em andamento.
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown(httpSrv, srv, 5*time.Second)
	}()

	// Serve retorna assim que Shutdown é chamado; esperamos o Shutdown acabar.
	if err := httpSrv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-shutdownDone
	stopHub()      // o hub fecha o channel do Reporter...
	<-reporterDone // ...e esperamos ele imprimir as últimas linhas

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		fmt.Println("\nO link expirou (-ttl).")
	}
	st := srv.Stats()
	fmt.Printf("\nResumo: %d arquivo(s) recebido(s), %s", st.Received, progress.HumanBytes(st.Bytes))
	if st.Failed > 0 {
		fmt.Printf(", %d com erro", st.Failed)
	}
	fmt.Println(". Tchau!")
	return nil
}

// listen abre a porta. Se é a porta padrão (sem -port) e outro programa já
// está nela, usa uma porta livre qualquer: o QR code leva a porta certa de
// qualquer jeito. Quem escolheu a porta com -port recebe o erro.
func listen(port int, explicit bool) (net.Listener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil && !explicit {
		return net.Listen("tcp", ":0")
	}
	return ln, err
}

// shutdown desliga o servidor: espera até grace os envios em andamento e,
// passado o prazo, derruba as conexões que sobraram. Cada upload derrubado
// apaga o que já tinha gravado, e srv.Wait espera essa limpeza. Antes, o
// programa saía no meio de um envio longo e deixava o arquivo pela metade.
func shutdown(httpSrv *http.Server, srv *server.Server, grace time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "qrdrop: interrompendo os envios que não terminaram em %s\n", grace)
		httpSrv.Close()
	}
	srv.Wait()
}

// pickIP usa o IP da flag -ip, se veio, ou detecta o da rede local.
func pickIP(flagValue string) (net.IP, error) {
	if flagValue == "" {
		return netutil.LocalIP()
	}
	ip := net.ParseIP(flagValue)
	if ip == nil || ip.To4() == nil {
		return nil, fmt.Errorf("-ip %q não é um IPv4 válido", flagValue)
	}
	return ip.To4(), nil
}

func printBanner(url string, ip net.IP, dir string, catalog []share.File, maxSize int64, expiresAt time.Time) {
	fmt.Println("Escaneie com a câmera do celular (no mesmo Wi-Fi deste computador):")
	printQR(os.Stdout, url, enableANSI())
	fmt.Println(url)
	fmt.Println()

	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	fmt.Println("Salvando em:", dir)
	if len(catalog) > 0 {
		fmt.Printf("Compartilhando %d arquivo(s) para baixar no celular.\n", len(catalog))
	}
	if maxSize > 0 {
		fmt.Println("Limite por arquivo:", progress.HumanBytes(maxSize))
	}
	if !expiresAt.IsZero() {
		fmt.Printf("O link vale até %s.\n", expiresAt.Format("15:04"))
	}
	if others := otherIPs(ip); len(others) > 0 {
		fmt.Printf("Outros IPs deste computador: %s (se o celular não abrir, tente -ip)\n", strings.Join(others, ", "))
	}
	fmt.Printf("De outro PC: qrdrop send %q arquivo...\n", url)
	fmt.Print("Ctrl+C para sair.\n\n")
}

func otherIPs(current net.IP) []string {
	ips, err := netutil.Candidates()
	if err != nil {
		return nil
	}
	var out []string
	for _, ip := range ips {
		if !ip.Equal(current) {
			out = append(out, ip.String())
		}
	}
	return out
}

func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("gerando token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ---------- enviar ----------

func runSend(args []string) error {
	fs := flag.NewFlagSet("qrdrop send", flag.ExitOnError)
	parallel := fs.Int("p", 4, "quantos arquivos enviar ao mesmo tempo")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "uso: qrdrop send [-p 4] URL arquivo...")
		fs.PrintDefaults()
	}
	fs.Parse(args)
	if fs.NArg() < 2 {
		fs.Usage()
		os.Exit(2)
	}
	base, err := client.ParseURL(fs.Arg(0))
	if err != nil {
		return err
	}
	files := fs.Args()[1:]

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	prog := make(chan client.Progress, 64)
	printerDone := make(chan struct{})
	go printProgress(os.Stdout, prog, len(files), printerDone)

	s := &client.Sender{BaseURL: base, Parallel: *parallel, Progress: prog}
	start := time.Now()
	results := s.SendAll(ctx, files)
	close(prog)
	<-printerDone

	failed := 0
	var total int64
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Printf("ERRO  %s: %v\n", r.File, r.Err)
			continue
		}
		if info, err := os.Stat(r.File); err == nil {
			total += info.Size()
		}
		fmt.Printf("OK    %s -> %s (sha256 %s, integridade conferida)\n", r.File, r.SavedAs, r.SHA256[:12])
	}
	elapsed := time.Since(start)
	fmt.Printf("%d de %d arquivos enviados, %s em %s", len(results)-failed, len(results), progress.HumanBytes(total), elapsed.Round(time.Millisecond))
	if secs := elapsed.Seconds(); total > 0 && secs > 0 {
		fmt.Printf(" (%s/s)", progress.HumanBytes(int64(float64(total)/secs)))
	}
	fmt.Println()
	if failed > 0 {
		return fmt.Errorf("%d arquivo(s) falharam", failed)
	}
	return nil
}

// printProgress redesenha uma linha com quantos arquivos já foram e a
// porcentagem dos que estão indo. Lê do channel até ele ser fechado; o
// ticker limita a 5 redesenhos por segundo. Escreve em out (os.Stdout no
// programa, um bytes.Buffer nos testes).
func printProgress(out io.Writer, in <-chan client.Progress, total int, done chan<- struct{}) {
	defer close(done)
	active := map[string]client.Progress{} // arquivos em andamento
	finished := 0
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	width := 0 // tamanho da última linha desenhada

	draw := func() {
		line := progressLine(finished, total, active)
		// "\r" volta ao início da linha; completar com espaços apaga o que
		// sobrou da linha anterior (funciona até no prompt antigo do Windows).
		n := utf8.RuneCountInString(line)
		fmt.Fprintf(out, "\r%s%s", line, strings.Repeat(" ", max(0, width-n)))
		width = n
	}

	for {
		select {
		case p, ok := <-in:
			if !ok {
				draw()
				fmt.Fprintln(out)
				return
			}
			if p.Done {
				delete(active, p.File)
				finished++
			} else {
				active[p.File] = p
			}
		case <-tick.C:
			draw()
		}
	}
}

// progressLine monta a linha de andamento, por exemplo
// "1/5 prontos | a.jpg  40% | b.mp4   7% | c.pdf  90% | +1".
// É uma função pura (só depende dos argumentos), então o teste é uma tabela.
func progressLine(finished, total int, active map[string]client.Progress) string {
	names := make([]string, 0, len(active))
	for name := range active {
		names = append(names, name)
	}
	sort.Strings(names) // a ordem de um map muda a cada leitura; ordenado, a linha não "pula"
	parts := []string{fmt.Sprintf("%d/%d prontos", finished, total)}
	for i, name := range names {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("+%d", len(names)-3))
			break
		}
		p := active[name]
		pct := 100
		if p.Total > 0 {
			pct = int(p.Sent * 100 / p.Total)
		}
		parts = append(parts, fmt.Sprintf("%s %3d%%", short(name), pct))
	}
	line := strings.Join(parts, " | ")
	if r := []rune(line); len(r) > 79 { // linha maior que o terminal quebraria o "\r"
		line = string(r[:79])
	}
	return line
}

// short tira as pastas do caminho e encurta nomes longos: a linha de
// progresso tem que caber no terminal.
func short(path string) string {
	name := path[strings.LastIndexAny(path, `/\`)+1:]
	if r := []rune(name); len(r) > 18 {
		return string(r[:15]) + "..."
	}
	return name
}
