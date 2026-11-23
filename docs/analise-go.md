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
- **Abstração (aula 07):** interfaces, encapsulamento por pacote
- **Controle de sequência (aula 08):** `select`, `defer`, ausência de exceções
- **Subprogramas (aula 09):** passagem de parâmetros por valor (inclusive de ponteiros), closures, goroutines como subprogramas concorrentes
- **Tradução (aula 05):** compilação para código nativo, binário estático, compilação cruzada

## 4. Exemplos de código feitos pelo grupo (1,0)

- [ ] 3 a 5 trechos curtos e comentados, cada um mostrando um recurso diferente
- [ ] Sugestões: handler de upload com `io.Copy` em streaming; hub de progresso com `select`; geração de token; `embed.FS`

## 5. Pontos fracos e críticas (recomendado)

Uma análise honesta vale mais que propaganda.
- [ ] Verbosidade do tratamento de erro
- [ ] Generics tardios e limitados
- [ ] Garbage collector (latência) vs. Rust/C
- [ ] Ausência de enums ricos e de tipos soma
