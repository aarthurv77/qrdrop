package server

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"qrdrop/internal/progress"
	"qrdrop/internal/share"
	"qrdrop/web"
)

// Config reúne o que o Server precisa. Só Dir, Token e Hub são obrigatórios.
type Config struct {
	Dir           string        // pasta de destino
	Token         string        // token exigido em toda requisição (menos /healthz)
	Hub           *progress.Hub // para onde vão os eventos de progresso
	MaxSize       int64         // tamanho máximo por arquivo, em bytes (0 = sem limite)
	MaxConcurrent int           // arquivos gravando ao mesmo tempo (0 = DefaultMaxConcurrent)
	Shared        []share.File  // arquivos oferecidos para download (PC -> celular)
	ExpiresAt     time.Time     // fim da sessão, só para mostrar na página (zero = sem fim)
}

// DefaultMaxConcurrent é o limite de gravações simultâneas quando a Config
// não define outro. A página manda 4 por vez; sobra espaço para dois celulares.
const DefaultMaxConcurrent = 8

// Server recebe arquivos, publica o progresso no Hub e serve os downloads.
type Server struct {
	cfg       Config
	shared    map[string]share.File // nome -> arquivo, para achar o download pelo nome
	slots     chan struct{}         // semáforo: um "slot" por gravação em andamento
	quit      chan struct{}         // fechado por CloseEvents
	closeOnce sync.Once

	// Estatísticas. Várias goroutines (uma por upload) somam nesses
	// contadores ao mesmo tempo; sync/atomic garante isso sem mutex.
	received, failed, bytes, active atomic.Int64

	anonymous atomic.Int64   // numera os uploads que chegam sem X-Upload-ID
	saving    sync.WaitGroup // gravações em andamento, para Wait

	testHookSaving func() // só nos testes: chamado com o slot ocupado
}

// New cria um Server a partir da configuração.
func New(cfg Config) *Server {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = DefaultMaxConcurrent
	}
	if cfg.Shared == nil {
		cfg.Shared = []share.File{} // na resposta JSON vira [] em vez de null
	}
	s := &Server{
		cfg:    cfg,
		shared: make(map[string]share.File, len(cfg.Shared)),
		slots:  make(chan struct{}, cfg.MaxConcurrent),
		quit:   make(chan struct{}),
	}
	for _, f := range cfg.Shared {
		s.shared[f.Name] = f
	}
	return s
}

// Wait espera as gravações em andamento terminarem, inclusive a limpeza dos
// arquivos pela metade de uploads interrompidos. Chame depois de
// http.Server.Shutdown ou Close: sem isso, o programa poderia sair antes
// dessa limpeza e deixar um pedaço de arquivo na pasta.
func (s *Server) Wait() { s.saving.Wait() }

// Handler monta as rotas. Server não precisa implementar http.Handler ele
// mesmo: devolvemos um ServeMux, que já implementa (interface implícita).
// Os padrões "MÉTODO /caminho/{nome}" existem no ServeMux desde o Go 1.22.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.requireToken(s.handleIndex))
	mux.HandleFunc("GET /info", s.requireToken(s.handleInfo))
	mux.HandleFunc("POST /upload", s.requireToken(s.handleUpload))
	mux.HandleFunc("GET /events", s.requireToken(s.handleEvents))
	mux.HandleFunc("GET /download/{name}", s.requireToken(s.handleDownload))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	return securityHeaders(mux)
}

// securityHeaders é um middleware que vale para todas as rotas.
//   - nosniff: o navegador usa o Content-Type que mandamos, sem "adivinhar".
//     Um .html compartilhado com -share é baixado, nunca executado.
//   - no-referrer: o token está na URL; assim ele nunca vai no cabeçalho
//     Referer para outro site.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// requireToken é um middleware: recebe um handler e devolve outro que só
// chama o original se o token estiver certo. Funções como valores + closure.
func (s *Server) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.URL.Query().Get("t")
		if got == "" {
			got = r.Header.Get("X-Token")
		}
		// Comparação em tempo constante: não vaza quantos caracteres acertaram.
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.Token)) != 1 {
			http.Error(w, "token inválido", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	http.ServeFileFS(w, r, web.Files, "index.html")
}

// Stats é um retrato dos contadores do servidor.
type Stats struct {
	Received int64 `json:"received"` // arquivos recebidos com sucesso
	Failed   int64 `json:"failed"`   // envios que falharam ou foram recusados
	Bytes    int64 `json:"bytes"`    // total recebido com sucesso
	Active   int64 `json:"active"`   // gravações em andamento agora
}

// Stats lê os contadores. Cada Load é atômico; o conjunto é uma foto
// aproximada, o que basta para mostrar na tela.
func (s *Server) Stats() Stats {
	return Stats{
		Received: s.received.Load(),
		Failed:   s.failed.Load(),
		Bytes:    s.bytes.Load(),
		Active:   s.active.Load(),
	}
}

// Info é a resposta de GET /info: o que a página precisa saber ao abrir.
type Info struct {
	MaxSize   int64        `json:"maxSize"`
	ExpiresAt *time.Time   `json:"expiresAt,omitempty"` // ponteiro: ausente quando não há validade
	Shared    []share.File `json:"shared"`
	Stats     Stats        `json:"stats"`
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	info := Info{MaxSize: s.cfg.MaxSize, Shared: s.cfg.Shared, Stats: s.Stats()}
	if !s.cfg.ExpiresAt.IsZero() {
		info.ExpiresAt = &s.cfg.ExpiresAt
	}
	writeJSON(w, info)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	// Se a escrita falhar, o cliente já desconectou: não há a quem avisar.
	json.NewEncoder(w).Encode(v)
}
