# qrdrop

> Mande fotos e arquivos do celular para o PC escaneando um QR code. Sem cabo, sem WhatsApp, sem nuvem.

Projeto final da disciplina **CIC0093 – Linguagens de Programação** (UnB, 2026/2, Prof. Marcelo Ladeira).
Linguagem do projeto: **Go**.

## Como funciona

```
┌──────────────┐   1. roda `qrdrop`        ┌──────────────────────┐
│      PC      │ ───────────────────────▶  │ servidor HTTP local  │
│  (terminal)  │   2. mostra QR code        │ http://192.168.x.y:8080/?t=TOKEN
└──────────────┘                            └──────────▲───────────┘
                                                       │ 4. upload (multipart, em paralelo)
┌──────────────┐   3. escaneia o QR e abre a página    │
│   Celular    │ ──────────────────────────────────────┘
│ (navegador)  │   5. vê o progresso de cada arquivo em tempo real
└──────────────┘
```

O celular não instala nada: usa o navegador. O PC roda um único binário, que já traz a página web dentro dele.

## Uso

```bash
qrdrop                         # recebe: salva em ./recebidos, porta 8080, mostra o QR
qrdrop -dir ~/Fotos -port 9000
qrdrop -max-size 2GB -ttl 30m  # limite por arquivo e link que expira em 30 min
qrdrop -share slides.pdf -share ~/Musicas   # o celular também pode BAIXAR esses arquivos
qrdrop -ip 192.168.0.12        # força o IP do QR (quando o PC tem várias redes)

# de outro PC, pelo terminal: envia vários arquivos em paralelo
qrdrop send "http://192.168.0.12:8080/?t=TOKEN" video.mp4 fotos/*.jpg
qrdrop send -p 8 URL arquivos...   # 8 envios ao mesmo tempo (padrão: 4)
```

Celular ou outro computador também podem abrir a URL do QR no navegador e arrastar os arquivos. Em todos os casos cada arquivo vai numa requisição separada, em paralelo, e o servidor calcula o SHA-256 enquanto grava; o `qrdrop send` confere esse hash com o do arquivo original. Na página dá para cancelar e tentar de novo cada arquivo.

`qrdrop -h` lista todas as opções. No Ctrl+C (ou quando o `-ttl` vence) o qrdrop espera os envios em andamento terminarem e mostra um resumo.

**Dicas para a demo:** no Windows, permita o qrdrop no firewall na primeira vez (inclusive em redes públicas). Wi-Fi de faculdade costuma bloquear um aparelho de falar com outro: o mais seguro é ligar o roteador do celular e conectar o notebook nele.

No iPhone, se o Safari disser que o pedido era para "uma URL http com apenas https ativado", desligue Ajustes > Apps > Safari > Aviso de Conexão Não Segura (ou abra o endereço no Chrome ou no Firefox). O qrdrop usa http, e esse ajuste faz o Safari recusar qualquer endereço http.

## Desenvolvimento

Requisitos: Go 1.22+.

```bash
go run ./cmd/qrdrop      # roda
go test ./...            # testes (inclui um de ponta a ponta que compila o qrdrop)
go test -short ./...     # o mesmo, sem o teste de ponta a ponta
go vet ./...             # análise estática
go run golang.org/x/vuln/cmd/govulncheck@latest ./...   # vulnerabilidades conhecidas nas dependências
go test -race ./...      # testes com o detector de condições de corrida
go test -run=^$ -fuzz=FuzzSafeName ./internal/storage   # fuzzing dos nomes de arquivo
go test -run=^$ -bench=. ./internal/server              # vazão do upload em MB/s
./scripts/build.sh v1.0.0  # binários para Windows, Linux e macOS em dist/
```

## Documentação

| Documento | Conteúdo |
|---|---|
| [docs/visao.md](docs/visao.md) | Problema, escopo, o que **não** faremos |
| [docs/arquitetura.md](docs/arquitetura.md) | Componentes, fluxo, endpoints, modelo de concorrência |
| [docs/analise-go.md](docs/analise-go.md) | Análise da linguagem Go (base do vídeo) |
| [docs/planejamento.md](docs/planejamento.md) | Papéis, marcos, critérios de avaliação |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Fluxo de branches, PRs e commits |

## Equipe

| Integrante | Papel principal | Issues |
|---|---|---|
| Hendrick ([@Hendrickhmq](https://github.com/Hendrickhmq)) | Backend, concorrência e frontend | #2 servidor, #3 upload, #4 IP local, #5 QR code, #6 página mobile, #8 token, #9 hub de progresso, #11 SSE, #12 nomes seguros, #13 embed, #14 testes com httptest, #16 Ctrl+C, #23 PC → celular |
| Gustavo Choueiri | Testes, build/CI e análise da linguagem | #1 tabela da equipe no README, #3 teste com arquivo de 150 MB, #9 teste com 50 uploads simultâneos, #15 releases multiplataforma, #19 construtores e exemplos de código |
| Artur | Estrutura do projeto, CI e coordenação | #1 repositório, `go.mod`, CI, documentação inicial e criação das issues e marcos |
| Gabriel | _a definir_ | |
| Antonio | _a definir_ | |

Algumas issues têm mais de um autor: na #1, Artur montou o repositório e Gustavo preencheu a tabela da equipe; na #3 e na #9, Hendrick implementou o upload e o hub, e Gustavo escreveu os testes que faltavam para cumprir os critérios de aceite. O histórico de issues e PRs é o registro detalhado do que cada um fez.

## Referências

- [croc](https://github.com/schollz/croc), transferência de arquivos em Go (inspiração; escopo bem maior que o nosso)
- [The Go Programming Language Specification](https://go.dev/ref/spec)
