# Arquitetura

## Estrutura de pastas

```
cmd/qrdrop/        main: flags (tipos próprios em flags.go), inicialização, QR, resumo ao sair
internal/server/   server.go (rotas, token, /info, estatísticas), upload.go, events.go (SSE), download.go
internal/progress/ hub de progresso (channel + goroutine única) e Reporter (linhas no terminal do PC)
internal/client/   `qrdrop send`: envio PC → PC com pool de goroutines
internal/share/    catálogo de arquivos que o celular pode baixar (-share)
internal/netutil/  descoberta do IP local
internal/storage/  nomes seguros, gravação em disco
web/               index.html, JS e CSS da página mobile (embutidos no binário)
scripts/           build.sh (binários para todos os sistemas)
docs/              documentação e análise da linguagem
```

`internal/` é uma convenção do próprio compilador Go: esses pacotes não podem ser importados por outros módulos. Também é um bom exemplo para a seção de abstração/encapsulamento.

## Fluxo

1. `main` lê as flags (`-dir`, `-port`, `-max-size`, `-share`, `-ttl`, `-ip`...), cria a pasta de destino e gera um **token aleatório** (`crypto/rand`).
2. Descobre o IP da máquina na rede local e monta `http://IP:PORTA/?t=TOKEN`.
3. Imprime o QR code dessa URL no terminal.
4. Sobe o servidor HTTP. **Cada requisição roda na própria goroutine**: isso é do `net/http`, não escrevemos nada para isso.
5. O celular abre a página, escolhe os arquivos, e o JS envia **cada arquivo numa requisição separada, em paralelo**.
6. O handler de upload lê o corpo em streaming (`multipart.Reader` + `io.Copy`), sem carregar o arquivo inteiro na memória, e publica o progresso num channel.
7. Um **hub de progresso** (uma goroutine) recebe os eventos do channel e repassa para todos os inscritos: as páginas conectadas em `/events` (SSE) e o `Reporter`, que escreve no terminal do PC "recebendo..." e "OK ... em 2.8s (1.0 MB/s)".
8. No Ctrl+C, ou quando o `-ttl` vence (`context.WithDeadline`), o servidor para de aceitar conexões, espera até 5 s os envios em andamento, e o `main` imprime o resumo lido das estatísticas.

## Envio pelo terminal (`qrdrop send`)

Para mandar de um PC para outro sem navegador. Um pool de N goroutines (`-p`, padrão 4) consome um channel de tarefas; cada uma calcula o SHA-256 local, envia o arquivo em streaming (`io.Pipe` + `multipart.Writer`) e compara o hash devolvido pelo receptor. O progresso de todas vai por um channel para uma goroutine que desenha a linha no terminal.

## Endpoints

| Método | Rota | Descrição |
|---|---|---|
| GET | `/?t=TOKEN` | Página de upload (servida de `embed.FS`) |
| GET | `/info?t=TOKEN` | JSON com limite de tamanho, validade do link, arquivos para baixar e estatísticas |
| POST | `/upload?t=TOKEN` | Recebe **um** arquivo por requisição (multipart): a primeira parte do formulário que traz arquivo; campos de texto antes dela são ignorados. Cabeçalhos opcionais: `X-Upload-ID`, `X-File-Size` (acima do `-max-size` recusa na hora com 413), `X-SHA256` (64 caracteres hexadecimais, maiúsculos ou minúsculos; malformado recusa na hora com 400; se não bater, responde 422 e apaga o arquivo) |
| GET | `/events?t=TOKEN` | Stream SSE de progresso, com um "ping" a cada 20 s |
| GET | `/download/{nome}?t=TOKEN` | Baixa um arquivo compartilhado com `-share`. Aceita `Range` (download retomável) |
| GET | `/healthz` | Verificação simples |

Requisições sem token válido recebem `401`. Toda resposta leva `X-Content-Type-Options: nosniff` (um `.html` compartilhado é baixado, nunca executado) e `Referrer-Policy: no-referrer` (o token da URL nunca vai para outro site).

Nomes de arquivo vindos da rede passam por `storage.SafeName`: sem pastas (`../`), sem caracteres proibidos no Windows, sem caracteres de controle (inclusive os C1, que alguns terminais executam) e sem marcas de direção do texto (com U+202E, `fatura\u202Egpj.exe` apareceria como `faturaexe.jpg`).

## Modelo de concorrência

```
 requisição A ─▶ goroutine A ─┐
 requisição B ─▶ goroutine B ─┼──▶ chan Progress ──▶ goroutine hub ──▶ clientes SSE
 requisição C ─▶ goroutine C ─┘
```

- Os produtores (uploads) não conhecem os consumidores (telas): o channel desacopla os dois.
- O hub é o único dono do mapa de clientes, então ele não precisa de mutex. É o lema do Go: *"Don't communicate by sharing memory; share memory by communicating."*
- Escrita em disco: cada arquivo tem nome único (`O_EXCL`), então não há disputa entre goroutines.
- **Semáforo com channel:** um channel com buffer de N posições (`-max-uploads`, padrão 8) limita quantos arquivos gravam ao mesmo tempo. Quem chega com tudo ocupado espera no `select`, ou desiste se o cliente desconectar.
- **Estatísticas com `sync/atomic`:** contadores somados por várias goroutines sem mutex. Contraste útil para a análise: o hub usa channels, as estatísticas usam memória compartilhada atômica, cada um onde faz mais sentido.
- **Inscrito lento não trava ninguém:** evento de progresso com a fila do inscrito cheia é descartado (o próximo traz o número atualizado); o evento final espera até 50 ms. Ao parar, o hub ainda entrega o que estava na fila antes de fechar os inscritos.
- **Desligamento:** `RegisterOnShutdown` fecha os streams SSE, que nunca terminariam sozinhos. Envios que não terminam em 5 s têm a conexão derrubada (`http.Server.Close`), e `Server.Wait` (um `sync.WaitGroup`) espera cada um apagar o arquivo pela metade antes de o programa sair. O hub só para depois disso, para o terminal ainda mostrar esses envios.

## Decisões e alternativas

| Decisão | Alternativa descartada | Motivo |
|---|---|---|
| SSE para progresso | WebSocket | SSE é unidirecional, sai da stdlib e basta para o caso |
| Um arquivo por requisição | Todos numa requisição | Paralelismo real e progresso individual |
| Token na URL | Login/senha | O QR já é o canal de autenticação |
| QR com cores fixas (ANSI) | Só os caracteres de bloco | Em terminal de fundo claro o QR saía invertido e um leitor comum não lia |
| Porta livre qualquer se a 8080 estiver ocupada | Erro | O QR leva a porta certa; com `-port` explícito, o erro continua |
| Sem framework (Gin, Echo) | Gin | A stdlib basta e evidencia a linguagem |
| Semáforo com channel | `golang.org/x/sync/semaphore` | Mostra channel como ferramenta de sincronização, sem dependência |
| `sync/atomic` nas estatísticas | Mutex | Só somas e leituras: atômico é mais simples e mais rápido |
| `filename*` (RFC 6266) no download | Só `filename` | Nomes com acento ("apresentação.pdf") chegam certos no celular |

## Dependências externas

- Geração de QR no terminal: [`github.com/mdp/qrterminal/v3`](https://github.com/mdp/qrterminal) (a confirmar na issue correspondente)
- O resto é biblioteca padrão.
