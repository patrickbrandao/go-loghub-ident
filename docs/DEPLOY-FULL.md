# DEPLOY-FULL — Guia completo da `go-loghub-ident`

Tutorial detalhado de **todas** as propriedades, variáveis de ambiente,
fallbacks, contratos de uso e códigos de saída da biblioteca.

A biblioteca resolve a **identidade** de um software dentro do ecossistema
Loghub: seis valores (`DataDir`, `MachineID`, `AgentName`, `AgentUUID`,
`Hostname`, `Workspace`) carregados a partir de variáveis de ambiente,
arquivos e fallbacks do sistema, e expostos por getters de pacote.

---

## 1. Identidade do módulo

| Item                | Valor                                            |
|---------------------|--------------------------------------------------|
| Módulo Go           | `github.com/patrickbrandao/go-loghub-ident`      |
| Cláusula `package`  | `loghubident`                                     |
| Alias recomendado   | `lhident`                                          |
| Versão mínima de Go | 1.22 (exigida pela dependência `go-loghub-uuid`) |
| Dependência externa | `github.com/patrickbrandao/go-loghub-uuid`        |

---

## 2. API pública

A biblioteca expõe **apenas** uma função de inicialização e seis getters.
A struct interna que guarda os valores **não é exportada** — não há acesso a
campos públicos.

```go
func Initialize()        // resolve a identidade; encerra o processo em falha

func DataDir()   string  // diretório de dados resolvido (ex.: "/data")
func MachineID() string  // 32 hex [0-9a-f], sem hífen
func AgentName() string  // [a-z0-9._-]+
func AgentUUID() string  // UUIDv7 canônico, com hífens
func Hostname()  string  // [a-z0-9.-]+
func Workspace() string  // [a-z0-9.-]+ (tenant)
```

Todos os getters retornam `string` (imutável). A leitura é um simples acesso a
variável de pacote, sem locks — custo praticamente nulo, adequado a centenas de
threads lendo milhares de vezes por milissegundo.

### Constantes exportadas

| Constante               | Valor             | Significado                                         |
|-------------------------|-------------------|-----------------------------------------------------|
| `DefaultDataDir`        | `/data`           | DATADIR usado quando a env está ausente/vazia       |
| `DefaultMachineIDFile`  | `/etc/machine-id` | arquivo de machine-id padrão do SO                  |
| `DefaultWorkspace`      | `default`         | workspace usado quando nenhuma fonte fornece valor  |
| `EnvDebug`              | `LOGHUB_IDENT_DEBUG` | nome da env que ativa o log de diagnóstico       |

---

## 3. Ciclo de vida e concorrência

- **Chamada única.** Chame `Initialize()` **uma vez**, no início de `main()`.
  Uma segunda chamada é detectada por um `atomic.Bool` e encerra o processo com
  **código 112**.
- **Contrato happens-before.** `Initialize()` DEVE ser chamada **antes** de
  criar qualquer goroutine que vá ler os getters. O início da goroutine
  estabelece a relação *happens-before* que torna a leitura sem sincronização
  segura.
- **Imutável após Initialize.** Os valores são escritos uma única vez, durante
  `Initialize()`, e nunca mais modificados — por isso os getters dispensam lock.

### Uso correto

```go
package main

import (
	"fmt"
	"sync"

	lhident "github.com/patrickbrandao/go-loghub-ident"
)

func main() {
	lhident.Initialize() // 1º: resolve a identidade (antes de qualquer goroutine)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// leitura concorrente segura — Initialize() já retornou
			_ = lhident.Workspace()
			_ = lhident.AgentUUID()
		}()
	}
	wg.Wait()

	fmt.Println(lhident.AgentName(), "@", lhident.Hostname())
}
```

---

## 4. Pipeline de resolução por campo

Para cada fonte, a biblioteca aplica sempre:

**ler 1ª linha → trim de whitespace → forçar lowercase → sanear → validar regex**

- **Sanear** é mínimo: trim + lowercase em todos os campos. A remoção de `-`
  aplica-se **somente ao `MACHINE_ID`**. Nenhum outro caractere é removido —
  lixo reprova na regex e o processo aborta (falha alto).
- **Ausência vs. invalidez:**
  - Fonte **ausente ou vazia** após trim → **cai** para a próxima fonte.
  - Env **presente mas inválida** → **aborta** com o código do campo.
  - **Exceção** (arquivos auto-geridos pela lib — `machine_id` e `agent_uuid`
    em `$DATADIR`): conteúdo vazio **ou** inválido é tratado como ausente e a
    biblioteca **regenera + regrava**.

Nenhum valor final pode ser vazio ou reprovado pela regex.

---

## 5. Variáveis de ambiente — referência completa

| Variável              | Campo afetado | Obrigatória? | Observação                                                        |
|-----------------------|---------------|--------------|-------------------------------------------------------------------|
| `DATADIR`             | DataDir       | não          | padrão `/data`; validado de forma preguiçosa (ver §6)             |
| `MACHINE_ID`          | MachineID     | não          | se presente, deve casar `^[0-9a-f]{32}$` (após remover `-`)        |
| `MACHINE_ID_FILE`     | MachineID     | não          | caminho do arquivo de machine-id do SO; padrão `/etc/machine-id`  |
| `AGENT_NAME`          | AgentName     | não          | se presente, deve casar `^[a-z0-9._-]+$`                           |
| `AGENT_UUID`          | AgentUUID     | não          | se presente, deve ser UUIDv7 canônico (com hífens)                |
| `HOSTNAME`            | Hostname      | não          | se presente, deve casar `^[a-z0-9.-]+$`                            |
| `WORKSPACE`           | Workspace     | não          | se presente, deve casar `^[a-z0-9.-]+$`; padrão `default`          |
| `LOGHUB_IDENT_DEBUG`  | (diagnóstico) | não          | qualquer valor não vazio liga o log de origem em stderr           |

---

## 6. `DATADIR` — diretório de dados

- Env: `DATADIR`. Padrão: `/data` (`DefaultDataDir`).
- **Validação preguiçosa (lazy):** o diretório só é validado (existe e é
  diretório) quando um arquivo de fallback em `$DATADIR` precisa ser realmente
  lido ou gravado. No caminho feliz (todas as envs presentes), `$DATADIR` nunca
  é tocado e sua ausência **não** é fatal — suporta filesystem read-only.
- A biblioteca **não cria** o diretório; o orquestrador (Docker, systemd, etc.)
  deve montar/preparar o volume.
- Falha (não existe ou não é diretório), quando necessário → **código 100**.

Arquivos auto-geridos dentro de `$DATADIR` (permissão `0644`):

| Arquivo        | Conteúdo                              | Gravado quando?               |
|----------------|---------------------------------------|-------------------------------|
| `machine_id`   | 32 hex                                | gerado no nível 4 do MACHINE_ID |
| `agent_uuid`   | UUIDv7 canônico                       | gerado no nível 3 do AGENT_UUID |
| `agent_name`   | nome do agente                        | apenas lido (nunca gravado pela lib) |
| `workspace`    | nome do workspace                     | apenas lido (nunca gravado pela lib) |

---

## 7. `MACHINE_ID`

Regex: `^[0-9a-f]{32}$` (após remover `-` e aplicar lowercase).

Cadeia de resolução (4 níveis):

1. Env `MACHINE_ID` — se presente e **inválida** → aborta **102**.
2. Arquivo `$MACHINE_ID_FILE` (env; padrão `/etc/machine-id`). Se a env apontar
   para arquivo inexistente, troca para `/etc/machine-id`. Conteúdo inválido
   neste nível → **cai** (não aborta).
3. Arquivo `$DATADIR/machine_id` (auto-gerido). Vazio/inválido → cai.
4. **Gerar:** UUIDv7 (`Level1`) com hífens removidos → 32 hex; gravar em
   `$DATADIR/machine_id` (perm `0644`). Falha de gravação → **código 113**.

> A cadeia sempre termina em geração; machine-id nunca é fatal por arquivo
> ausente.

---

## 8. `AGENT_NAME`

Regex: `^[a-z0-9._-]+$`.

Cadeia:

1. Env `AGENT_NAME` — presente e inválida → aborta **104**. (Nunca grava arquivo.)
2. Arquivo `$DATADIR/agent_name` — presente e inválido → aborta **104**.
3. Fallback `argv[0]`: `filepath.Base` + lowercase + remoção do sufixo `.exe`
   (pontos internos preservados). Resultado vazio → **103**; resultado que
   reprova na regex → **104**.

Fallbacks determinísticos (`argv[0]`) **não** gravam arquivo.

---

## 9. `AGENT_UUID`

Formato canônico **com hífens**. Regex (v7 estrita):

```
^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$
```

Sanear = apenas trim + lowercase (NÃO remove `-`).

Cadeia:

1. Env `AGENT_UUID` — presente e inválida → aborta **107**. (Nunca grava arquivo.)
2. Arquivo `$DATADIR/agent_uuid` (auto-gerido) — vazio/inválido → trata como
   ausente e regenera.
3. **Gerar:** `loghubuuid.GenerateString(loghubuuid.Level1)`; gravar em
   `$DATADIR/agent_uuid` (perm `0644`). Falha de geração → **105**; falha de
   gravação → **106**; valor gerado que reprova na regex → **107**.

---

## 10. `HOSTNAME`

Regex: `^[a-z0-9.-]+$` (após lowercase).

Cadeia:

1. Env `HOSTNAME` — presente e inválida → aborta **109**.
2. `os.Hostname()` — erro → **108**; valor inválido → **109**.

---

## 11. `WORKSPACE`

Regex: `^[a-z0-9.-]+$`.

Cadeia:

1. Env `WORKSPACE` — presente e inválida → aborta **111**.
2. Arquivo `$DATADIR/workspace` — presente e inválido → aborta **111**.
3. Fallback `default` (`DefaultWorkspace`; não grava arquivo).

> Com o fallback `default`, workspace nunca é fatal por ausência.

---

## 12. Observabilidade / Debug

- Silencioso por padrão no caminho de sucesso.
- Se `LOGHUB_IDENT_DEBUG` estiver definida (qualquer valor não vazio),
  `Initialize()` escreve em stderr **uma linha por campo** indicando a origem
  do valor (`env` / `file` / `generated` / `fallback`), no formato:

  ```
  lib-loghub-ident: debug: MACHINE_ID: env
  lib-loghub-ident: debug: AGENT_NAME: fallback argv[0]
  lib-loghub-ident: debug: AGENT_UUID: generated
  ...
  ```

Exemplo:

```sh
LOGHUB_IDENT_DEBUG=1 DATADIR=/tmp/mydata WORKSPACE=staging ./my-service
```

---

## 13. Saída de erro e códigos de saída

Em qualquer falha, a biblioteca escreve em stderr exatamente:

```
lib-loghub-ident: <VARIÁVEL>: <motivo>
```

e chama `os.Exit(<código>)`.

| Código | Variável     | Motivo                                                         |
|--------|--------------|----------------------------------------------------------------|
| 100    | `DATADIR`    | diretório necessário não existe ou não é diretório             |
| 102    | `MACHINE_ID` | env presente não casa com `^[0-9a-f]{32}$`                      |
| 103    | `AGENT_NAME` | todas as fontes vazias (`argv[0]` saneado ficou vazio)         |
| 104    | `AGENT_NAME` | valor não casa com `^[a-z0-9._-]+$`                             |
| 105    | `AGENT_UUID` | falha na geração local de UUIDv7                               |
| 106    | `AGENT_UUID` | gravação em `$DATADIR/agent_uuid` (0644) falhou                |
| 107    | `AGENT_UUID` | valor não casa com a regex de UUIDv7                           |
| 108    | `HOSTNAME`   | env vazia e `os.Hostname()` retornou erro                      |
| 109    | `HOSTNAME`   | valor não casa com `^[a-z0-9.-]+$`                             |
| 111    | `WORKSPACE`  | valor não casa com `^[a-z0-9.-]+$`                             |
| 112    | (geral)      | `Initialize()` chamado mais de uma vez                        |
| 113    | `MACHINE_ID` | gravação em `$DATADIR/machine_id` (0644) falhou                |

---

## 14. Cenários de uso

### 14.1 Caminho feliz — tudo via env (sem tocar o disco)

```sh
MACHINE_ID=abcdef0123456789abcdef0123456789 \
AGENT_NAME=my-service \
AGENT_UUID=019e99e3-42f0-7882-9719-2305ff84949c \
HOSTNAME=node01 \
WORKSPACE=production \
./my-service
```

Como todas as envs estão presentes e válidas, `$DATADIR` nunca é acessado —
ideal para containers com filesystem read-only.

### 14.2 Identidade persistente num volume

```sh
mkdir -p /var/lib/myapp
DATADIR=/var/lib/myapp WORKSPACE=staging ./my-service
# 1ª execução: machine_id e agent_uuid são gerados e gravados em /var/lib/myapp
# execuções seguintes: os mesmos valores são lidos do volume (identidade estável)
```

### 14.3 Híbrido — machine-id do SO + nome do binário

```sh
# MACHINE_ID herda /etc/machine-id; AGENT_NAME cai para argv[0] (= "my-service")
DATADIR=/var/lib/myapp ./my-service
```

### 14.4 Docker

```dockerfile
FROM golang:1.22 AS build
WORKDIR /src
COPY . .
RUN go build -o /out/my-service .

FROM gcr.io/distroless/base-debian12
COPY --from=build /out/my-service /my-service
VOLUME ["/data"]                 # DATADIR padrão; identidade persistente
ENV WORKSPACE=production
ENTRYPOINT ["/my-service"]
```

---

## 15. Boas práticas

- Chame `Initialize()` como **primeira** instrução de `main()`.
- Em ambientes efêmeros (read-only / sem volume), forneça **todas** as envs
  (`MACHINE_ID`, `AGENT_NAME`, `AGENT_UUID`, `HOSTNAME`, `WORKSPACE`) para evitar
  qualquer acesso a `$DATADIR`.
- Para identidade estável entre reinícios, monte um volume e aponte `DATADIR`
  para ele; deixe `machine_id` e `agent_uuid` serem gerados na 1ª execução.
- Trate os códigos de saída no orquestrador: a faixa 100–113 indica
  precisamente qual variável/etapa falhou.
- Use `LOGHUB_IDENT_DEBUG=1` ao diagnosticar de onde cada valor veio.

Para um passo a passo mínimo de criação do ambiente e compilação, veja
[DEPLOY-FAST.md](DEPLOY-FAST.md).
