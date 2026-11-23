# Análise da linguagem Go

Este documento é a base do vídeo (máx. 10 min). A estrutura segue **exatamente** os itens da planilha de avaliação. Cada seção indica o peso e o que precisa aparecer.

> Regra do grupo: todo exemplo de código aqui deve vir **do nosso repositório**, com link para o arquivo. A planilha cobra "exemplos de código na LP que foram [feitos] pelo grupo".

---

## 1. Histórico e versão (0,5)

- [ ] Origem: Google, 2007; Robert Griesemer, Rob Pike, Ken Thompson
- [ ] Motivação: builds lentos de C++, dificuldade com concorrência em servidores, complexidade das linguagens da época
- [ ] Marcos: anúncio público (2009), Go 1.0 e a promessa de compatibilidade (2012), módulos (1.11–1.16), generics (1.18)
- [ ] Versão usada no projeto: _preencher (ver `go.mod`)_
- [ ] Fontes: _preencher_

## 2. Domínio de aplicação, usuário característico e premissas de projeto (1,5)

Seção de maior peso. Precisa ligar **premissa da linguagem → decisão no nosso projeto**.

- [ ] Domínio: servidores, infraestrutura de nuvem, ferramentas de linha de comando (Docker, Kubernetes, Terraform)
- [ ] Usuário característico: equipes grandes mantendo serviços de rede
- [ ] Premissas de projeto:
  - Simplicidade (poucas palavras-chave, uma forma de fazer as coisas, `gofmt`)
  - Compilação rápida
  - Concorrência como recurso da linguagem (CSP de Hoare)
  - Composição em vez de herança
  - Erros explícitos
- [ ] Relação com os critérios do Sebesta (cap. 1): legibilidade, facilidade de escrita, confiabilidade, custo
- [ ] Onde cada premissa aparece no qrdrop: _preencher com links_

## 3. Construtores da linguagem (1,0)

Para cada item: explicação curta + trecho do nosso código.

| Construtor | Onde aparece no qrdrop |
|---|---|
| Tipos básicos e compostos (struct, slice, map) | `Event` e o mapa de inscritos em [hub.go](../internal/progress/hub.go); `share.File` e o mapa nome → arquivo em [server.go](../internal/server/server.go) |
| Declarações (`var`, `:=`, `const`, inferência de tipos) | `const MaxNameBytes` e `var windowsReserved` em [storage.go](../internal/storage/storage.go); `var version` trocada no build em [main.go](../cmd/qrdrop/main.go) |
| Comandos de controle (`for` único, `switch`, `select`) | `select` com quatro channels em [events.go](../internal/server/events.go); `for range` num channel em [reporter.go](../internal/progress/reporter.go); `switch` de tipo em [ip.go](../internal/netutil/ip.go) |
| Funções: múltiplos retornos, funções como valores, closures | middleware `requireToken` em [server.go](../internal/server/server.go); retorno nomeado de `save` em [upload.go](../internal/server/upload.go); `sort.Slice` em [share.go](../internal/share/share.go) |
| `defer` | `save` em [upload.go](../internal/server/upload.go) (ordem inversa: fecha o arquivo antes de apagar); espera da goroutine do corpo em [client.go](../internal/client/client.go) |
| Interfaces implícitas (`io.Reader`, `http.Handler`) | `progressWriter` é um `io.Writer` em [upload.go](../internal/server/upload.go); `byteSize` e `stringList` são `flag.Value` em [flags.go](../cmd/qrdrop/flags.go); `io.ReadSeeker` em [download.go](../internal/server/download.go) |
| Goroutines e channels | hub em [hub.go](../internal/progress/hub.go); pool de envio em [client.go](../internal/client/client.go); semáforo em [upload.go](../internal/server/upload.go); `sync/atomic` em [server.go](../internal/server/server.go) |
| Tratamento de erro (`error`, `if err != nil`, `errors.Is`) | `TooLargeError` + `errors.As` e `errHashMismatch` + `errors.Is` em [upload.go](../internal/server/upload.go) |
| Pacotes e visibilidade (maiúscula/minúscula, `internal/`) | pastas em `internal/`; `Stats()` exportado e `save`, `deliver` não exportados |

Ferramentas que vêm com a linguagem, também com exemplos no repositório: testes de tabela, fuzzing nativo (`FuzzSafeName`) e exemplos testáveis (`ExampleSafeName`) em [storage_test.go](../internal/storage/storage_test.go); benchmark em MB/s (`BenchmarkUpload`) em [server_test.go](../internal/server/server_test.go); detector de corrida (`go test -race`), que achou um bug real no `qrdrop send` (ver histórico de commits); e um teste de ponta a ponta em [main_test.go](../cmd/qrdrop/main_test.go) que compila o programa com `os/exec`, sobe o receptor, envia arquivos com `qrdrop send` e manda Ctrl+C.

Compilação condicional: [console_windows.go](../cmd/qrdrop/console_windows.go) começa com `//go:build windows` e só entra no binário do Windows (segura a janela aberta com dois cliques e liga as cores ANSI); nos outros sistemas entra [console_other.go](../cmd/qrdrop/console_other.go), com as mesmas funções. É assim que o mesmo código compila para os três sistemas.

Testando os testes: plantamos 32 defeitos no código, um por vez (inverter uma condição, apagar uma linha), e rodamos os testes. Na primeira rodada 9 passaram despercebidos; cada um ganhou um teste, e hoje os 32 são pegos. Os testes com "olhar de atacante" (hash em maiúsculas, formulário fora de ordem, conexão derrubada no meio, vazamento de goroutines) estão em [robustez_test.go](../internal/server/robustez_test.go).

### Ganchos com o conteúdo da disciplina

| Aula | Conceito | Onde aparece no qrdrop |
|---|---|---|
| **05 – Tradução** | Compilação para código nativo; binário estático e autossuficiente; compilação cruzada; diretivas para o compilador | `//go:embed` em [embed.go](../web/embed.go) põe o HTML dentro do binário; `//go:build windows` em [console_windows.go](../cmd/qrdrop/console_windows.go) escolhe o arquivo por sistema; [build.sh](../scripts/build.sh) gera os seis binários de uma máquina só, trocando `GOOS`/`GOARCH` (exemplo 5) |
| **07 – Abstração** | Tipo abstrato = dados + operações; encapsulamento; interfaces | `progressWriter` vira um `io.Writer` só por ter o método `Write` (exemplo 1); o `Hub` esconde o mapa de inscritos e só expõe `Publish`, `Subscribe` e `Unsubscribe`, e nomes com minúscula (`save`, `deliver`, `subs`) não saem do pacote; `internal/` impede que outro módulo importe os pacotes |
| **08 – Controle de sequência** | Seleção, iteração, desvio; tratamento de exceções | Só existe `for` (com `range` sobre map e channel); `select` escolhe entre channels prontos (exemplo 2); sem exceções: o erro é um valor de retorno, testado com `if err != nil`, `errors.Is` e `errors.As` em [upload.go](../internal/server/upload.go); `defer` garante a limpeza em qualquer saída (exemplo 3) |
| **09 – Subprogramas** | Passagem de parâmetros; subprogramas como parâmetros; closures; corrotinas/concorrência | Tudo é passado por valor: `save` recebe `src io.Reader` (uma cópia da interface) e o receptor `*Server` é uma cópia do ponteiro; múltiplos retornos e retorno nomeado em `save` (exemplo 3); `requireToken` recebe e devolve uma função (exemplo 4); goroutines são subprogramas que rodam concorrentemente, criados com `go` (o hub em [main.go](../cmd/qrdrop/main.go), uma por requisição dentro do `net/http`) |

## 4. Exemplos de código feitos pelo grupo (1,0)

Cinco trechos do nosso código, cada um mostrando um recurso diferente. Os trechos estão resumidos (`// ...` marca o que foi cortado); o código completo está no link de cada um.

### Exemplo 1 – Interface implícita e streaming ([upload.go](../internal/server/upload.go), `progressWriter` e `save`)

```go
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

// em save:
res.Size, err = io.Copy(io.MultiWriter(f, h, pw), src)
```

Em nenhum lugar está escrito "`progressWriter` implementa `io.Writer`". Em Go, um tipo satisfaz uma interface só por ter os métodos dela (tipagem estrutural); em Java seria preciso `implements`. Por isso tipos que nunca se conheceram se encaixam: o arquivo (`*os.File`), o hash (`sha256`) e o nosso contador são todos `io.Writer`, e o `io.MultiWriter` da biblioteca padrão junta os três. Cada pedaço de 32 KB lido da rede vai para o disco, para o hash e para a barra de progresso de uma vez. A memória usada é só o buffer do `io.Copy`, seja o arquivo de 1 MB ou de 2 GB.

**Aula 07 (abstração):** a interface define o comportamento, não a implementação.

### Exemplo 2 – Goroutine dona do estado e `select` ([hub.go](../internal/progress/hub.go), `Hub.Run`)

```go
func (h *Hub) Run(ctx context.Context) {
	defer close(h.stopped)
	subs := make(map[chan Event]struct{})
	for {
		select {
		case <-ctx.Done():
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
```

O mapa `subs` é uma variável local de `Run`: só esta goroutine o enxerga, então não há mutex. Quem quer se inscrever ou publicar não mexe no mapa, manda uma mensagem por channel. É o modelo CSP (Hoare, 1978) que o Go traz para a linguagem: *"não comunique compartilhando memória; compartilhe memória comunicando"*. O `select` espera o primeiro de quatro channels que ficar pronto; se vários estiverem prontos, sorteia um (por isso o `drain`: no Ctrl+C ainda podem existir eventos na fila). O CI roda os testes com o detector de corrida (`go test -race`), que acusaria qualquer acesso concorrente ao mapa.

**Aula 08 (controle de sequência):** `select` é um comando de seleção não determinístico, parecido com os comandos guardados de Dijkstra.

### Exemplo 3 – `defer`, retorno nomeado e erros como valores ([upload.go](../internal/server/upload.go), `save`)

```go
func (s *Server) save(id, name string, total int64, src io.Reader, wantHash string) (res UploadResult, err error) {
	// ...
	defer func() { // roda por último: decide o desfecho
		ev := progress.Event{ID: id, Name: res.Name, Bytes: res.Size, Total: total, Done: true}
		if err != nil {
			if path != "" {
				os.Remove(path) // não deixar arquivo pela metade
			}
			ev.Err = err.Error()
			s.failed.Add(1)
		} else {
			// ...
		}
		s.cfg.Hub.Publish(ev)
	}()

	f, err := storage.Create(s.cfg.Dir, name)
	if err != nil {
		return res, err
	}
	path = f.Name()
	defer func() { // registrado depois, roda ANTES: fecha o arquivo antes de apagá-lo
		if cerr := f.Close(); err == nil && cerr != nil {
			err = cerr
		}
	}()
	// ...
}
```

Go não tem exceções: o erro é um valor de retorno comum, e cada chamada que pode falhar é seguida de `if err != nil`. Os `defer` rodam quando a função termina, por qualquer `return`, em ordem inversa (pilha). A ordem importa aqui: o arquivo é fechado antes de ser apagado, porque o Windows não apaga arquivo aberto. Como os retornos têm nome (`res`, `err`), a closure do `defer` lê e até altera o resultado da função depois do `return`: se o `Close` falhar, `err` passa a ser esse erro.

**Aulas 08 e 09:** substituto de `try/finally` e de exceções; múltiplos valores de retorno.

### Exemplo 4 – Função que recebe e devolve função ([server.go](../internal/server/server.go), `requireToken`)

```go
func (s *Server) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.URL.Query().Get("t")
		if got == "" {
			got = r.Header.Get("X-Token")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.Token)) != 1 {
			http.Error(w, "token inválido", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// uso, em Handler:
mux.HandleFunc("POST /upload", s.requireToken(s.handleUpload))
```

Funções são valores de primeira classe. `requireToken` recebe um handler e devolve outro, que "lembra" de `next` e de `s` mesmo depois que `requireToken` já retornou: isso é uma closure. Com ela a checagem do token é escrita uma vez e aplicada a cada rota. `s.handleUpload` também é um valor: um *method value*, o método já preso ao receptor `s`. A comparação usa `crypto/subtle` porque um `==` comum para no primeiro caractere diferente, e o tempo de resposta vazaria quantos caracteres estão certos.

**Aula 09 (subprogramas):** subprogramas como parâmetros e como resultado; ambiente de referência da closure.

### Exemplo 5 – Diretivas do compilador e compilação cruzada ([embed.go](../web/embed.go), [console_other.go](../cmd/qrdrop/console_other.go), [build.sh](../scripts/build.sh))

```go
// web/embed.go
//go:embed index.html
var Files embed.FS
```

```go
// cmd/qrdrop/console_other.go
//go:build !windows

package main

// ...

// waitIfOwnConsole só faz algo no Windows (veja console_windows.go): no Linux
// e no macOS o programa roda num terminal que continua aberto depois.
func waitIfOwnConsole() {}
```

```bash
# scripts/build.sh (resumido): de um PC só saem os binários dos três sistemas
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o dist/qrdrop-windows-amd64.exe ./cmd/qrdrop
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -o dist/qrdrop-darwin-arm64     ./cmd/qrdrop
```

`//go:embed` manda o compilador copiar `index.html` para dentro do executável; a página é servida de `web.Files` e o binário roda sozinho, sem a pasta `web/` ao lado. `//go:build` decide na compilação qual arquivo entra: `console_windows.go` e `console_other.go` definem as mesmas funções, e só um deles é compilado em cada sistema. Por fim, o compilador do Go gera código nativo para qualquer sistema a partir de qualquer outro, só trocando `GOOS`/`GOARCH`; com `CGO_ENABLED=0` o binário não depende de nenhuma biblioteca do sistema. É assim que o [build.sh](../scripts/build.sh) gera, numa máquina Linux, os executáveis de Windows e macOS.

**Aula 05 (tradução):** compilação (não interpretação) para código de máquina; ligação estática; compilação cruzada.

## 5. Pontos fracos e críticas (recomendado)

Uma análise honesta vale mais que propaganda.
- [ ] Verbosidade do tratamento de erro
- [ ] Generics tardios e limitados
- [ ] Garbage collector (latência) vs. Rust/C
- [ ] Ausência de enums ricos e de tipos soma
