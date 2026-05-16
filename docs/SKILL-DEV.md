---
name: loghub_ident
description: Especificação e guia de implementação da biblioteca Go lib-loghub-ident (pacote loghub_ident), que preenche a identidade de um software (DATADIR, MACHINE_ID, AGENT_NAME, AGENT_UUID, HOSTNAME, WORKSPACE) para uso em logs, eventos e mensageria. USE ESTA SKILL SEMPRE QUE o usuário pedir para criar, alterar, depurar, revisar ou testar essa biblioteca, OU quando solicitar qualquer função Go que leia/valide essas seis variáveis a partir de env e arquivos. Cobre precedência (env→arquivo→fallback), regex de validação por variável, códigos de saída 100–112, regras de imutabilidade e implementação de referência. UUID v4 gerado via crypto/rand. Arquivo de machine-id configurável via MACHINE_ID_FILE.
---

# loghub_ident — identidade de agente para microserviços Loghub

Esta skill define como implementar o pacote Go `loghub_ident`. A biblioteca é carregada uma única vez na inicialização do software (`Initialize()`) e expõe os seis valores de identidade em um singleton imutável (`ID`).

## Quando essa skill se aplica

- Usuário mencionou `lib-loghub-ident`, `loghub_ident`, "identidade do agente" ou "identidade do remetente".
- Tarefa pede para implementar/alterar/testar leitura de `DATADIR`, `MACHINE_ID`, `AGENT_NAME`, `AGENT_UUID`, `HOSTNAME` ou `WORKSPACE` em Go.
- Qualquer pergunta sobre os códigos de saída 100–112 dessa biblioteca.

## Contrato público da API

- Pacote: `loghub_ident`
- Sem dependências externas. Apenas stdlib.
- Versão mínima do Go: **1.21**.
- Multiplataforma (UUID gerado via `crypto/rand`; arquivo de machine-id configurável via env `MACHINE_ID_FILE`, padrão `/etc/machine-id`).

```go
package loghub_ident

// Identity contém os seis valores de identidade do agente.
// Todos em lowercase. Imutável após Initialize.
type Identity struct {
    DataDir   string // diretório existente; ex.: "/data"
    MachineID string // 32 hex chars [0-9a-f], sem hífen
    AgentName string // [a-z0-9._-]+
    AgentUUID string // UUID v4 canônico
    Hostname  string // [a-z0-9.-]+
    Workspace string // [a-z0-9.-]+
}

// ID é o singleton populado por Initialize.
// Antes de Initialize é nil. Após Initialize, nunca é reatribuído nem mutado.
var ID *Identity

// Initialize popula ID lendo env, arquivos e fallbacks.
// Em qualquer falha: escreve no stderr e chama os.Exit com o código específico.
// Chamar duas vezes encerra com código 112.
func Initialize()
```

Uso típico:

```go
package main

import (
    "fmt"
    "github.com/<org>/lib-loghub-ident/loghub_ident"
)

func main() {
    loghub_ident.Initialize() // encerra o processo se algo falhar
    fmt.Println("agent:", loghub_ident.ID.AgentName, loghub_ident.ID.AgentUUID)
}
```

## As seis variáveis (precedência e validação)

Precedência sempre: **(1) variável de ambiente → (2) arquivo de disco → (3) fallback do sistema**, e em cada fonte: **ler primeira linha → trim de whitespace → forçar lowercase → sanear conforme a variável → validar regex final**.

| Variável     | Env           | Arquivo de fallback             | Fallback final                                                            | Regex final                       | Códigos       |
|--------------|---------------|---------------------------------|---------------------------------------------------------------------------|-----------------------------------|---------------|
| `DATADIR`    | `DATADIR`     | —                               | `/data`                                                                   | (diretório existente)             | 100           |
| `MACHINE_ID` | `MACHINE_ID`  | `$MACHINE_ID_FILE` (padrão `/etc/machine-id`) | —                                                             | `^[0-9a-f]{32}$`                  | 101, 102      |
| `AGENT_NAME` | `AGENT_NAME`  | `$DATADIR/agent_name`           | `filepath.Base(os.Args[0])` saneado                                       | `^[a-z0-9._-]+$`                  | 103, 104      |
| `AGENT_UUID` | `AGENT_UUID`  | `$DATADIR/agent_uuid`           | gerar via `crypto/rand` e **gravar** em `$DATADIR/agent_uuid` (perm 0644) | UUID v4 canônico (regex abaixo)   | 105, 106, 107 |
| `HOSTNAME`   | `HOSTNAME`    | —                               | `os.Hostname()`                                                            | `^[a-z0-9.-]+$`                   | 108, 109      |
| `WORKSPACE`  | `WORKSPACE`   | `$DATADIR/workspace`            | — (obrigatório; sem fallback)                                              | `^[a-z0-9.-]+$`                   | 110, 111      |

Regex de UUID v4 canônico:
```
^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$
```

Para `MACHINE_ID`, antes de validar o regex: remover qualquer `-` e aplicar `ToLower`. Isso aceita transparentemente a forma com hífens de UUID e a forma canônica de `/etc/machine-id`.

## Códigos de saída

Em qualquer falha, escrever no stderr (sem cor, sem timestamp):
```
lib-loghub-ident: <VARIÁVEL>: <motivo>
```
e em seguida `os.Exit(<código>)`.

| Código | Variável     | Motivo                                                                                  |
|--------|--------------|-----------------------------------------------------------------------------------------|
| 100    | `DATADIR`    | diretório informado (env ou `/data`) não existe ou não é um diretório                   |
| 101    | `MACHINE_ID` | env vazio e arquivo de machine-id (`$MACHINE_ID_FILE`) ausente ou ilegível              |
| 102    | `MACHINE_ID` | valor obtido não casa com `^[0-9a-f]{32}$` após remover `-` e aplicar lowercase         |
| 103    | `AGENT_NAME` | env vazio, arquivo `$DATADIR/agent_name` ausente/vazio e `argv[0]` saneado ficou vazio  |
| 104    | `AGENT_NAME` | valor obtido não casa com `^[a-z0-9._-]+$`                                              |
| 105    | `AGENT_UUID` | falha na geração local de UUID v4 via `crypto/rand`                                     |
| 106    | `AGENT_UUID` | gravação em `$DATADIR/agent_uuid` (perm 0644) falhou                                    |
| 107    | `AGENT_UUID` | valor obtido não casa com regex de UUID v4 canônico                                     |
| 108    | `HOSTNAME`   | env vazio e `os.Hostname()` retornou erro                                               |
| 109    | `HOSTNAME`   | valor obtido não casa com `^[a-z0-9.-]+$` após lowercase                                |
| 110    | `WORKSPACE`  | env vazio e `$DATADIR/workspace` ausente ou vazio                                       |
| 111    | `WORKSPACE`  | valor obtido não casa com `^[a-z0-9.-]+$`                                               |
| 112    | (geral)      | `Initialize()` chamado mais de uma vez                                                   |

## Regras críticas (não negociáveis)

1. **Chamada única.** `Initialize()` só pode ser chamado uma vez. Segunda chamada → código 112. Implementar com guarda no início: `if ID != nil { fatal(112, ...) }`.
2. **Imutabilidade.** Após `Initialize()`, nenhum campo de `ID` muda. Sem setters, sem mutex, sem ponteiros para slices/maps exportados. Goroutines leem `ID.*` direto, sem sincronização.
3. **DATADIR deve existir antes.** A biblioteca **não cria** o diretório. Em container, o orquestrador monta o volume.
4. **UUID persistente é obrigatório.** Se o UUID precisou ser gerado, **tem** que ser gravado em `$DATADIR/agent_uuid` com permissão 0644. Falha de gravação → código 106. O agente jamais pode ter UUID diferente entre execuções.
5. **Não retornar `error`.** O contrato é "fatal em qualquer falha". Toda função interna que detectar erro chama o helper `fatal(código, variável, motivo)` que escreve stderr e faz `os.Exit`. (Isso é incomum em libs Go — documentar claramente.)
6. **Sem dependências externas.** UUID v4 gerado via `crypto/rand`. Não puxar `google/uuid`, `viper` ou similar.
7. **MACHINE_ID_FILE.** O caminho do arquivo de machine-id é configurável via env `MACHINE_ID_FILE` (padrão `/etc/machine-id`).

## Algoritmo de Initialize (ordem dos passos)

A ordem importa porque `AGENT_NAME`, `AGENT_UUID` e `WORKSPACE` dependem de `DATADIR` já resolvido.

1. Se `ID != nil` → `fatal(112, "init", "Initialize() chamado mais de uma vez")`.
2. Resolver `DATADIR` (env → `/data`); validar `os.Stat` + `IsDir` → 100.
3. Resolver `MACHINE_ID` (env → arquivo de `$MACHINE_ID_FILE`) → 101/102.
4. Resolver `AGENT_NAME` (env → `$DATADIR/agent_name` → `argv[0]` saneado) → 103/104.
5. Resolver `AGENT_UUID` (env → `$DATADIR/agent_uuid` → gerar via `crypto/rand` e gravar) → 105/106/107.
6. Resolver `HOSTNAME` (env → `os.Hostname()`) → 108/109.
7. Resolver `WORKSPACE` (env → `$DATADIR/workspace`; **sem** fallback) → 110/111.
8. `ID = &Identity{...}`.

## Detalhamento e implementação

Para escrever o código:

- **`docs/SPEC.md`** — Algoritmo passo-a-passo por variável, exemplos das mensagens de stderr, esqueleto de testes table-driven.
- **`references/reference-impl.go`** — Implementação Go completa que compila. Adapte os imports ao caminho do módulo, mas **mantenha a lógica idêntica**: cada divergência da spec quebra o contrato com os consumidores da biblioteca.

Quando adaptar o código de referência, **não** introduza `error` no retorno, **não** adicione mutex, **não** mude os códigos de saída, e **não** acrescente dependências externas.
