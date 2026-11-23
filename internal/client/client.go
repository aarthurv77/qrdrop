// Pacote client implementa "qrdrop send": envia arquivos de um PC para outro
// PC que esteja rodando o qrdrop, vários ao mesmo tempo.
package client

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Progress é o andamento de um arquivo no lado de quem envia. O último
// evento de cada arquivo vem com Done (e Err, se deu errado).
type Progress struct {
	File  string
	Sent  int64
	Total int64
	Done  bool
	Err   error
}

// Result é o resultado final de um arquivo.
type Result struct {
	File    string
	SavedAs string // nome com que o receptor salvou
	SHA256  string
	Err     error
}

// Sender envia arquivos para um receptor qrdrop.
type Sender struct {
	BaseURL  *url.URL        // ex.: http://192.168.0.12:8080/?t=TOKEN
	Parallel int             // quantos arquivos ao mesmo tempo
	Client   *http.Client    // nil = http.DefaultClient
	Progress chan<- Progress // opcional: recebe o andamento de cada arquivo
}

// ParseURL aceita a URL que o receptor mostra no terminal (a mesma do QR).
func ParseURL(raw string) (*url.URL, error) {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("URL inválida: %w", err)
	}
	if u.Query().Get("t") == "" {
		return nil, fmt.Errorf("a URL precisa ter o token (?t=...), copie a URL completa que o receptor mostrou")
	}
	return u, nil
}

// SendAll envia os arquivos usando um "pool" de goroutines: Parallel
// trabalhadores pegam arquivos de um channel de tarefas até ele acabar.
// Devolve um resultado por arquivo, na mesma ordem de files.
func (s *Sender) SendAll(ctx context.Context, files []string) []Result {
	workers := s.Parallel
	if workers < 1 {
		workers = 1
	}

	jobs := make(chan int) // índices em files
	results := make([]Result, len(files))
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				// Cada trabalhador escreve numa posição diferente do slice,
				// então não há disputa e não precisamos de mutex.
				results[i] = s.sendFile(ctx, files[i])
			}
		}()
	}

	// Mesmo depois de um Ctrl+C todos os arquivos passam pelos trabalhadores:
	// sendFile devolve na hora com ctx.Err(), e assim cada arquivo tem o seu
	// resultado e o seu evento final de progresso (a contagem "N/M" fecha).
	for i := range files {
		jobs <- i
	}
	close(jobs) // avisa os trabalhadores que não vem mais nada
	wg.Wait()
	return results
}

func (s *Sender) sendFile(ctx context.Context, path string) (res Result) {
	res.File = path
	// Registrado primeiro, roda por último: só avisa "terminou" depois que
	// todos os outros defers (inclusive a espera pela goroutine do corpo)
	// rodaram. res é o retorno nomeado, então aqui já tem o resultado final.
	defer func() {
		if s.Progress != nil {
			s.Progress <- Progress{File: path, Done: true, Err: res.Err}
		}
	}()
	if err := ctx.Err(); err != nil { // cancelado (Ctrl+C) antes de começar
		res.Err = err
		return res
	}
	f, err := os.Open(path)
	if err != nil {
		res.Err = err
		return res
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		res.Err = err
		return res
	}
	if info.IsDir() {
		res.Err = fmt.Errorf("%s é uma pasta", path)
		return res
	}

	// Primeira passada: hash local, para o receptor conferir.
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		res.Err = fmt.Errorf("lendo %s: %w", path, err)
		return res
	}
	res.SHA256 = hex.EncodeToString(h.Sum(nil))
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		res.Err = err
		return res
	}

	// io.Pipe liga quem escreve o corpo multipart (uma goroutine) a quem lê
	// e manda pela rede (o http.Client). Nada é carregado inteiro na memória.
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		part, err := mw.CreateFormFile("file", filepath.Base(path))
		if err == nil {
			_, err = io.Copy(part, &countingReader{r: f, file: path, total: info.Size(), out: s.Progress})
		}
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err) // err nil = fim normal do corpo
	}()
	// Antes de sair, esperamos a goroutine que escreve o corpo terminar.
	// Se o receptor respondeu cedo (token errado, por exemplo), ninguém mais
	// lê o pipe: fechar o lado de leitura destrava a escrita pendente. Sem
	// isso, a goroutine poderia continuar lendo o arquivo e mandando progresso
	// depois que quem chamou SendAll já fechou o channel de progresso.
	// Este defer roda antes do f.Close() lá de cima (defers rodam em ordem inversa).
	defer func() {
		pr.Close()
		<-writerDone
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.uploadURL(), pr)
	if err != nil {
		res.Err = err
		return res
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Upload-ID", newID())
	req.Header.Set("X-File-Size", strconv.FormatInt(info.Size(), 10))
	req.Header.Set("X-SHA256", res.SHA256)

	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		res.Err = err
		return res
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Só queremos a mensagem para mostrar; se a leitura falhar, fica vazia.
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		res.Err = fmt.Errorf("receptor respondeu %s: %s", resp.Status, strings.TrimSpace(string(msg)))
		return res
	}
	var body struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		res.Err = fmt.Errorf("resposta inválida: %w", err)
		return res
	}
	res.SavedAs = body.Name
	if body.SHA256 != res.SHA256 {
		res.Err = fmt.Errorf("SHA-256 não confere (enviado %s, recebido %q)", res.SHA256, body.SHA256)
	}
	return res
}

func (s *Sender) uploadURL() string {
	u := *s.BaseURL // cópia: não alteramos a URL compartilhada entre goroutines
	u.Path = strings.TrimSuffix(u.Path, "/") + "/upload"
	return u.String()
}

// countingReader conta os bytes lidos e avisa o channel de progresso.
type countingReader struct {
	r     io.Reader
	file  string
	sent  int64
	total int64
	out   chan<- Progress
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.sent += int64(n)
	if c.out != nil && n > 0 {
		c.out <- Progress{File: c.file, Sent: c.sent, Total: c.total}
	}
	return n, err
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b) // crypto/rand.Read não falha em sistemas suportados
	return hex.EncodeToString(b)
}
