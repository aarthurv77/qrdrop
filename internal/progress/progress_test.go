package progress

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"
	"time"
)

func startHub(t *testing.T) (*Hub, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h := NewHub()
	go h.Run(ctx)
	t.Cleanup(cancel)
	return h, cancel
}

// recv espera um evento por até 1 s, para um teste com defeito não travar.
func recv(t *testing.T, c <-chan Event) Event {
	t.Helper()
	select {
	case ev, ok := <-c:
		if !ok {
			t.Fatal("channel fechado")
		}
		return ev
	case <-time.After(time.Second):
		t.Fatal("nenhum evento em 1 s")
	}
	return Event{}
}

func TestTodosOsInscritosRecebem(t *testing.T) {
	h, _ := startHub(t)
	a, b := h.Subscribe(), h.Subscribe()

	h.Publish(Event{ID: "1", Name: "foto.jpg", Done: true})

	for _, c := range []chan Event{a, b} {
		if ev := recv(t, c); ev.ID != "1" || !ev.Done {
			t.Errorf("evento inesperado: %+v", ev)
		}
	}
}

func TestUnsubscribeFechaOChannel(t *testing.T) {
	h, _ := startHub(t)
	c := h.Subscribe()
	h.Unsubscribe(c)
	select {
	case _, ok := <-c:
		if ok {
			t.Fatal("esperava channel fechado")
		}
	case <-time.After(time.Second):
		t.Fatal("Unsubscribe não fechou o channel")
	}
}

// Um inscrito que não lê não pode travar os uploads: o hub descarta os
// eventos dele e segue entregando para os outros.
func TestInscritoLentoNaoTravaNinguem(t *testing.T) {
	h, _ := startHub(t)
	_ = h.Subscribe() // nunca lê
	rapido := h.Subscribe()

	got := make(chan int)
	go func() {
		n := 0
		for ev := range rapido {
			n++
			if ev.Done {
				got <- n
				return
			}
		}
	}()

	publicado := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			h.Publish(Event{ID: "x", Bytes: int64(i)})
		}
		h.Publish(Event{ID: "x", Done: true})
		close(publicado)
	}()

	select {
	case <-publicado:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish travou por causa do inscrito lento")
	}
	select {
	case n := <-got:
		if n < 1 {
			t.Error("o inscrito rápido não recebeu nada")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("o inscrito rápido não recebeu o evento final")
	}
}

// Um inscrito com a fila cheia ainda recebe o evento final, se abrir espaço
// em até doneTimeout (50 ms). Os eventos de progresso podem se perder; o
// final, não: é ele que faz a página e o terminal mostrarem "recebido".
func TestEventoFinalEsperaInscritoComFilaCheia(t *testing.T) {
	h, _ := startHub(t)
	c := h.Subscribe()
	for i := 0; i < cap(c); i++ {
		h.Publish(Event{ID: "x", Bytes: int64(i)}) // enche a fila do inscrito
	}
	h.Publish(Event{ID: "x", Done: true})
	time.Sleep(doneTimeout / 3) // o hub já tentou entregar e está esperando espaço

	for i := 0; i < cap(c); i++ {
		recv(t, c) // o inscrito "acorda" e esvazia a fila
	}
	if ev := recv(t, c); !ev.Done {
		t.Fatalf("esperava o evento final, veio %+v", ev)
	}
}

func TestPararOHubFechaInscritosESoltaPublish(t *testing.T) {
	h, cancel := startHub(t)
	c := h.Subscribe()
	cancel()

	select {
	case _, ok := <-c:
		if ok {
			t.Fatal("esperava channel fechado")
		}
	case <-time.After(time.Second):
		t.Fatal("o hub parou mas não fechou o channel")
	}

	done := make(chan struct{})
	go func() {
		h.Publish(Event{Done: true}) // com o hub parado não pode travar
		h.Unsubscribe(h.Subscribe())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish/Subscribe travaram com o hub parado")
	}
}

// Eventos que já estavam na fila quando o hub parou ainda são entregues.
// Sem o drain, o select do Run sorteia entre "parar" e "entregar" e alguns
// destes 50 se perderiam (este teste falha na maioria das vezes sem ele).
func TestPararOHubEntregaOQueJaEstavaNaFila(t *testing.T) {
	for rodada := 0; rodada < 20; rodada++ {
		h, cancel := startHub(t)
		c := h.Subscribe()
		for i := 0; i < 50; i++ { // 50 cabem no buffer do inscrito (64)
			h.Publish(Event{ID: strconv.Itoa(i), Done: true})
		}
		cancel()

		n := 0
		for range c { // range termina quando o hub fecha o channel
			n++
		}
		if n != 50 {
			t.Fatalf("rodada %d: o inscrito recebeu %d de 50 eventos", rodada, n)
		}
	}
}

func TestReporter(t *testing.T) {
	var out bytes.Buffer
	// Relógio falso: Run chama Now uma vez por evento, e cada chamada devolve
	// o próximo horário da lista. Só a goroutine do Reporter mexe em i.
	t0 := time.Date(2026, 11, 25, 14, 0, 0, 0, time.UTC)
	t2 := t0.Add(2 * time.Second)
	times := []time.Time{t0, t2, t2, t2, t2.Add(50 * time.Millisecond)}
	i := 0
	r := &Reporter{Out: &out, Now: func() time.Time { i++; return times[i-1] }}

	events := make(chan Event)
	done := make(chan struct{})
	go func() { r.Run(events); close(done) }()

	events <- Event{ID: "v", Name: "video.mp4", Bytes: 1 << 20, Total: 20 << 20}
	events <- Event{ID: "v", Name: "video.mp4", Bytes: 20 << 20, Total: 20 << 20, Done: true, SHA256: "9a982e9cd0591d5d34"}
	events <- Event{ID: "f", Name: "foto.jpg", Done: true, Err: "SHA-256 não confere"}
	// Chega em 50 ms: rápido demais para tempo e velocidade fazerem sentido.
	events <- Event{ID: "r", Name: "nota.txt", Bytes: 0, Total: 10}
	events <- Event{ID: "r", Name: "nota.txt", Bytes: 10, Total: 10, Done: true, SHA256: "0123456789abcdef"}
	close(events)
	<-done

	want := []string{
		"14:00:00 recebendo video.mp4 (20.0 MB)",
		"14:00:02 OK        video.mp4  20.0 MB em 2s (10.0 MB/s)  sha256 9a982e9cd059",
		"14:00:02 ERRO      foto.jpg: SHA-256 não confere",
		"14:00:02 recebendo nota.txt (10 B)",
		"14:00:02 OK        nota.txt  10 B  sha256 0123456789ab",
	}
	got := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(got) != len(want) {
		t.Fatalf("saída inesperada:\n%s", out.String())
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("linha %d:\n got  %q\n want %q", i, got[i], want[i])
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KB", 30 << 20: "30.0 MB", 3 << 30: "3.0 GB"}
	for in, want := range cases {
		if got := HumanBytes(in); got != want {
			t.Errorf("HumanBytes(%d) = %q, queria %q", in, got, want)
		}
	}
}
