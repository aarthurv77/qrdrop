package server

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"qrdrop/internal/progress"
)

// Cinquenta uploads ao mesmo tempo, de verdade: uma barreira segura cada
// upload até os 50 estarem gravando juntos. Enquanto isso, outras goroutines
// entram e saem do hub sem parar (como celulares abrindo e fechando a
// página). Rodado com -race (é assim no CI), o teste mostra que o hub não
// precisa de mutex: só a goroutine dele mexe no mapa de inscritos.
//
// Confere também que o inscrito que ficou do começo ao fim recebe o evento
// final de cada um dos 50 uploads, com o hash certo.
func TestCinquentaUploadsSimultaneos(t *testing.T) {
	const n = 50
	e := newTestServer(t, func(c *Config) { c.MaxConcurrent = n })

	// Barreira: cada upload avisa que chegou e espera todos chegarem.
	var arrived sync.WaitGroup
	arrived.Add(n)
	release := make(chan struct{})
	var releaseOnce sync.Once
	open := func() { releaseOnce.Do(func() { close(release) }) }
	// Se o teste falhar antes de os 50 chegarem, solta os que estão presos;
	// senão o fechamento do servidor de teste esperaria por eles para sempre.
	t.Cleanup(open)
	e.srv.testHookSaving = func() {
		arrived.Done()
		<-release
	}
	go func() {
		arrived.Wait() // os 50 estão com o slot ocupado, ao mesmo tempo
		open()
	}()

	watcher := e.hub.Subscribe()
	defer e.hub.Unsubscribe(watcher)

	// Inscritos que entram e saem durante os uploads.
	stopChurn := make(chan struct{})
	var churn sync.WaitGroup
	for i := 0; i < 5; i++ {
		churn.Add(1)
		go func() {
			defer churn.Done()
			for {
				select {
				case <-stopChurn:
					return
				default:
				}
				c := e.hub.Subscribe()
				e.hub.Unsubscribe(c)
			}
		}()
	}

	want := map[string]string{} // ID -> SHA-256 esperado
	var uploads sync.WaitGroup
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("up-%02d", i)
		data := []byte("conteúdo do arquivo " + id)
		want[id] = sum(data)
		uploads.Add(1)
		go func() {
			defer uploads.Done()
			req := uploadReq(t, e.url("/upload"), "foto.jpg", data, map[string]string{"X-Upload-ID": id})
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("%s: status %d", id, resp.StatusCode)
			}
		}()
	}

	// Lê os eventos enquanto os uploads acontecem, até ter os 50 finais.
	done := map[string]progress.Event{}
	timeout := time.After(10 * time.Second)
	for len(done) < n {
		select {
		case ev := <-watcher:
			if ev.Done {
				if _, dup := done[ev.ID]; dup {
					t.Errorf("evento final repetido para %s", ev.ID)
				}
				done[ev.ID] = ev
			}
		case <-timeout:
			t.Fatalf("só %d de %d eventos finais chegaram em 10 s", len(done), n)
		}
	}
	uploads.Wait()
	close(stopChurn)
	churn.Wait()

	for id, sha := range want {
		ev, ok := done[id]
		switch {
		case !ok:
			t.Errorf("%s: sem evento final", id)
		case ev.Err != "" || ev.SHA256 != sha:
			t.Errorf("%s: evento final %+v, queria SHA-256 %s", id, ev, sha)
		}
	}
	// Mesmo nome nos 50: storage.Create (O_EXCL) dá um nome diferente a cada um.
	if got := len(files(t, e.dir)); got != n {
		t.Errorf("%d arquivos na pasta, queria %d", got, n)
	}
	if st := e.srv.Stats(); st.Received != n || st.Failed != 0 || st.Active != 0 {
		t.Errorf("estatísticas %+v", st)
	}
}
