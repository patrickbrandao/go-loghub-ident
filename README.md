# lib-loghub-ident

Biblioteca Go que **resolve e expõe a identidade de um software** dentro do
ecossistema Loghub. Preenche seis campos — `DataDir`, `MachineID`, `AgentName`,
`AgentUUID`, `Hostname`, `Workspace` — a partir de variáveis de ambiente,
arquivos e fallbacks do sistema. Projetada para microserviços em containers.

- **Leve e rápida:** após `Initialize`, cada getter é apenas a leitura de uma
  variável de pacote — sem locks, adequado a centenas de threads chamando
  milhares de vezes por milissegundo.
- **Imutável após `Initialize`:** os valores são escritos uma única vez no boot
  e nunca mais modificados.
- **Multiplataforma:** UUIDv7 gerado localmente via
  [`go-loghub-uuid`](https://github.com/patrickbrandao/go-loghub-uuid), sem
  dependência de `/proc`.

> A especificação completa de design está em [SPEC.md](SPEC.md).

## Instalação

```bash
go get github.com/patrickbrandao/go-loghub-ident
```

Requer **Go 1.22+**.

## Uso


Arquivo: go.mod
```go
module ident-test

go 1.22

require github.com/patrickbrandao/go-loghub-uuid  v0.1.0
require github.com/patrickbrandao/go-loghub-ident v0.2.0
```

Arquivo: main.go
```go
// Defina LOGHUB_IDENT_DEBUG=1 para ver a origem de cada valor em stderr.
package main

import (
    "fmt"

    lhident "github.com/patrickbrandao/go-loghub-ident"
)

func main() {
    lhident.Initialize() // encerra o processo se algo falhar

    fmt.Printf("DataDir:   %s\n", lhident.DataDir())
    fmt.Printf("MachineID: %s\n", lhident.MachineID())
    fmt.Printf("AgentName: %s\n", lhident.AgentName())
    fmt.Printf("AgentUUID: %s\n", lhident.AgentUUID())
    fmt.Printf("Hostname:  %s\n", lhident.Hostname())
    fmt.Printf("Workspace: %s\n", lhident.Workspace())
}
```

Compilar:
```bash
go mod tidy && go build -o ident-test .
```

Compilar (multiplataforma):
```bash
# Windows
GOOS=windows GOARCH=amd64 go build -o ident-test.exe .

# macOS
GOOS=darwin GOARCH=amd64 go build -o ident-test .

# Linux (outros processadores)
GOOS=linux GOARCH=arm64 go build -o ident-test .
```

Rodar:
```bash
./ident-test;
    # DataDir:   /data
    # MachineID: 79fb72c3199f4b85978420223a389b94
    # AgentName: ident-test
    # AgentUUID: 019e9e7e-bc45-76b6-8351-4430b52c4070
    # Hostname:  debv.tmsoft.com.br
    # Workspace: default
```




### Regras de uso

- Chame `Initialize()` **uma única vez**, no início de `main()`, **antes** de
  criar qualquer goroutine que vá ler os getters. Esse contrato é o que torna a
  leitura concorrente segura sem sincronização.
- Uma segunda chamada a `Initialize()` encerra o processo com **código 112**.

## API

| Função          | Retorno  | Descrição                                  |
|-----------------|----------|--------------------------------------------|
| `Initialize()`  | —        | resolve a identidade; encerra em falha     |
| `DataDir()`     | `string` | diretório de dados                         |
| `MachineID()`   | `string` | id da máquina (32 hex, sem hífen)          |
| `AgentName()`   | `string` | nome do agente                             |
| `AgentUUID()`   | `string` | UUIDv7 canônico (com hífens)               |
| `Hostname()`    | `string` | hostname                                   |
| `Workspace()`   | `string` | workspace/tenant                           |

## Resolução de cada campo

Precedência geral: **variável de ambiente → arquivo de disco → fallback**.
Para cada fonte, env e arquivo igualmente: ler 1ª linha → remover BOM → trim de
espaços e caracteres de controle → lowercase → sanear → validar.
Uma fonte **ausente** cai para a próxima; uma env **presente mas inválida**
encerra o processo.

As regexes abaixo descrevem o conjunto de caracteres aceito, mas não são
compiladas: a validação usa verificadores manuais equivalentes, o que mantém o
pacote `regexp` fora da árvore de dependências. Além do conjunto de caracteres,
valem as regras que uma classe não expressa:

- `AGENT_NAME` e `WORKSPACE`: no máximo **64 caracteres**, e `.` e `..` são
  recusados (evita travessia de diretório em quem usa o valor para compor um
  caminho).
- `HOSTNAME`: regras de rótulo da **RFC 1123** — no máximo 253 caracteres,
  rótulos de 1 a 63, sem rótulo vazio e sem hífen nas bordas.
- Qualquer arquivo-fonte é lido com teto de **4 KiB** e precisa ser um arquivo
  comum: um FIFO ou um `/dev/zero` apontado por engano vira "fonte inválida" em
  vez de travar ou consumir toda a memória do nó.

| Campo        | Env             | Fontes de fallback                                              | Regex             |
|--------------|-----------------|----------------------------------------------------------------|-------------------|
| `DATADIR`    | `DATADIR`       | `/data` (existência validada de forma preguiçosa)              | caminho absoluto  |
| `MACHINE_ID` | `MACHINE_ID`    | `$MACHINE_ID_FILE` (def. `/etc/machine-id`) → `$DATADIR/machine_id` → gerado | `^[0-9a-f]{32}$` |
| `AGENT_NAME` | `AGENT_NAME`    | `$DATADIR/agent_name` → `argv[0]` (base, sem `.exe`)           | `^[a-z0-9._-]+$`  |
| `AGENT_UUID` | `AGENT_UUID`    | `$DATADIR/agent_uuid` → gerado (UUIDv7)                        | UUIDv7 canônico   |
| `HOSTNAME`   | `HOSTNAME`      | `os.Hostname()`                                                | `^[a-z0-9.-]+$`   |
| `WORKSPACE`  | `WORKSPACE`     | `$DATADIR/workspace` → `default`                              | `^[a-z0-9.-]+$`   |

### Persistência

- Apenas identidades **geradas aleatoriamente** (`machine_id`, `agent_uuid`) são
  gravadas em `$DATADIR`, para que o mesmo valor seja reobtido em execuções
  futuras. Valores determinísticos (`argv[0]`, `default`) nunca gravam arquivo.
- A permissão `0644` é **garantida**, não um teto: a biblioteca aplica um `chmod`
  explícito, então um umask restritivo do container não deixa os arquivos `0600`
  e ilegíveis para um sidecar com outro UID.
- A gravação é **durável** (`fsync`) e **atômica**: a criação usa exclusão mútua
  do filesystem e a regeneração usa arquivo temporário + `rename`. Um crash no
  meio da escrita não deixa arquivo truncado, e processos irmãos que sobem ao
  mesmo tempo sobre o mesmo volume **convergem para uma identidade única** — quem
  perde a corrida adota o valor de quem chegou primeiro.
- Isso vale para os **dois** cenários: arquivo ainda inexistente (arranque a
  frio) e arquivo existente porém corrompido (recuperação depois de uma queda).
  No segundo, a disputa é arbitrada por um **registro de regeneração**
  `$DATADIR/.machine_id.regen` / `$DATADIR/.agent_uuid.regen` — arquivos ocultos
  que só aparecem se uma identidade corrompida tiver sido substituída, e que a
  biblioteca preserva de propósito: apagá-los devolveria o nome à disputa. Um
  registro obsoleto é removido sozinho quando a identidade é criada do zero.
- `$DATADIR` precisa ser um **caminho absoluto**; um caminho relativo aborta com
  100. Do contrário a identidade dependeria do diretório de trabalho do processo.
- A biblioteca **não cria** o `DATADIR`; o container/orquestrador deve montar o
  volume. No caminho feliz (todas as envs presentes), `$DATADIR` nunca é tocado —
  compatível com filesystem read-only. Se alguma env faltar, o `$DATADIR` é
  apenas **lido**: um diretório ausente é tratado como fonte ausente e a cadeia
  segue para o fallback. Ele só é **obrigatório** quando há identidade a gerar e
  persistir (`machine_id`, `agent_uuid`).

## Diagnóstico

Defina a env `LOGHUB_IDENT_DEBUG` (qualquer valor não vazio) para que
`Initialize()` escreva em stderr **uma linha por campo** — os seis, `DATADIR`
inclusive — com a origem do valor (`env` / `file` / `generated` / `fallback`) e o
**valor final**:

```
lib-loghub-ident: debug: DATADIR: env = "/data"
lib-loghub-ident: debug: MACHINE_ID: file /etc/machine-id = "79fb72c3199f4b85978420223a389b94"
lib-loghub-ident: debug: AGENT_NAME: fallback argv[0] = "ident-test"
lib-loghub-ident: debug: AGENT_UUID: generated = "019e9e7e-bc45-76b6-8351-4430b52c4070"
lib-loghub-ident: debug: HOSTNAME: os.Hostname = "debv.tmsoft.com.br"
lib-loghub-ident: debug: WORKSPACE: fallback = "default"
```

As linhas saem **antes** do tratamento de uma eventual falha: no caminho de erro
elas são a única pista sobre qual fonte alimentou cada campo já resolvido.

### Avisos

Independentemente do modo debug, a biblioteca escreve em stderr um aviso sempre
que **descarta uma identidade persistida** — um `$DATADIR/machine_id` ou
`$DATADIR/agent_uuid` com conteúdo inválido, que a regra de regeneração manda
substituir:

```
lib-loghub-ident: aviso: MACHINE_ID: /data/machine_id tinha conteúdo inválido ("1111222233334444") e será REGERADO; a identidade desta máquina muda a partir de agora
```

Sem esse aviso, um agente voltaria com outro `machine_id` depois de um crash e
apareceria no servidor Loghub como uma máquina nova, sem rastro para
correlacionar.

## Códigos de saída

Em qualquer falha, escreve `lib-loghub-ident: <VARIÁVEL>: <motivo>` em stderr e
chama `os.Exit(<código>)`.

| Código | Variável     | Motivo                                                 |
|--------|--------------|--------------------------------------------------------|
| 100    | `DATADIR`    | diretório necessário ausente, não é diretório, caminho relativo, ou erro de I/O |
| 100    | `MACHINE_ID_FILE` | caminho informado está inacessível (não é "inexistente")  |
| 102    | `MACHINE_ID` | env não casa com `^[0-9a-f]{32}$`                       |
| 103    | `AGENT_NAME` | todas as fontes vazias (`argv[0]` ficou vazio)        |
| 104    | `AGENT_NAME` | valor não casa com `^[a-z0-9._-]+$`                     |
| 105    | `AGENT_UUID` | falha na geração local de UUIDv7                      |
| 106    | `AGENT_UUID` | gravação em `$DATADIR/agent_uuid` falhou              |
| 107    | `AGENT_UUID` | valor não casa com a regex de UUIDv7                  |
| 108    | `HOSTNAME`   | env vazia e `os.Hostname()` retornou erro             |
| 109    | `HOSTNAME`   | valor não casa com `^[a-z0-9.-]+$`                     |
| 111    | `WORKSPACE`  | valor não casa com `^[a-z0-9.-]+$`                     |
| 112    | (geral)      | `Initialize()` chamado mais de uma vez                |
| 113    | `MACHINE_ID` | gravação em `$DATADIR/machine_id` falhou              |
| 114    | `MACHINE_ID` | falha na geração local do UUIDv7 base do machine-id   |

> Em relação ao rascunho original: removidos os códigos **101** (machine-id é
> sempre regenerável) e **110** (workspace tem fallback `default`); adicionados o
> **113** (falha de gravação do machine_id) e o **114** (falha de geração do
> machine_id — o **105** é exclusivo do `AGENT_UUID`).

A linha de erro é a **última** que a biblioteca escreve; avisos e, em modo debug,
linhas de diagnóstico podem precedê-la.

## Exemplo

Veja [`examples/minimal`](examples/minimal) para um programa executável.

## Licença

Ver [LICENSE](LICENSE).
