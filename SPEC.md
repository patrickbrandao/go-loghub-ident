# Especificação — `lib-loghub-ident`

> Biblioteca Go que resolve e expõe a identidade de um software dentro do
> ecossistema Loghub. Documento de especificação consolidado após a entrevista
> de design.
> 
> **Aviso de Arquitetura Documental:** As especificações canônicas, exaustivas e divididas por responsabilidades técnicas (suficientes para refatorar o projeto do zero) estão modularizadas em [`docs/00-INDEX.md`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/docs/00-INDEX.md). Para guias de uso, receitas Docker/K8s e integração em outros projetos, consulte a skill em [`skill/SKILL.md`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/skill/SKILL.md).

## 1. Identidade do módulo

| Item                | Valor                                              |
|---------------------|----------------------------------------------------|
| Módulo Go           | `github.com/patrickbrandao/go-loghub-ident`        |
| Cláusula `package`  | `loghubident`                                      |
| Alias de uso        | `lhident`                                          |
| Versão mínima de Go | 1.22 (exigida pela dependência `go-loghub-uuidv7`) |
| Dependência externa | `github.com/patrickbrandao/go-loghub-uuidv7`       |

> Observação: o rascunho pedia Go 1.21+. A dependência de UUID exige 1.22, então
> esta é a versão mínima efetiva.

**Convenção de idioma:** nomes de arquivos e código-fonte em inglês; comentários,
documentação e esta especificação em português do Brasil.

## 2. API pública

A biblioteca expõe **apenas funções getter**. A struct interna que guarda os
valores **não é exportada** — não há acesso a campos públicos.

```go
func Initialize()        // resolve a identidade; encerra o processo em falha
func IsInitialized() bool // true após Initialize() bem-sucedido

func DataDir()   string
func MachineID() string
func AgentName() string
func AgentUUID() string
func Hostname()  string
func Workspace() string
```

Todos os getters retornam `string` (imutável em Go). A leitura é um simples
acesso a uma variável de pacote, sem locks — custo praticamente nulo, adequado a
centenas de threads chamando milhares de vezes por milissegundo.

## 3. Modelo de concorrência e ciclo de vida

- **Chamada única.** `Initialize()` deve ser chamada uma vez, no início de
  `main()`. A segunda chamada é detectada por um `atomic.Bool` via
  `CompareAndSwap(false, true)`; se o swap falhar, escreve em stderr e encerra
  com **código 112**.
- **Contrato happens-before.** `Initialize()` DEVE ser chamada **antes** de criar
  qualquer goroutine que vá ler os getters. O início da goroutine estabelece a
  relação *happens-before* que torna a leitura sem sincronização segura.
- **Imutável após Initialize.** Os valores são escritos uma única vez, durante
  `Initialize()`, e nunca mais modificados.
- **Antes de Initialize.** Os getters devolvem `""` (zero value). O valor é
  indefinido e não deve ser usado; use `IsInitialized()` para validar o contrato
  em runtime.

## 4. Arquitetura interna (testabilidade)

A lógica de resolução é **pura** e separada da saída do processo:

```go
type system interface {        // abstração de I/O, injetável em testes
    Getenv(key string) string
    Stat(path string) (os.FileInfo, error)
    ReadFile(path string) ([]byte, error)                 // limitada a 4 KiB, só arquivo comum
    ReadFileNoFollow(path string) ([]byte, error)         // limitada a 4 KiB, só arquivo comum, recusa symlinks
    CreateExclusive(path string, data []byte, perm os.FileMode) (bool, error)
    ReplaceFile(path string, data []byte, perm os.FileMode) error
    Remove(path string) error
    Hostname() (string, error)
    Args() []string
    GenerateUUIDv7() (string, error)
}

type failure struct { code int; variable, reason string }

func resolve(sys system) (*identity, *failure)
```

- `osSystem{}` é a implementação real (usa `os`, `filepath` e
  `uuidv7.GenerateString(uuidv7.Level1)`).
- `Initialize()` chama `resolve(osSystem{})`; em erro, escreve em stderr e chama
  `os.Exit(failure.code)`.
- Os testes injetam um `fakeSystem` com env/arquivos/hostname/argv em memória e
  inspecionam `(identity, failure)` sem matar o processo.

**Gravação.** Não há um `WriteFile` genérico. A persistência dos arquivos
auto-geridos é dividida em duas operações, ambas duráveis (`fsync`) e com a
permissão aplicada explicitamente (`fchmod`), sem interferência do umask:

- `CreateExclusive` grava **somente se o arquivo ainda não existir**. Devolve
  `(true, nil)` se este processo criou o arquivo e `(false, nil)` se outro
  chegou antes — é o filesystem quem arbitra a corrida entre processos irmãos
  que sobem ao mesmo tempo sobre o mesmo `$DATADIR`. Quem perde a corrida
  **relê o arquivo e adota o valor do vencedor**, de modo que todos convergem
  para uma identidade única. O processo perdedor aguarda a estabilização do arquivo
  por até 10 s (500 tentativas de 20 ms), prevenindo declarações falsas de corrupção
  em volumes de rede com latência de atributos (NFS). Em sistemas de arquivos sem suporte a hard links
  (Plano B), a exclusão mútua utiliza um arquivo de trava `.claim` com TTL
  (10 s), verificação de timestamp e publicação final atômica via `os.Rename`,
  garantindo que leitores concorrentes nunca observem um arquivo vazio de 0 bytes.
- `ReplaceFile` substitui o conteúdo de forma **atômica** (arquivo temporário
  + `rename`). Usada apenas na regeneração de um arquivo corrompido. Nunca
  expõe conteúdo parcial nem deixa arquivo truncado se o processo morrer no
  meio da escrita.

**Regeneração concorrente.** Substituir um arquivo corrompido exige uma
arbitragem própria, e não a mesma do arranque a frio. `CreateExclusive` não
serve: o nome já está ocupado, então todo processo perderia. `ReplaceFile`
sozinha também não: ela é **incondicional**, então cada processo gravaria o SEU
valor e voltaria com uma identidade diferente na memória, ainda que só a última
gravação sobrevivesse no disco — a mesma divergência do arranque a frio,
entrando por outra porta, e no cenário de recuperação mais provável (um arquivo
corrompido normalmente significa queda no meio de uma gravação, que é
exatamente quando várias réplicas sobem juntas).

A disputa é resolvida num **registro de regeneração** à parte,
`$DATADIR/.<arquivo>.regen`, criado com `CreateExclusive`: o primeiro processo
a criá-lo vence, e é o valor dele que **todos** gravam no arquivo de identidade
— a mesma gravação, byte a byte, em todos os processos, e portanto idempotente
em qualquer ordem. O processo vencedor emite o aviso de que a identidade foi
regerada; processos que perdem a corrida leem o registro `.regen`, adotam o
valor vencedor e emitem aviso específico indicando restauração a partir do
registro prévio.

O registro **não é apagado** ao final: apagá-lo devolveria o nome à disputa e
permitiria que um processo atrasado vencesse uma segunda vez, com outro valor.
Ele é removido apenas quando uma identidade é criada **do zero** (arquivo
ausente), momento em que um registro anterior está obsoleto.

**Leitura e segurança.** `ReadFile` recusa o que não é arquivo comum (FIFO, device,
diretório) e o que passa de **4 KiB**, devolvendo "fonte inválida". No caso de
arquivos dentro de `$DATADIR`, a leitura utiliza `ReadFileNoFollow`, que recusa
links simbólicos com erro de fonte inválida, evitando que arquivos externos ao
volume sejam exfiltrados. Sem esses limites, um `MACHINE_ID_FILE` apontado
por engano para `/dev/zero` consumiria toda a memória do nó, e um FIFO sem
escritor penduraria o boot para sempre.

**Isolamento de volume.** A convergência de identidade assume processos rodando no
mesmo nó (mesmo pod / host). Réplicas ou containers independentes NUNCA devem
compartilhar o mesmo volume gravável `$DATADIR`, sob pena de colidirem
`machine_id` e `agent_uuid`.

## 5. Pipeline de resolução por campo

Para cada fonte: **ler 1ª linha → remover BOM → trim de espaços e caracteres de
controle → forçar lowercase → sanear → validar**.

O pipeline é o **mesmo para env e para arquivo**. Uma variável de ambiente com
quebra de linha embutida tem sua primeira linha extraída, exatamente como um
arquivo — as duas fontes do mesmo campo não podem ter saneamentos diferentes.

- **Sanear** é mínimo: remoção do BOM UTF-8 (`\ufeff`), trim de espaços **e de
  caracteres de controle** nas bordas, e lowercase. A remoção de `-` aplica-se
  **somente ao `MACHINE_ID`**. Nenhum outro caractere é removido — lixo reprova
  na validação (falha alto).
  - O trim de caracteres de controle não é detalhe: um byte `NUL` residual — o
    que uma gravação truncada por crash deixa — sobreviveria a um trim de
    espaços, reprovaria na validação e faria a biblioteca **descartar a
    identidade persistida**. Um BOM, que editores do Windows escrevem por
    padrão, derrubaria o processo.
  - Avisos e erros operacionais nunca exibem dados brutos ou lixo arbitrário lido
    do disco em texto claro, registrando apenas tamanho e identificador sanitizado,
    evitando vazamento de segredos em logs operacionais.
- **Ausência vs. invalidez:**
  - Fonte **ausente ou vazia** após trim → cai para a próxima fonte.
  - Fonte de env **presente mas inválida** → **aborta** com o código do campo.
  - Exceção (arquivos auto-geridos pela lib — `machine_id`, `agent_uuid` em
    `$DATADIR`): conteúdo vazio **ou** inválido → trata como ausente e
    **regenera + regrava**, com aviso obrigatório (§12) e arbitragem entre
    processos concorrentes (§4).

Nenhum valor final pode ser vazio ou reprovado pela validação.

**Validação.** As regexes descritas nas seções seguintes definem o conjunto de
caracteres aceito, mas **não são compiladas**: a verificação é feita por
validadores manuais equivalentes. A troca elimina o pacote `regexp` da árvore de
dependências (~379 KB de binário e ~11,6 µs / 355 alocações de init pagos por
todo importador, inclusive o que nunca chama `Initialize`) e permite expressar
as regras que uma classe de caracteres não alcança: teto de comprimento, recusa
de componentes de caminho relativos e a estrutura de rótulos da RFC 1123.

## 6. DATADIR

- Env: `DATADIR`. Padrão: `/data`.
- **Caminho absoluto obrigatório e livre de caracteres de controle.** O valor é
  normalizado com `filepath.Clean`, precisa ser absoluto e não pode conter
  caracteres de controle Unicode ou bytes `< 0x20` / `0x7f` (ex.: `\n`, `\r`, `NUL`);
  caminho relativo ou com controle aborta com **código 100**. Um
  `DATADIR=dados` faria a identidade depender do diretório de trabalho: o mesmo
  serviço iniciado de outro lugar (um `WorkingDirectory` diferente no unit do
  systemd, um `chdir` da aplicação) leria outro arquivo e viraria outro agente.
  A normalização também garante que `DataDir()` nunca devolva `/data//` ou
  `/data/.`, que virariam chaves distintas para o mesmo diretório rio abaixo.
  O critério é "ancorado na raiz", não `filepath.IsAbs` puro: em Windows o
  `IsAbs` recusa `/data` — o próprio padrão desta biblioteca — porque lá um
  caminho absoluto exige letra de unidade. Um caminho iniciado por separador é
  aceito em qualquer plataforma; `dados`, `./dados` e o `C:dados` relativo à
  unidade continuam recusados.
- **Validação preguiçosa (lazy):** a EXISTÊNCIA do diretório só é verificada
  quando um arquivo em `$DATADIR` precisa ser realmente lido ou gravado. No
  caminho feliz (todas as envs presentes), `$DATADIR` nunca é tocado e sua
  ausência **não** é fatal — suporta filesystem read-only.
- A biblioteca **não cria** o diretório; o orquestrador deve montar o volume.
- **Leitura:** um `$DATADIR` inexistente (`fs.ErrNotExist`) é "fonte ausente" — o campo segue para
  o próximo nível da cadeia. É o que torna alcançáveis os fallbacks que as §7 e
  §11 declaram infalíveis; se a leitura abortasse, o nível seguinte nunca
  rodaria e a justificativa para remover os códigos 101 e 110 não se
  sustentaria. Qualquer outro erro de `Stat` (como `EACCES` / permissão negada)
  ou a detecção de que `$DATADIR` não é diretório aborta imediatamente com
  **código 100**, inclusive na leitura.
- **Gravação:** um `$DATADIR` inexistente ou que não é diretório aborta com
  **código 100**. Gerar identidade sem onde persistí-la é falha legítima.
- Erro de **I/O real** ao ler um arquivo dentro de um `$DATADIR` que existe
  (ex.: permissão negada, disco com falha) também aborta com **código 100** —
  não é mascarado como fonte vazia. Links simbólicos dentro de `$DATADIR` são
  recusados por segurança (`ReadFileNoFollow`) e abortam com **código 100**,
  evitando exfiltração de segredos ou operações sobre arquivos impróprios.

## 7. MACHINE_ID

Regex: `^[0-9a-f]{32}$` (após remover `-` e aplicar lowercase).

Cadeia de resolução:
1. Env `MACHINE_ID` — se presente e **inválida** → aborta **102**.
2. Arquivo `$MACHINE_ID_FILE` (env; padrão `/etc/machine-id`). Conteúdo inválido
   neste nível → **cai** (não aborta). Tratamento de erro:
    - Se a env contiver um caminho **relativo** ou contendo **caracteres de controle**,
      aborta com **código 100** e a variável `MACHINE_ID_FILE`.
   - Se a env apontar para arquivo **inexistente**, troca para `/etc/machine-id`.
     "Inexistente" é `fs.ErrNotExist` e nada mais.
   - Se a env apontar para um caminho **inacessível** (permissão negada, erro de
     I/O) OU **inutilizável** (diretório, FIFO, dispositivo, ou maior que 4 KiB),
     aborta com **código 100** e a variável `MACHINE_ID_FILE`. O operador deu uma
     instrução explícita que não pôde ser cumprida; entregar outra identidade em
     silêncio esconde o erro.
   - O `/etc/machine-id` **padrão** (env ausente) continua best-effort: ilegível,
     inexistente, inválido ou inutilizável, apenas cai para o próximo nível.
3. Arquivo `$DATADIR/machine_id` (auto-gerido). Vazio/inválido → cai, com
   **aviso obrigatório em stderr** (ver §12).
4. **Gerar:** UUIDv7 (`Level1`) com hífens removidos → 32 hex; gravar em
   `$DATADIR/machine_id` (perm 0644, garantida, durável e atômica — ver §4).
   - Falha de **geração** → **código 114**.
   - Valor gerado que reprova na validação (só com dependência corrompida) →
     **código 114**.
   - Falha de **gravação** → **código 113**.
   - Se outro processo criou o arquivo entre a leitura e a gravação, o valor
     dele é adotado.
   - A persistência é **por campo, sem atomicidade entre campos**: um boot que
     gere `machine_id` e aborte depois (p.ex. 104 em `AGENT_NAME`) deixa o arquivo
     gerado para o próximo boot. Isso é válido e esperado; não apagar.

> O código **101** do rascunho foi **removido**: como a cadeia sempre termina em
> geração, machine-id nunca é fatal por arquivo ausente.

## 8. AGENT_NAME

Regex: `^[a-z0-9._-]+$`, com no máximo **64 caracteres**, e recusando `.` e `..`.

Os dois acréscimos são de segurança: `AgentName` identifica o agente no
ecossistema Loghub e um consumidor que o use para compor um caminho
(`filepath.Join(base, AgentName())` é o uso óbvio) herdaria uma travessia de
diretório vinda de variável de ambiente. Sem teto de comprimento, o valor vaza
para nomes de arquivo, cabeçalhos e chaves de índice rio abaixo.

Cadeia:

1. Env `AGENT_NAME` — presente e inválida → aborta **104**. (Nunca grava arquivo.)
2. Arquivo `$DATADIR/agent_name` — presente e inválido → aborta **104**.
3. Fallback `argv[0]`: `filepath.Base` + lowercase + remoção do sufixo `.exe`
   (pontos internos preservados). Resultado vazio → **103**; resultado que reprova
   na validação → **104**. "Vazio" cobre `""`, `"."` e o separador de caminho da
   plataforma: em Windows, `filepath.Base` de uma raiz devolve a contrabarra, não
   a barra — uma guarda fixa em `/` deixaria o caso escapar e trocaria o 103
   documentado por um 104.

Fallbacks determinísticos (`argv[0]`) **não** gravam arquivo.

## 9. AGENT_UUID

Formato canônico **com hífens**. Regex (v7 estrita):
`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`.
Sanear = só trim + lowercase (NÃO remove `-`).

Cadeia:
1. Env `AGENT_UUID` — presente e inválida → aborta **107**. (Nunca grava arquivo.)
2. Arquivo `$DATADIR/agent_uuid` (auto-gerido) — vazio/inválido → trata como
   ausente e regenera.
3. **Gerar:** `uuidv7.GenerateString(uuidv7.Level1)`; gravar em
   `$DATADIR/agent_uuid` (perm 0644, garantida, durável e atômica — ver §4).
   - Falha de geração → **105**; falha de gravação → **106**; valor gerado que
     reprova na validação → **107**.
   - Se outro processo criou o arquivo entre a leitura e a gravação, o valor
     dele é adotado.

O descarte de um `$DATADIR/agent_uuid` inválido gera **aviso obrigatório em
stderr** (ver §12).

## 10. HOSTNAME

Regex: `^[a-z0-9.-]+$` (após lowercase), acrescida das regras estruturais da
**RFC 1123**: no máximo 253 caracteres; rótulos (separados por `.`) de 1 a 63
caracteres; nenhum rótulo vazio; nenhum hífen no início ou no fim de um rótulo.
Sem elas, valores degenerados como `-`, `...`, `-host-` ou um nome de 300
caracteres passariam e virariam chave de agrupamento rio abaixo.

Cadeia:
1. Env `HOSTNAME` — presente e inválida → aborta **109**.
2. `os.Hostname()` — erro → **108**; valor inválido → **109**.

## 11. WORKSPACE

Regex: `^[a-z0-9.-]+$` (`^` corrigido do rascunho), com no máximo **64
caracteres**, e recusando `.` e `..` — pelo mesmo motivo da §8: `Workspace`
identifica o tenant e costuma virar componente de caminho ou chave de índice.

Cadeia:
1. Env `WORKSPACE` — presente e inválida → aborta **111**.
2. Arquivo `$DATADIR/workspace` — presente e inválido → aborta **111**.
3. Fallback `default` (não grava arquivo).

> O código **110** do rascunho foi **removido**: como há o fallback `default`,
> workspace nunca é fatal por ausência.

## 12. Observabilidade

- Silencioso por padrão no caminho de sucesso.
- Se a env `LOGHUB_IDENT_DEBUG` estiver definida (qualquer valor não vazio),
  `Initialize()` escreve em stderr **uma linha por campo** — os **seis**,
  `DATADIR` inclusive — indicando a origem (`env` / `file` / `generated` /
  `fallback`) **e o valor final**, no formato:

  ```
  lib-loghub-ident: debug: MACHINE_ID: env = "abcdef0123456789abcdef0123456789"
  ```

  As linhas são emitidas **antes** do tratamento de uma eventual falha: no
  caminho de erro elas são a única pista que o operador tem sobre qual fonte
  alimentou cada campo já resolvido.

- **Avisos operacionais** são emitidos em stderr **SEMPRE**, independentemente
  do modo debug, no formato:

  ```
  lib-loghub-ident: aviso: <mensagem>
  ```

  Casos de emissão:
  1. **Descarte e regeneração de identidade persistida:** quando um
     `$DATADIR/machine_id` ou `$DATADIR/agent_uuid` tem conteúdo inválido e a
     biblioteca assume a liderança para regenerar o valor.
  2. **Adoção de regeneração prévia:** quando uma réplica irmã descobre que a
     identidade corrompida já foi regerada por outro processo concorrente
     (arbitrado pelo registro `.regen`) e adota essa regeneração.

  Avisos de descarte protegem segredos potencialmente contidos no arquivo lido,
  reportando apenas tamanho e hash sanitizado em vez do conteúdo em claro.

## 13. Saída de erro

Em qualquer falha: escreve em stderr exatamente

```
lib-loghub-ident: <VARIÁVEL>: <motivo>
```

e chama `os.Exit(<código>)`.

| Código | Variável     | Motivo                                                              |
|--------|--------------|--------------------------------------------------------------------|
| 100    | `DATADIR`    | diretório necessário ausente, não é diretório, caminho relativo, erro de I/O / permissão, ou link simbólico recusado |
| 100    | `MACHINE_ID_FILE` | caminho informado é relativo, inacessível, ou não é arquivo comum utilizável (diretório, fifo, device, > 4 KiB) |
| 102    | `MACHINE_ID` | env presente não casa com `^[0-9a-f]{32}$`                          |
| 103    | `AGENT_NAME` | todas as fontes vazias (`argv[0]` saneado ficou vazio)             |
| 104    | `AGENT_NAME` | valor não casa com `^[a-z0-9._-]+$`                                 |
| 105    | `AGENT_UUID` | falha na geração local de UUIDv7                                    |
| 106    | `AGENT_UUID` | gravação em `$DATADIR/agent_uuid` (0644) falhou                    |
| 107    | `AGENT_UUID` | valor não casa com a regex de UUIDv7                                |
| 108    | `HOSTNAME`   | env vazia e `os.Hostname()` retornou erro                          |
| 109    | `HOSTNAME`   | valor não casa com `^[a-z0-9.-]+$`                                  |
| 111    | `WORKSPACE`  | valor não casa com `^[a-z0-9.-]+$`                                  |
| 112    | (geral)      | `Initialize()` chamado mais de uma vez                             |
| 113    | `MACHINE_ID` | gravação em `$DATADIR/machine_id` (0644) falhou                    |
| 114    | `MACHINE_ID` | falha na geração local do UUIDv7 base do machine-id                 |

> Removidos em relação ao rascunho: **101** (machine-id sempre regenerável) e
> **110** (workspace tem fallback `default`). Adicionados: **113** e **114**.

> O **114** existe porque o **105** é, por esta tabela, exclusivo do
> `AGENT_UUID`. Emitir 105 numa falha de geração do `MACHINE_ID` levaria o
> operador a diagnosticar o campo errado.

A linha de erro é a **última** que a biblioteca escreve. Avisos operacionais
(§12) e, em modo debug, as linhas de diagnóstico podem precedê-la.

## 14. Entregáveis

- `system.go`, `system_unix.go`, `system_windows.go` — interface `system`, implementação `osSystem` e abertura imune a FIFO TOCTOU.
- `identity.go` — struct interna, getters, validadores manuais por bytes (sem `regexp`), constantes.
- `resolve.go` — lógica de resolução pura (`resolve`).
- `initialize.go` — `Initialize`, guarda de chamada única, stderr/exit, debug.
- `doc.go` — documentação de pacote.
- `*_test.go` — testes unitários (precedência, regex, códigos de saída) via
  `fakeSystem`.
- `README.md` — documentação em PT-BR.
- `skill/examples/minimal/` — exemplo executável mínimo.
