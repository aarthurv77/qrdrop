package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// heartbeat é o intervalo dos "pings" no stream de eventos. Sem tráfego,
// celulares e roteadores costumam derrubar conexões paradas.
var heartbeat = 20 * time.Second

// handleEvents mantém a conexão aberta e manda cada evento de progresso
// como Server-Sent Events (texto "data: {...}\n\n").
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher) // type assertion: o ResponseWriter sabe "flushar"?
	if !ok {
		http.Error(w, "streaming não suportado", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprint(w, ": conectado\n\n")
	flusher.Flush()

	events := s.cfg.Hub.Subscribe()
	defer s.cfg.Hub.Unsubscribe(events)
	ping := time.NewTicker(heartbeat)
	defer ping.Stop()

	// select espera o que acontecer primeiro entre quatro channels.
	for {
		select {
		case <-r.Context().Done(): // a página foi fechada
			return
		case <-s.quit: // o servidor está desligando
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n") // linha de comentário: o navegador ignora
			flusher.Flush()
		case ev, ok := <-events:
			if !ok { // o hub parou
				return
			}
			data, _ := json.Marshal(ev) // Event só tem tipos simples: Marshal não falha
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// CloseEvents encerra as conexões de progresso (SSE). Registre com
// http.Server.RegisterOnShutdown: sem isso o Shutdown ficaria esperando
// essas conexões, que nunca terminam sozinhas. Pode ser chamado várias vezes.
func (s *Server) CloseEvents() {
	s.closeOnce.Do(func() { close(s.quit) })
}
