# ⚽ Copa de Figurinhas

Álbum de figurinhas virtual para um evento local (Copa 2026, com família e amigos).
Cada convidado recebe uma figurinha física com a própria foto e um **QR code**. Escanear
os QRs dos outros monta um **álbum virtual** em tempo real, com **telão** de ranking ao vivo
e **tela de campeão** no apito inicial.

Roda **100% local** num notebook Windows, **sem internet** — um único binário (`copa.exe`)
com banco SQLite embutido.

## Como funciona

1. O convidado escaneia o QR da própria figurinha → confirma a identidade ("Você é X?").
2. A partir daí, escaneia as figurinhas dos outros → o álbum cresce ao vivo.
3. O telão (projetor) mostra o ranking ao vivo e a contagem regressiva pro apito.
4. No apito, a classificação **congela** e o **campeão** é anunciado.

**Regra de vitória:** quem completar o álbum primeiro vence; se ninguém completar até o
apito, vence quem tiver mais figurinhas (desempate por quem chegou primeiro ao número).

## Funcionalidades

- **Jogo via QR:** identidade no 1º scan, recuperação re-escaneando a própria figurinha,
  "trocar de jogador" para aparelhos compartilhados.
- **Telão (`/tv`):** ranking ao vivo por SSE, contagem regressiva, indicador de conexão,
  pódio top-3 e confete na tela de campeão.
- **Painel admin (`/admin`):**
  - Participantes (CRUD) e **importação em massa** por CSV + fotos.
  - **QR Sheet** e **cartões para impressão** (frente foto + verso QR).
  - **Diagnóstico de rede** (detecta os IPs do notebook e valida o `base_url`).
  - **Backup/restauração** do banco e **export CSV** do resultado.
  - **Trava de elenco**, horário do apito e **reset** de dados de teste.
- **Confiabilidade:** sessões de admin seguras (token no servidor), backup atômico
  (`VACUUM INTO`) + periódico, shutdown gracioso com checkpoint do WAL, SSE com heartbeat e
  reenvio de snapshot, redimensionamento de imagens no upload.

## Stack

- **Go** (single-binary, `embed.FS` para templates/estáticos)
- **SQLite** via `modernc.org/sqlite` (puro Go, sem CGO), modo WAL
- **SSE** (Server-Sent Events) para o ranking em tempo real
- QR via `skip2/go-qrcode`; imagens via `golang.org/x/image/draw`

## Build e execução

Requer Go 1.25+.

```bash
# compilar
go build -o copa.exe ./cmd/copa

# rodar (porta 8080)
./copa.exe
```

Acesse:
- Convidados: `http://<ip-do-notebook>:8080/`
- Telão: `http://<ip-do-notebook>:8080/tv`
- Admin: `http://<ip-do-notebook>:8080/admin`

> A senha do admin é definida no 1º login. A variável `COPA_ADMIN_PASSWORD`, se definida,
> **sobrescreve** a senha salva a cada inicialização — defina-a apenas se quiser fixar a senha
> por fora; caso contrário, configure pelo painel.

## Testes

```bash
go test ./...
go vet ./...
```

## Estrutura

```
cmd/copa/            # main + templates/estáticos embutidos (web/)
internal/
  db/                # abertura do SQLite + migrações
  service/           # store: toda a lógica de dados (ranking, winner, backup/restore...)
  handler/           # HTTP: sticker/álbum/telão/admin/SSE
  qr/                # geração de QR
  sse/               # hub de Server-Sent Events
  model/             # structs de domínio
OPERACAO.md          # guia de operação e checklist do dia do evento
```

## Operação do evento

O passo a passo de rede, impressão dos QRs, ensaio e checklist do dia está em
**[OPERACAO.md](OPERACAO.md)**.
