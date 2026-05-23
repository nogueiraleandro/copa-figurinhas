# Copa de Figurinhas — Guia de Operação

> Evento: **13 de junho de 2026** · ~50 convidados · Servidor local, sem internet

---

## Índice

1. [Visão geral rápida](#1-visão-geral-rápida)
2. [Pré-requisitos](#2-pré-requisitos)
3. [Configuração da rede](#3-configuração-da-rede)
4. [Primeira execução e senha do admin](#4-primeira-execução-e-senha-do-admin)
5. [Painel admin — fluxo completo](#5-painel-admin--fluxo-completo)
6. [Impressão dos QR codes](#6-impressão-dos-qr-codes)
7. [Ensaio / dry-run (fazer ANTES do evento)](#7-ensaio--dry-run-fazer-antes-do-evento)
8. [Checklist do dia do evento](#8-checklist-do-dia-do-evento)
9. [Durante o jogo](#9-durante-o-jogo)
10. [Procedimentos de recuperação](#10-procedimentos-de-recuperação)
11. [Pós-evento / backup final](#11-pós-evento--backup-final)
12. [Referência rápida de URLs](#12-referência-rápida-de-urls)

---

## 1. Visão geral rápida

```
copa.exe                  ← binário único, ~18 MB
  ├── data/copa.db        ← banco SQLite (criado na 1ª execução)
  └── uploads/            ← fotos dos participantes
```

**Fluxo do convidado:**

1. Recebe a figurinha física com QR code impresso.
2. Escaneia o próprio QR → confirma identidade na tela.
3. Escaneia figurinhas de outros → álbum virtual cresce em tempo real.
4. Telão (projetor) exibe ranking ao vivo.
5. No apito final → ranking congela, campeão é anunciado.

---

## 2. Pré-requisitos

| Item | Detalhe |
|------|---------|
| Notebook Windows | Deve ficar ligado o evento inteiro |
| `copa.exe` | Já compilado em `C:\Users\leand\IdeaProjects\copa\copa.exe` |
| Roteador Wi-Fi dedicado | Preferível a depender da rede do local |
| IP fixo no notebook | Configurado via roteador (DHCP reservation) ou estático |
| Projetor / TV | Conectado ao notebook via HDMI |
| Impressora | Para as figurinhas com QR |
| Figurinhas impressas | Envelope com a figurinha de cada convidado |

---

## 3. Configuração da rede

### 3.1 IP fixo no notebook (recomendado)

No roteador, reserve o IP `192.168.0.50` para o MAC address do notebook
(geralmente chamado de "DHCP Reservation" ou "Static Lease" nas configurações).

Alternativamente, configure IP estático no Windows:
```
Painel de Controle → Rede → Adaptador Wi-Fi → Propriedades → IPv4
  IP: 192.168.0.50
  Máscara: 255.255.255.0
  Gateway: 192.168.0.1
  DNS: 192.168.0.1
```

### 3.2 Regra de firewall (abrir porta 8080)

Execute como **Administrador** no PowerShell:

```powershell
New-NetFirewallRule -DisplayName "Copa Figurinhas" `
  -Direction Inbound -Protocol TCP -LocalPort 8080 -Action Allow
```

Verificar se está aberta:
```powershell
Get-NetFirewallRule -DisplayName "Copa Figurinhas" | Select-Object Enabled
```

---

## 4. Primeira execução e senha do admin

### 4.1 Executar o servidor

Abra o **Prompt de Comando** (ou PowerShell) e rode:

```cmd
cd C:\Users\leand\IdeaProjects\copa
set COPA_ADMIN_PASSWORD=minha_senha_secreta
copa.exe
```

O servidor sobe na porta **8080** por padrão.

> **Dica:** Crie um arquivo `iniciar.bat` na pasta do projeto para não ter que digitar toda vez:
> ```bat
> @echo off
> set COPA_ADMIN_PASSWORD=minha_senha_secreta
> cd /d C:\Users\leand\IdeaProjects\copa
> copa.exe
> pause
> ```

### 4.2 Primeiro login no admin

1. Abra `http://localhost:8080/admin` no navegador do notebook.
2. Digite a senha definida em `COPA_ADMIN_PASSWORD`.
3. Após o login, configure a **URL base** (veja abaixo).

### 4.3 Configurar a URL base ⚠️ CRÍTICO — fazer ANTES de imprimir

Em **Admin → Config**, defina:

```
http://192.168.0.50:8080
```

(sem barra final, com o IP real do notebook na sua rede)

> ⚠️ Esta URL é embutida nos QR codes. Se mudar depois de imprimir, os QRs ficam inválidos.
> **Configure antes de gerar a QR Sheet.**

---

## 5. Painel admin — fluxo completo

### 5.1 Importar participantes em massa

**Admin → Importar** — submeter um CSV + as fotos dos participantes:

```
CSV formato:
name,nickname,image
Leandro Menezes,Leo,leandro.png
Ana Beatriz,Aninha,ana.png
Carlos,Carlão,
```

- A coluna `image` é opcional; deixe em branco se não tiver foto.
- Envie as imagens junto no mesmo formulário (campo "Imagens").
- As fotos são redimensionadas para máx 600 px automaticamente.

### 5.2 Conferir os participantes

**Admin → Participantes** — lista com foto, nome e apelido.
- Editar nome/apelido individualmente se necessário.
- Desativar participantes que não compareceram (não afeta o álbum de ninguém).

### 5.3 Gerar a QR Sheet

**Admin → QR Sheet** — exibe uma folha HTML com o QR de cada participante.
- Use `Ctrl+P` → "Salvar como PDF" ou imprimir direto.
- Cada QR leva para `http://192.168.0.50:8080/s/<token>`.
- **Corte e cole o QR no verso da figurinha física.**

### 5.4 Travar o elenco (~30 min antes do apito)

**Admin → Dashboard → Travar elenco agora**

Após travado:
- Nenhum novo participante pode ser adicionado.
- O total de figurinhas fica fixo → "completar" tem um alvo definido.
- O ranking congelado no apito usará esse total.

Para destravar (se alguém chegar atrasado antes do apito):
**Admin → Dashboard → Destravar elenco**

### 5.5 Configurar o horário do apito

**Admin → Config → Horário do apito (kickoff_at)**

Coloque o horário exato do apito do jogo do Brasil (ex: `2026-06-13T21:00:00`).
O sistema:
- Mostra contagem regressiva no telão até esse horário.
- Ao chegar a zero, congela o ranking e exibe o campeão.

---

## 6. Impressão dos QR codes

### Fluxo recomendado

1. Cadastrar todos os participantes com foto.
2. Configurar `base_url` (IP:porta).
3. Abrir `/admin/qrsheet` → imprimir PDF.
4. Recortar cada QR.
5. Colar no verso da figurinha física (papel fotográfico, estilo Panini).
6. Colocar em envelopes individuais (cada pessoa recebe o próprio envelope).

### Figurinha com foto

Se usar papel fotográfico ou adesivo:
- Frente: foto + nome (estilo figurinha Panini).
- Verso: QR code (da QR Sheet).

Se quiser imprimir frente-e-verso numa folha só, use o "QR Sheet" e componha manualmente.

---

## 7. Ensaio / dry-run (fazer ANTES do evento)

Execute este roteiro **pelo menos 1 semana antes** (ou no dia anterior):

### Checklist do ensaio

- [ ] `copa.exe` sobe sem erro → `http://localhost:8080` carrega
- [ ] Login no admin funciona com a senha escolhida
- [ ] `base_url` configurada corretamente
- [ ] Importar CSV de teste com 3-5 participantes fictícios (você, familiar próximo)
- [ ] QR Sheet gerada e QR de teste escaneado com o celular
- [ ] Scan abre a página de confirmação corretamente
- [ ] "Sim, sou eu" cria o álbum e redireciona para `/album`
- [ ] Escanear o QR de outro → aparece no álbum
- [ ] Telão (`/tv`) abre e mostra ranking
- [ ] Ranking ao vivo atualiza após scan (sem recarregar a página)
- [ ] Contagem regressiva funciona (configure `kickoff_at` no passado para testar)
- [ ] Tela de campeão aparece quando `kickoff_at` está no passado
- [ ] Backup baixado via **Admin → Backup** é um arquivo SQLite válido
- [ ] Export CSV contém os participantes e colunas esperadas
- [ ] Celular consegue acessar `http://192.168.0.50:8080` pela rede local
- [ ] Múltiplos celulares simultâneos (peça 3-4 pessoas para scanear ao mesmo tempo)
- [ ] Limpar o banco de teste: parar o servidor, deletar `data/copa.db`, reiniciar

---

## 8. Checklist do dia do evento

### Manhã / antes dos convidados chegarem

- [ ] Ligar o notebook e conectar ao roteador dedicado
- [ ] Verificar IP: `ipconfig` → confirmar `192.168.0.50`
- [ ] Iniciar `copa.exe` (via `iniciar.bat` ou terminal)
- [ ] Login no admin: `http://localhost:8080/admin`
- [ ] Conferir `base_url` no admin (⚠️ não alterar se já imprimiu QRs)
- [ ] Importar participantes finais via CSV (se ainda não feito)
- [ ] Verificar lista em **Admin → Participantes** (nomes, fotos, total)
- [ ] Conectar projetor/TV via HDMI
- [ ] Abrir `http://localhost:8080/tv` em modo tela cheia no projetor (F11)
- [ ] Configurar `kickoff_at` com o horário exato do apito do Brasil
- [ ] Confirmar que a contagem regressiva está correta no telão
- [ ] Fazer backup inicial: **Admin → Backup** → salvar em pendrive

### ~30 minutos antes do apito

- [ ] **Admin → Dashboard → Travar elenco** (confirmar travamento)
- [ ] Conferir **Admin → Dashboard** → cartão "Ainda não entraram"
- [ ] Abrir `http://localhost:8080/admin/dashboard` em outra aba para monitorar
- [ ] Fazer backup de segurança: **Admin → Backup** → salvar em pendrive

### No apito

- [ ] Telão muda automaticamente para a tela de campeão (sem ação necessária)
- [ ] Se não mudar em até 30 segundos: recarregar `http://localhost:8080/tv`
- [ ] Comemorar! 🏆

---

## 9. Durante o jogo

### Monitoramento pelo admin

| Página | O que acompanhar |
|--------|-----------------|
| `/admin/dashboard` | Ranking, quem ainda não entrou, status do elenco |
| `/tv` | Telão — confira se está atualizando |
| `/admin/participants` | Adicionar latecomer se necessário (antes de travar) |

### Suporte ao convidado

**"Não consigo escanear"**
- Verificar se o celular está conectado à rede certa (Wi-Fi do evento).
- Tentar abrir `http://192.168.0.50:8080` no navegador manualmente.
- Verificar se a câmera nativa do celular lê QR (ou usar Google Lens).

**"Escaneei e não abriu"**
- Verificar se a URL no QR bate com o IP/porta do servidor.
- Confirmar que `copa.exe` está rodando (terminal aberto, sem erros).

---

## 10. Procedimentos de recuperação

### Convidado perdeu o cookie / trocou de celular

O cookie identifica o dispositivo. Se o convidado trocar de celular ou limpar o browser:

1. Peça para ele escanear o **próprio QR code** (a figurinha dele).
2. A tela de confirmação "Você é X?" aparece.
3. Confirmar → o álbum é recuperado no novo celular.

> O álbum e a coleção ficam intactos; apenas a sessão é re-associada ao novo dispositivo.

### Participante cadastrado com nome errado

**Admin → Participantes → Editar** → corrigir nome/apelido.
A correção reflete imediatamente em toda a interface.

### Participante que não foi / desistiu

**Admin → Participantes → Desativar**

- O participante desativado **sai do álbum** de todos.
- O progresso de quem tinha ele na coleção é ajustado automaticamente.
- **Não desative após travar o elenco** (pode afetar o resultado final).

### Servidor travou / notebook reiniciou

1. Reiniciar `copa.exe` (via `iniciar.bat`).
2. O banco SQLite (`data/copa.db`) está intacto — todos os dados preservados.
3. Os convidados reconectam normalmente; o SSE do telão reconecta automaticamente.

### Banco corrompido (improvável, mas possível com queda de luz)

SQLite em modo WAL é resiliente, mas se o banco abrir com erro:

1. Parar o servidor.
2. Restaurar o último backup: copiar o `.db` do backup para `data/copa.db`.
3. Reiniciar o servidor.

> **Por isso faça backups frequentes no dia!** (Admin → Backup a cada 30 min)

### Projetor / TV parou de atualizar

A página `/tv` reconecta o SSE automaticamente em caso de queda de rede.
Se ficar parada por mais de 1 minuto:
- Recarregar `http://localhost:8080/tv` no browser do projetor.
- O ranking atual é enviado imediatamente ao reconectar.

---

## 11. Pós-evento / backup final

1. **Admin → Backup** → salvar cópia final em pendrive.
2. **Admin → Export (CSV)** → salvar resultado oficial em CSV.
3. Parar o servidor (Ctrl+C no terminal) → o WAL é checkpointed automaticamente.
4. Copiar a pasta inteira `data/` e `uploads/` para backup externo.

---

## 12. Referência rápida de URLs

| URL | Descrição |
|-----|-----------|
| `http://192.168.0.50:8080/` | Página inicial (convidados) |
| `http://192.168.0.50:8080/admin` | Login do admin |
| `http://192.168.0.50:8080/admin/dashboard` | Dashboard com ranking e status |
| `http://192.168.0.50:8080/admin/participants` | Gerenciar participantes |
| `http://192.168.0.50:8080/admin/bulk` | Importar CSV + fotos |
| `http://192.168.0.50:8080/admin/qrsheet` | Gerar folha de QR codes |
| `http://192.168.0.50:8080/admin/settings` | Configurações (base_url, kickoff) |
| `http://192.168.0.50:8080/admin/backup` | Baixar backup SQLite |
| `http://192.168.0.50:8080/admin/export` | Exportar resultado CSV |
| `http://192.168.0.50:8080/tv` | Telão (projetor) — ranking ao vivo |
| `http://192.168.0.50:8080/album` | Álbum do convidado (depois de registrado) |

> Substitua `192.168.0.50` pelo IP real do notebook na sua rede, se diferente.

---

*Boa Copa! 🇧🇷⚽🏆*
