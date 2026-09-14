# AGENTS.md — Guia do Projeto e Orientações para Agentes de IA

> Este documento serve como manual técnico, arquitetural e operacional para qualquer agente de IA ou desenvolvedor atuando no repositório [`go-loghub-ident`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident).
> Ele resume as especificações canônicas modulares contidas em [`docs/00-INDEX.md`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/docs/00-INDEX.md), mapeia todos os componentes da base de código e estabelece as regras estritas de implementação e testes.

---

## 1. Visão Geral e Propósito do Projeto

O **`go-loghub-ident`** (ou `lib-loghub-ident`) é uma biblioteca Go de baixo nível cujo objetivo é **resolver, persistir e expor a identidade canônica** de um processo/microserviço no ecossistema Loghub.

A biblioteca resolve seis campos fundamentais:
1. [`DataDir`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/identity.go#L228): Diretório persistente para armazenamento local de dados e arquivos de identidade.
2. [`MachineID`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/identity.go#L231): Identificador único do host/nó físico ou virtual (32 caracteres hexadecimais em lowercase, sem hífens).
3. [`AgentName`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/identity.go#L234): Nome do agente/serviço no ecossistema (máx. 64 caracteres, seguro contra path traversal).
4. [`AgentUUID`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/identity.go#L237): Identificador UUIDv7 canônico com hífens, ordenável temporalmente.
5. [`Hostname`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/identity.go#L240): Nome de host do sistema, validado segundo as regras de rótulos da RFC 1123.
6. [`Workspace`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/identity.go#L243): Workspace ou tenant lógico ao qual o agente pertence (máx. 64 caracteres, padrão `"default"`).

### Diretrizes de Engenharia e Princípios Arquiteturais

- **Zero Locks e Imutabilidade:** Após a execução de [`Initialize()`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/initialize.go#L34), os valores são armazenados em variáveis globais de pacote que nunca mais sofrem mutação. As chamadas aos getters são leituras diretas de memória (O(1)), adequadas para centenas de goroutines chamando milhares de vezes por milissegundo sem disputa de locks.
- **Chamada Única e Happens-Before:** [`Initialize()`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/initialize.go#L34) deve ser executada obrigatoriamente uma única vez no início de `main()`, antes do disparo de qualquer goroutine de trabalho. Uma segunda chamada resulta em encerramento imediato via `os.Exit(112)`.
- **Validação Manual Sem o Pacote `regexp`:** Por decisão deliberada de design, **é proibido importar `regexp`** no pacote principal. As expressões regulares contidas na documentação e nas constantes são descritivas; a validação é 100% feita por loops manuais em bytes em [`identity.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/identity.go). Isso economiza ~379 KB no binário e ~11,6 µs / 355 alocações de memória na inicialização.
- **Resiliência e Concorrência em Volume Compartilhado:** Identidades geradas (`machine_id` e `agent_uuid`) são persistidas com permissão explícita `0644` (imune a `umask` restritivo de containers) e `fsync`. Processos concorrentes competindo pelo mesmo volume convergem para uma identidade comum através de arquivos de exclusão mútua (`.claim` e `.regen`).
- **Segurança Rigorosa:** Arquivos lidos dentro de `$DATADIR` não seguem symlinks (`O_NOFOLLOW`/`Lstat`), impedindo exfiltração de segredos do host. Entradas como `.` e `..` são rejeitadas em `AGENT_NAME` e `WORKSPACE` para prevenir ataques de *path traversal*.
- **Convenção de Idioma:** Código-fonte, variáveis, funções, commits e branches em **inglês**. Comentários, mensagens de diagnóstico/erro, especificações e documentação em **português do Brasil (PT-BR)**.

---

## 2. Mapa de Navegação Rápida (Sitemap de Arquivos)

Aqui está a localização exata de cada arquivo do projeto e suas respectivas responsabilidades:

| Arquivo | Descrição e Funcionalidades Centrais |
| :--- | :--- |
| [`doc.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/doc.go) | Documentação oficial do pacote Go `loghubident` (`lhident`). |
| [`identity.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/identity.go) | Constantes (`DefaultDataDir`, `DefaultWorkspace`, limites RFC 1123, nomes de arquivos), tipo não-exportado [`identity`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/identity.go#L62), validadores manuais ([`validMachineID`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/identity.go#L104), [`validAgentUUID`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/identity.go#L118), [`validAgentName`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/identity.go#L148), [`validWorkspace`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/identity.go#L163), [`validHostname`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/identity.go#L181)), getters públicos e função [`IsInitialized()`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/identity.go#L247). |
| [`initialize.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/initialize.go) | Função de inicialização pública [`Initialize()`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/initialize.go#L34). Controle atômico com `atomic.Bool`, emissão de diagnósticos em `stderr` (`LOGHUB_IDENT_DEBUG`), avisos operacionais de regeneração e terminação via `os.Exit`. |
| [`resolve.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/resolve.go) | Motor de resolução pura: função [`resolve`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/resolve.go#L47) e struct [`resolver`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/resolve.go#L30). Cadeias de precedência de cada campo, pipeline de higienização de strings (`firstLine`, remoção de BOM UTF-8, trim de controles/NUL), persistência durável (`persistGenerated`), detecção de corrupção e protocolo de convergência concorrente (`.regen`). |
| [`system.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/system.go) | Interface abstrata [`system`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/system.go#L33) para operações de I/O do SO e implementação concreta [`osSystem`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/system.go#L87). Operações de leitura com limite de 4 KiB, `ReadFileNoFollow`, criação exclusiva com `os.Link` e fallback para trava `.claim` com TTL de 10s, gravação atômica via `os.CreateTemp` + `ReplaceFile`, `fsync` de arquivo e de diretório. |
| [`resolve_test.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/resolve_test.go) | Suíte completa de testes unitários isolados utilizando o mock [`fakeSystem`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/resolve_test.go#L15). Testa exaustivamente cada nível da cadeia de fallbacks, regras de saneamento, erros simulados de I/O e todos os códigos de saída. |
| [`system_test.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/system_test.go) | Testes unitários focados nas primitivas de baixo nível do SO em [`osSystem`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/system.go#L87): verificação de recusa de links simbólicos, atomicidade de escrita, durabilidade de permissões e comportamento do plano B (`claim`). |
| [`SPEC.md`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/SPEC.md) | Sumário executivo da especificação e ponteiro unificado para a suíte canônica em `docs/`. |
| [`README.md`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/README.md) | Documentação de apresentação da biblioteca, instruções de uso básico, exemplos de compilação multiplataforma e tabelas resumidas. |
| [`CLAUDE.md`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/CLAUDE.md) | Ponteiro rápido (`@AGENTS.md`) para agentes baseados na CLI Claude/Anthropic. |
| [`go.mod`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/go.mod) / [`go.sum`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/go.sum) | Módulo Go (versão mínima `go 1.22`). Dependência única: `github.com/patrickbrandao/go-loghub-uuidv7 v0.0.2`. |

### Subdiretórios

- **[`docs/`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/docs)**: **Especificações Técnicas Canônicas e Modulares do Projeto** (suficientes para refatorar a lib do zero com 100% de conformidade):
  - [`docs/00-INDEX.md`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/docs/00-INDEX.md): Índice geral, mapa de navegação e blueprint de refatoração do zero.
  - [`docs/01-ARCHITECTURE-AND-API.md`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/docs/01-ARCHITECTURE-AND-API.md): Identidade do módulo, campos, API pública, concorrência e abstração `system`.
  - [`docs/02-PIPELINE-AND-VALIDATION.md`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/docs/02-PIPELINE-AND-VALIDATION.md): Pipeline universal de saneamento e validadores manuais byte a byte sem `regexp`.
  - [`docs/03-FIELDS-RESOLUTION-AND-FALLBACKS.md`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/docs/03-FIELDS-RESOLUTION-AND-FALLBACKS.md): Matriz de resolução dos 6 campos, precedências e fallbacks.
  - [`docs/04-PERSISTENCE-CONCURRENCY-AND-SAFETY.md`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/docs/04-PERSISTENCE-CONCURRENCY-AND-SAFETY.md): Gravação durável (`fsync`), `CreateExclusive`, `.claim`, `readSettled` e `.regen`.
  - [`docs/05-OBSERVABILITY-AND-EXIT-CODES.md`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/docs/05-OBSERVABILITY-AND-EXIT-CODES.md): Observabilidade, `LOGHUB_IDENT_DEBUG`, avisos e tabela de códigos de saída 100 a 114.
  - [`docs/06-ENGINEERING-LESSONS-AND-ANTI-REGRESSION.md`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/docs/06-ENGINEERING-LESSONS-AND-ANTI-REGRESSION.md): Catálogo de lições e prevenção de bugs (BUG-01 a BUG-20).
  - [`docs/07-TESTING-STRATEGY.md`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/docs/07-TESTING-STRATEGY.md): Estratégia de testes em duas camadas, harness e metas de benchmark.
  - [`docs/08-RELEASE-GUIDE.md`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/docs/08-RELEASE-GUIDE.md): Ciclo de versionamento semântico, imutabilidade de tags e publicação.
- **[`skill/`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/skill)**: **Skill Exportável para Agentes de IA e Projetos Consumidores**:
  - [`skill/SKILL.md`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/skill/SKILL.md): Guia unificado de adoção rápida e operação de produção (unificação de DEPLOY-FAST e DEPLOY-FULL).
  - [`skill/references/`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/skill/references): Manuais aprofundados de configuração e troubleshooting.
  - [`skill/examples/`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/skill/examples): Exemplos práticos em Go, Dockerfile distroless e Pod Kubernetes.
- **[`tests/`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/tests)**: Suíte de testes de integração ponta a ponta contra o sistema operacional real via subprocessos.
  - [`tests/helper_test.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/tests/helper_test.go): Harness de subprocesso baseado em `TestMain` e `LOGHUB_IDENT_HELPER=1`.
  - [`tests/api_test.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/tests/api_test.go): Testes de caminho feliz, precedências e persistência em disco temporário.
  - [`tests/exitcodes_test.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/tests/exitcodes_test.go): Validação automatizada de todos os códigos de saída (`100` a `114`).
  - [`tests/bugs_test.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/tests/bugs_test.go) & [`tests/bugs2_test.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/tests/bugs2_test.go): Testes de regressão permanentes (`TestFix_BUGxx`).
  - [`tests/bench_test.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/tests/bench_test.go): Benchmarks de alocação e latência dos getters, validadores manuais e ciclo de boot.
  - [`tests/doc.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/tests/doc.go) & [`tests/README.md`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/tests/README.md): Documentação da suíte de integração.
- **[`tasks/`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/tasks)**: Registros de auditoria internas e pendências temporárias (pasta não versionada, listada no `.gitignore`).

---

## 3. Fluxo de Resolução e Regras dos Campos

### Pipeline de Higienização de Strings

Toda fonte textual (seja lida de variável de ambiente ou da primeira linha de um arquivo) passa pelo **mesmo pipeline** em [`resolve.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/resolve.go):
1. **Primeira linha:** Extrai até o primeiro `\r` ou `\n`.
2. **Remoção de BOM:** Remove marca de ordem de byte UTF-8 (`0xEF, 0xBB, 0xBF`), comum em editores Windows.
3. **Trim de espaços e controles:** Remove espaços e caracteres de controle Unicode (incluindo `NUL` / `0x00`, prevenindo corrupção por escrita truncada).
4. **Lowercase:** Conversão compulsória para minúsculas.
5. **Sanitização específica:** Apenas `MACHINE_ID` remove caracteres `-` antes da validação.
6. **Validação estrita:** Teste contra a função de validação manual correspondente.

### Matriz de Resolução dos 6 Campos

| Campo | Variável de Ambiente | Cadeia de Precedência de Fallback | Validação & Restrições | Persistência em `$DATADIR`? | Códigos de Erro |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **`DATADIR`** | `DATADIR` | Padrão: `/data` | Deve ser caminho absoluto (`rootedPath`) e sem controles. Existência checada de forma lazy. | Diretório raiz de dados. **Não é criado pela lib**. | **100** (caminho relativo ou com controle, inexistente na gravação, não é diretório, erro I/O, symlink recusado) |
| **`MACHINE_ID`** | `MACHINE_ID` | 1. `$MACHINE_ID_FILE` (def: `/etc/machine-id`)<br>2. `$DATADIR/machine_id`<br>3. Geração local de UUIDv7 (sem `-`) | `^[0-9a-f]{32}$` (32 caracteres hexadecimais após strip de hífens). | **Sim** (somente quando gerado aleatoriamente). Permissão `0644`. | **100** (`MACHINE_ID_FILE` relativo/com controle/inacessível/não-comum; conteúdo inválido cai p/ próximo nível),<br>**102** (env presente inválida),<br>**113** (falha escrita),<br>**114** (falha geração UUID) |
| **`AGENT_NAME`** | `AGENT_NAME` | 1. `$DATADIR/agent_name`<br>2. `argv[0]` (nome base do binário sem `.exe`) | `^[a-z0-9._-]+$`, máx. 64 caracteres. **Rejeita `.` e `..`**. | **Não** (fallback `argv[0]` é determinístico). | **103** (todas as fontes vazias / `argv[0]` saneando para vazio/`/`/`.`),<br>**104** (valor não casa com regex/tamanho ou é `.` / `..`) |
| **`AGENT_UUID`** | `AGENT_UUID` | 1. `$DATADIR/agent_uuid`<br>2. Geração local de UUIDv7 canônico | UUIDv7 canônico RFC 9562 (36 caracteres com hífens, versão 7, variante RFC). | **Sim** (somente quando gerado aleatoriamente). Permissão `0644`. | **105** (falha geração local),<br>**106** (falha escrita `$DATADIR`),<br>**107** (env/valor não é UUIDv7 válido) |
| **`HOSTNAME`** | `HOSTNAME` | 1. `os.Hostname()` | `^[a-z0-9.-]+$`, máx. 253 chars, rótulos 1-63 chars, sem hífens nas pontas (RFC 1123). | **Não** (não salva em disco). | **108** (`os.Hostname` retornou erro),<br>**109** (inválido pela RFC 1123) |
| **`WORKSPACE`** | `WORKSPACE` | 1. `$DATADIR/workspace`<br>2. Fallback fixo: `"default"` | `^[a-z0-9.-]+$`, máx. 64 caracteres. **Rejeita `.` e `..`**. | **Não** (fallback `"default"` é determinístico). | **111** (env ou arquivo com valor inválido) |

> **Nota sobre códigos ausentes:** Os códigos **101** e **110** do design original foram propositalmente eliminados: `MACHINE_ID` nunca falha por ausência (ele é sempre gerável), e `WORKSPACE` possui fallback fixo infalível (`default`).

---

## 4. Persistência, Concorrência e Resiliência

### Gravação Durável e Atômica

As identidades geradas são gravadas via [`persistGenerated`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/resolve.go#L317):
1. **Criação a Frio (Arquivo Inexistente):** Utiliza [`CreateExclusive`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/system.go#L158). Cria um arquivo temporário via `writeTemp` no mesmo diretório, executa `fchmod(0644)`, `fsync`, fecha e tenta vincular ao destino via `os.Link`. Se outro processo chegou primeiro (`ErrExist`), o perdedor aguarda por até 10 s (500 tentativas de 20 ms) e adota o conteúdo do arquivo existente através de [`readSettled`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/resolve.go#L290), evitando falso descarte em volumes NFS.
2. **Plano B para Filesystems Sem Hard Link:** Em filesystems onde `os.Link` falha (como certas montagens virtuais ou Windows), [`createExclusiveDirect`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/system.go#L183) utiliza um arquivo de trava `.claim` com TTL de 10s e publicação final por `os.Rename`.
3. **Recuperação de Corrupção (Regeneração):** Se um arquivo existente em `$DATADIR` contiver dados inválidos (ex.: truncamento por crash ou lixo):
   - A biblioteca emite um aviso obrigatório em `stderr` reportando tamanho e hash FNV-1a 64-bit (sem vazar conteúdo).
   - A disputa é arbitrada por um arquivo de regeneração `$DATADIR/.<arquivo>.regen`. O processo vencedor gera a nova identidade e publica via [`ReplaceFile`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/system.go#L219) (arquivo temporário + rename atômico).
   - Processos perdedores adotam o valor registrado no `.regen`, garantindo convergência total de nós.

---

## 5. Tabela Completa de Códigos de Saída (Exit Codes)

Quando ocorre qualquer falha irrecuperável na resolução, a biblioteca escreve em `stderr`:
```text
lib-loghub-ident: <VARIÁVEL>: <motivo>
```
E chama imediatamente `os.Exit(código)`:

| Código | Variável | Motivo do Erro |
| :---: | :--- | :--- |
| **100** | `DATADIR` | Diretório não existe (no momento de gravação), não é diretório, caminho é relativo ou contém controle, erro de I/O / permissão, ou link simbólico recusado. |
| **100** | `MACHINE_ID_FILE` | Caminho explícito informado na env é relativo, contém controle, inacessível por erro de I/O/permissão, não é arquivo comum (FIFO/dir/device) ou excede 4 KiB. |
| **102** | `MACHINE_ID` | Variável de ambiente presente, porém não corresponde a 32 caracteres hexadecimais (`^[0-9a-f]{32}$`). |
| **103** | `AGENT_NAME` | Todas as fontes estão vazias (incluindo `argv[0]` saneado para vazio, `.` ou separador). |
| **104** | `AGENT_NAME` | Valor da env, do arquivo ou da base de `argv[0]` não atende a `^[a-z0-9._-]+$`, excede 64 caracteres ou é `.` / `..`. |
| **105** | `AGENT_UUID` | Falha interna na geração local de UUIDv7 para `AGENT_UUID`. |
| **106** | `AGENT_UUID` | Falha de gravação de `$DATADIR/agent_uuid` no disco (permissão, disco cheio, I/O). |
| **107** | `AGENT_UUID` | Valor fornecido na env ou gerado não é um UUIDv7 canônico estrito. |
| **108** | `HOSTNAME` | Variável de ambiente vazia e chamada ao sistema `os.Hostname()` retornou erro. |
| **109** | `HOSTNAME` | Hostname não cumpre a RFC 1123 (caracteres inválidos, mais de 253 chars, rótulos > 63 chars ou hífens nas extremidades). |
| **111** | `WORKSPACE` | Valor da env ou do arquivo não atende a `^[a-z0-9.-]+$`, excede 64 caracteres ou é `.` / `..`. |
| **112** | *(geral)* | [`Initialize()`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/initialize.go#L34) foi invocado mais de uma vez no mesmo processo (prefixo `geral:`). |
| **113** | `MACHINE_ID` | Falha de gravação de `$DATADIR/machine_id` no disco. |
| **114** | `MACHINE_ID` | Falha interna na geração local do UUIDv7 base para o `MACHINE_ID`. |

### Observabilidade e Diagnóstico

- **Modo Silencioso:** Por padrão, no caminho de sucesso a biblioteca não escreve nada em `stdout` ou `stderr`.
- **Diagnóstico Debug:** Se a env `LOGHUB_IDENT_DEBUG` estiver definida (qualquer valor não vazio), são impressas 6 linhas em `stderr` (uma por campo resolvido) antes de qualquer falha. Exemplo:
  ```text
  lib-loghub-ident: debug: DATADIR: env = "/data"
  lib-loghub-ident: debug: MACHINE_ID: file /etc/machine-id = "0123456789abcdef0123456789abcdef"
  lib-loghub-ident: debug: AGENT_NAME: fallback argv[0] = "my-service"
  lib-loghub-ident: debug: AGENT_UUID: generated = "018f3a5b-7c8d-7e9f-8a1b-2c3d4e5f6a7b"
  lib-loghub-ident: debug: HOSTNAME: os.Hostname = "node01.example.com"
  lib-loghub-ident: debug: WORKSPACE: fallback = "default"
  ```
- **Avisos Operacionais:** Eventos críticos (como descarte de identidades corrompidas e adoção de regenerações) são impressos **sempre** com o prefixo `lib-loghub-ident: aviso: `.

---

## 6. Diretrizes Estritas para Agentes de IA ao Modificar o Código

Qualquer agente que realize alterações neste repositório DEVE seguir impreterivelmente estas regras:

1. **NUNCA Adicione o Pacote `regexp`:**
   - Sob nenhuma hipótese adicione `"regexp"` aos imports do pacote raiz (`identity.go`, `resolve.go`, etc.).
   - Mantenha ou estenda os validadores manuais em [`identity.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/identity.go).
2. **Preserve a Abstração [`system`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/system.go#L33):**
   - A lógica pura de negócio em [`resolve.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/resolve.go) NÃO DEVE chamar funções do pacote `os` ou do sistema diretamente.
   - Qualquer nova interação com o SO deve ser adicionada à interface [`system`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/system.go#L33), implementada em [`osSystem`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/system.go#L87) e simulada em [`fakeSystem`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/resolve_test.go#L15).
3. **Getters Devem Permanecer Ultra-Rápidos e Sem Locks:**
   - Não adicione `sync.Mutex`, `sync.RWMutex` ou canais nos getters públicos ([`DataDir()`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/identity.go#L228), [`MachineID()`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/identity.go#L231), etc.). Toda a concorrência é resolvida na fase de inicialização via *happens-before*.
4. **Respeite o Limite de Leitura de Arquivo (4 KiB):**
   - Fontes de identidade não devem ler arquivos arbitrariamente grandes. Mantenha a guarda de [`maxIdentFileSize`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/system.go#L19) (4 KiB) e rejeição de non-regular files (FIFOs, sockets, devices).
5. **Atualize Testes em Camadas Duplas:**
   - Se alterar regras de resolução ou novos campos:
     - Adicione testes unitários no [`resolve_test.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/resolve_test.go) com `fakeSystem`.
     - Adicione/atualize testes na suíte de integração em [`tests/api_test.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/tests/api_test.go) ou [`tests/exitcodes_test.go`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/tests/exitcodes_test.go).
6. **Mantenha a Documentação Sincronizada:**
   - Alterações em comportamentos de campos, códigos de erro ou novas variáveis exigem atualização obrigatória e coerente nas especificações modulares em [`docs/`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/docs), na skill em [`skill/`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/skill), em [`SPEC.md`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/SPEC.md), em [`README.md`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/README.md) e neste [`AGENTS.md`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/AGENTS.md).

---

## 7. Comandos de Desenvolvimento e Verificação

### Executando Testes

```bash
# 1. Executar todos os testes do repositório (unitários e integração)
go test ./...

# 2. Executar suíte de integração com detector de corridas (race detector)
go test -race ./tests/

# 3. Executar testes rápidos ignorando testes concorrentes pesados
go test -short ./tests/

# 4. Executar apenas os testes de regressão de bugs (permanentes)
go test ./tests/ -run TestFix -v

# 5. Medir cobertura de código real sobre o pacote raiz a partir dos subprocessos
go test ./tests/ -coverpkg=github.com/patrickbrandao/go-loghub-ident

# 6. Executar os benchmarks de performance e alocações
go test ./tests/ -bench=. -benchmem -run=XXX

# 7. Verificação estática com go vet
go vet ./...
```

### Testando a Aplicação de Exemplo

```bash
# Executar o exemplo mínimo com variáveis explícitas (sem tocar disco)
cd skill/examples/minimal
MACHINE_ID=abcdef0123456789abcdef0123456789 \
AGENT_NAME=meu-agente \
AGENT_UUID=018f3a5b-7c8d-7e9f-8a1b-2c3d4e5f6a7b \
HOSTNAME=host01.empresa.local \
WORKSPACE=prod \
go run .
```
