// Pacote progress repassa eventos de progresso dos uploads para quem quiser
// acompanhar (página web via SSE, terminal do PC).
//
// Modelo: os uploads (produtores) mandam eventos para um channel; uma única
// goroutine, o hub, entrega cada evento a todos os inscritos (consumidores).
// Como só o hub mexe no mapa de inscritos, não é preciso mutex:
// "não comunique compartilhando memória; compartilhe memória comunicando".
package progress

import (
	"context"
	"time"
)

// Event descreve o andamento de um arquivo.
type Event struct {
	ID     string `json:"id"`               // identificador do upload, escolhido pelo cliente
	Name   string `json:"name"`             // nome com que o arquivo foi salvo
	Bytes  int64  `json:"bytes"`            // bytes recebidos até agora
	Total  int64  `json:"total"`            // tamanho esperado (-1 se desconhecido)
	Done   bool   `json:"done"`             // terminou (com ou sem erro)
	Err    string `json:"err,omitempty"`    // mensagem de erro, se houve
	SHA256 string `json:"sha256,omitempty"` // hash do arquivo, quando Done sem erro
}

// Hub distribui eventos para os inscritos.
type Hub struct {
	events      chan Event
	subscribe   chan chan Event
	unsubscribe chan chan Event
	stopped     chan struct{} // fechado quando Run termina
}

// NewHub cria um hub. Chame Run numa goroutine para ele funcionar.
func NewHub() *Hub {
	return &Hub{
		events:      make(chan Event, 256),
		subscribe:   make(chan chan Event),
		unsubscribe: make(chan chan Event),
		stopped:     make(chan struct{}),
	}
}

// Run é o laço do hub. Termina quando ctx é cancelado.
func (h *Hub) Run(ctx context.Context) {
	defer close(h.stopped)
	subs := make(map[chan Event]struct{})
	for {
		select {
		case <-ctx.Done():
			// Quando mais de um case está pronto, o select sorteia um. Então o
			// hub pode parar com eventos ainda na fila (o "terminou" do último
			// upload antes do Ctrl+C, por exemplo): entregamos esses antes.
			h.drain(subs)
			for c := range subs {
				close(c)
			}
			return
		case c := <-h.subscribe:
			subs[c] = struct{}{}
		case c := <-h.unsubscribe:
			if _, ok := subs[c]; ok {
				delete(subs, c)
				close(c)
			}
		case ev := <-h.events:
			broadcast(subs, ev)
		}
	}
}

// drain entrega os eventos que já estavam na fila e volta quando ela esvazia.
func (h *Hub) drain(subs map[chan Event]struct{}) {
	for {
		select {
		case ev := <-h.events:
			broadcast(subs, ev)
		default: // fila vazia
			return
		}
	}
}

func broadcast(subs map[chan Event]struct{}, ev Event) {
	for c := range subs {
		deliver(c, ev)
	}
}

// doneTimeout é quanto o hub espera um inscrito lento aceitar um evento final.
const doneTimeout = 50 * time.Millisecond

// deliver entrega ev a um inscrito sem deixar um inscrito lento travar todos.
// Evento de progresso com a fila cheia é descartado: o próximo traz o número
// atualizado. Já o evento final (Done) não pode se perder, então esperamos
// um pouco o inscrito abrir espaço; só um inscrito travado de vez o perde.
func deliver(c chan Event, ev Event) {
	select {
	case c <- ev:
		return
	default:
	}
	if !ev.Done {
		return
	}
	t := time.NewTimer(doneTimeout)
	defer t.Stop()
	select {
	case c <- ev:
	case <-t.C:
	}
}

// Publish envia um evento ao hub. Eventos intermediários são descartados se o
// hub estiver sobrecarregado; o evento final (Done) sempre chega ao hub.
func (h *Hub) Publish(ev Event) {
	if ev.Done {
		select {
		case h.events <- ev:
		case <-h.stopped:
		}
		return
	}
	select {
	case h.events <- ev:
	default:
	}
}

// Subscribe devolve um channel que recebe todos os eventos a partir de agora.
// Chame Unsubscribe quando não quiser mais receber.
func (h *Hub) Subscribe() chan Event {
	c := make(chan Event, 64)
	select {
	case h.subscribe <- c:
	case <-h.stopped:
		close(c)
	}
	return c
}

// Unsubscribe cancela a inscrição e fecha o channel.
func (h *Hub) Unsubscribe(c chan Event) {
	select {
	case h.unsubscribe <- c:
	case <-h.stopped:
	}
}
