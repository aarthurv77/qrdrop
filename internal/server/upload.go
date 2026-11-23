package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"qrdrop/internal/progress"
	"qrdrop/internal/storage"
)

// UploadResult é a resposta JSON de POST /upload.
type UploadResult struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

var (
	errHashMismatch = errors.New("SHA-256 não confere: arquivo corrompido no caminho")
	errInterrupted  = errors.New("envio interrompido no meio (cancelado ou a conexão caiu)")
)

// TooLargeError indica um arquivo maior que o limite (-max-size). Por ser um
// tipo, e não só uma mensagem, quem recebe o erro pode usar errors.As para
// descobrir o limite.
type TooLargeError struct{ Limit int64 }

func (e *TooLargeError) Error() string {
	return "arquivo maior que o limite de " + progress.HumanBytes(e.Limit)
}

// handleUpload recebe UM arquivo por requisição. O cliente manda vários
// arquivos em requisições paralelas, e o net/http já atende cada requisição
// na sua própria goroutine.
//
// Cabeçalhos opcionais:
//
//	X-Upload-ID   identificador para casar os eventos de progresso (sem ele, geramos um)
//	X-File-Size   tamanho do arquivo, para a porcentagem e para recusar cedo
//	X-SHA256      hash esperado, em hexadecimal; se não bater, o arquivo é apagado (422)
//
// Só a primeira parte do formulário que traz um arquivo é salva: um arquivo
// por requisição. Campos de texto antes dela são ignorados.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("X-Upload-ID")
	if id == "" {
		// curl e outros clientes não mandam ID; sem um, os eventos de dois
		// envios desses ao mesmo tempo se misturariam no terminal.
		id = "anon-" + strconv.FormatInt(s.anonymous.Add(1), 10)
	}
	total, err := strconv.ParseInt(r.Header.Get("X-File-Size"), 10, 64)
	if err != nil || total < 0 {
		total = -1
	}
	if s.cfg.MaxSize > 0 && total > s.cfg.MaxSize {
		// Recusa antes de ler o corpo: não faz sentido receber 2 GB para jogar fora.
		s.failed.Add(1)
		http.Error(w, (&TooLargeError{s.cfg.MaxSize}).Error(), http.StatusRequestEntityTooLarge)
		return
	}
	// O PowerShell (Get-FileHash) escreve o hash em MAIÚSCULAS e o sha256sum
	// em minúsculas: os dois valem. Um hash malformado é recusado já aqui;
	// senão o arquivo inteiro viria pela rede só para dar "não confere".
	wantHash := strings.ToLower(strings.TrimSpace(r.Header.Get("X-SHA256")))
	if wantHash != "" && !validSHA256(wantHash) {
		s.failed.Add(1)
		http.Error(w, "X-SHA256 inválido: esperava 64 caracteres hexadecimais", http.StatusBadRequest)
		return
	}

	// Semáforo com channel: no máximo MaxConcurrent gravações ao mesmo tempo.
	// Com todos os slots ocupados, esta goroutine espera aqui (e o envio do
	// cliente fica parado) até alguém liberar um, ou até o cliente desistir.
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-r.Context().Done():
		return
	}
	if s.testHookSaving != nil {
		s.testHookSaving()
	}

	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "esperava multipart/form-data", http.StatusBadRequest)
		return
	}
	part, err := filePart(mr)
	if err != nil {
		http.Error(w, "nenhum arquivo enviado", http.StatusBadRequest)
		return
	}
	defer part.Close()

	res, err := s.save(id, part.FileName(), total, part, wantHash)
	if err != nil {
		status := http.StatusInternalServerError
		var tooLarge *TooLargeError
		switch {
		case errors.Is(err, errHashMismatch):
			status = http.StatusUnprocessableEntity
		case errors.As(err, &tooLarge):
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, res)
}

// filePart avança até a primeira parte do formulário que traz um arquivo.
// Sem isso, um campo de texto que viesse antes (curl -F nome=x -F file=@a)
// seria salvo como se fosse o arquivo.
func filePart(mr *multipart.Reader) (*multipart.Part, error) {
	for {
		part, err := mr.NextPart()
		if err != nil {
			return nil, err // io.EOF: o formulário acabou sem nenhum arquivo
		}
		if part.FileName() != "" {
			return part, nil
		}
		part.Close()
	}
}

// validSHA256 diz se s tem a cara de um SHA-256 em hexadecimal.
func validSHA256(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil && len(s) == 2*sha256.Size
}

// save grava src em disco em streaming, calculando o SHA-256 no mesmo passo.
func (s *Server) save(id, name string, total int64, src io.Reader, wantHash string) (res UploadResult, err error) {
	s.saving.Add(1) // Wait espera por este Done
	defer s.saving.Done()
	s.active.Add(1)
	defer s.active.Add(-1)

	res.Name = storage.SafeName(name)
	var path string // fica vazio se nem deu para criar o arquivo

	// Este defer roda por último e cuida do desfecho, qualquer que seja o
	// caminho de saída: apaga o que ficou pela metade, atualiza as
	// estatísticas e avisa o hub. Repare que ele lê o err nomeado no retorno.
	defer func() {
		ev := progress.Event{ID: id, Name: res.Name, Bytes: res.Size, Total: total, Done: true}
		if err != nil {
			if path != "" {
				os.Remove(path) // melhor esforço: não deixar arquivo pela metade
			}
			ev.Err = err.Error()
			s.failed.Add(1)
		} else {
			ev.SHA256 = res.SHA256
			s.received.Add(1)
			s.bytes.Add(res.Size)
		}
		s.cfg.Hub.Publish(ev)
	}()

	f, err := storage.Create(s.cfg.Dir, name)
	if err != nil {
		return res, err
	}
	path = f.Name()
	res.Name = filepath.Base(path)
	// Defers rodam em ordem inversa: este fecha o arquivo ANTES do defer de
	// cima tentar apagá-lo (no Windows não dá para apagar um arquivo aberto).
	defer func() {
		if cerr := f.Close(); err == nil && cerr != nil {
			err = cerr
		}
	}()

	body := &bodyReader{r: src}
	src = body
	if s.cfg.MaxSize > 0 {
		// Lê no máximo MaxSize+1 bytes: se o byte a mais vier, passou do limite.
		src = io.LimitReader(src, s.cfg.MaxSize+1)
	}
	h := sha256.New()
	pw := &progressWriter{hub: s.cfg.Hub, ev: progress.Event{ID: id, Name: res.Name, Total: total}}
	// io.MultiWriter: cada pedaço lido vai ao mesmo tempo para o disco,
	// para o hash e para o contador de progresso. Memória usada: só o buffer
	// do io.Copy (32 KB), não importa o tamanho do arquivo.
	res.Size, err = io.Copy(io.MultiWriter(f, h, pw), src)
	if body.err != nil {
		// Falhou a leitura do corpo, não a escrita no disco: o cliente
		// cancelou, o Wi-Fi caiu ou o qrdrop está desligando. A mensagem
		// técnica ("read tcp ...: connection reset") não ajuda ninguém.
		return res, errInterrupted
	}
	if err != nil { // falhou a escrita (disco cheio, por exemplo)
		return res, fmt.Errorf("gravando %s: %w", res.Name, err)
	}
	if s.cfg.MaxSize > 0 && res.Size > s.cfg.MaxSize {
		return res, &TooLargeError{s.cfg.MaxSize}
	}
	res.SHA256 = hex.EncodeToString(h.Sum(nil))
	if wantHash != "" && wantHash != res.SHA256 {
		return res, errHashMismatch
	}
	return res, nil
}

// bodyReader repassa as leituras do corpo e guarda o erro, se houver, para
// save saber de que lado o io.Copy falhou: na leitura (rede) ou na escrita (disco).
type bodyReader struct {
	r   io.Reader
	err error
}

func (b *bodyReader) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err != nil && err != io.EOF { // io.EOF é o fim normal, não um erro
		b.err = err
	}
	return n, err
}

// progressWriter implementa io.Writer só para contar bytes e publicar
// eventos, no máximo um a cada 100 ms para não inundar o hub.
type progressWriter struct {
	hub  *progress.Hub
	ev   progress.Event
	last time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.ev.Bytes += int64(len(b))
	if now := time.Now(); now.Sub(p.last) >= 100*time.Millisecond {
		p.last = now
		p.hub.Publish(p.ev)
	}
	return len(b), nil
}
