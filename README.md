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

## Download

Baixe o binário do seu sistema na página de [Releases](https://github.com/aarthurv77/qrdrop/releases) e rode no terminal. Não é preciso instalar Go nem nada além dele.

| Sistema | Arquivo |
|---|---|
| Windows | `qrdrop-windows-amd64.exe` (ARM: `qrdrop-windows-arm64.exe`) |
| Linux | `qrdrop-linux-amd64` (ARM: `qrdrop-linux-arm64`) |
| macOS | `qrdrop-darwin-arm64` (Apple Silicon) ou `qrdrop-darwin-amd64` (Intel) |

No Linux e no macOS, dê permissão de execução antes: `chmod +x qrdrop-*`. O `SHA256SUMS.txt` da Release serve para conferir o download (`sha256sum -c SHA256SUMS.txt`).

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

### Publicar uma versão

```bash
git tag v1.0.0
git push origin v1.0.0
```

A tag dispara o workflow [`release.yml`](.github/workflows/release.yml): os testes rodam em Windows, Linux e macOS e, se passarem, o `scripts/build.sh` gera os binários, que vão para uma Release nova junto com o `SHA256SUMS.txt`.

## Documentação

| Documento | Conteúdo |
|---|---|
| [docs/visao.md](docs/visao.md) | Problema, escopo, o que **não** faremos |
| [docs/arquitetura.md](docs/arquitetura.md) | Componentes, fluxo, endpoints, modelo de concorrência |
| [docs/analise-go.md](docs/analise-go.md) | Análise da linguagem Go (base do vídeo) |
| [docs/planejamento.md](docs/planejamento.md) | Papéis, marcos, critérios de avaliação |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Fluxo de branches, PRs e commits |

## Equipe

| Integrante | Papel principal |
|---|---|
| _nome_ | Backend HTTP e arquivos |
| _nome_ | Concorrência, progresso e segurança |
| _nome_ | Frontend mobile e QR code |
| _nome_ | Análise da linguagem e vídeo |
| _nome_ | Build, CI, site e coordenação |

## Referências

- [croc](https://github.com/schollz/croc), transferência de arquivos em Go (inspiração; escopo bem maior que o nosso)
- [The Go Programming Language Specification](https://go.dev/ref/spec)
