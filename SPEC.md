# Especificação — `lib-loghub-ident`

> Biblioteca Go que resolve e expõe a identidade de um software dentro do
> ecossistema Loghub. Documento de especificação consolidado após a entrevista
> de design. Esta é a fonte de verdade da implementação.

## 1. Identidade do módulo

| Item                | Valor                                              |
|---------------------|----------------------------------------------------|
| Módulo Go           | `github.com/patrickbrandao/go-loghub-ident`        |
| Cláusula `package`  | `loghubident`                                      |
| Alias de uso        | `lhident`                                          |
| Versão mínima de Go | 1.22 (exigida pela dependência `go-loghub-uuid`)   |
| Dependência externa | `github.com/patrickbrandao/go-loghub-uuid`         |

> Observação: o rascunho pedia Go 1.21+. A dependência de UUID exige 1.22, então
> esta é a versão mínima efetiva.

**Convenção de idioma:** nomes de arquivos e código-fonte em inglês; comentários,
documentação e esta especificação em português do Brasil.

## 2. API pública

A biblioteca expõe **apenas funções getter**. A struct interna que guarda os
valores **não é exportada** — não há acesso a campos públicos.

```go
func Initialize()        // resolve a identidade; encerra o processo em falha

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

## 4. Arquitetura interna (testabilidade)

A lógica de resolução é **pura** e separada da saída do processo:

```go
type system interface {        // abstração de I/O, injetável em testes
    Getenv(key string) string
    Stat(path string) (os.FileInfo, error)
    ReadFile(path string) ([]byte, error)
    WriteFile(path string, data []byte, perm os.FileMode) error
    Hostname() (string, error)
    Args() []string
    GenerateUUIDv7() (string, error)
}

type failure struct { code int; variable, reason string }

func resolve(sys system) (*identity, *failure)
```

- `osSystem{}` é a implementação real (usa `os`, `filepath` e
  `loghubuuid.GenerateString(loghubuuid.Level1)`).
- `Initialize()` chama `resolve(osSystem{})`; em erro, escreve em stderr e chama
  `os.Exit(failure.code)`.
- Os testes injetam um `fakeSystem` com env/arquivos/hostname/argv em memória e
  inspecionam `(identity, failure)` sem matar o processo.

## 5. Pipeline de resolução por campo

Para cada fonte: **ler 1ª linha → trim whitespace → forçar lowercase → sanear →
validar regex**.

- **Sanear** é mínimo: trim + lowercase para todos os campos. A remoção de `-`
  aplica-se **somente ao `MACHINE_ID`**. Nenhum outro caractere é removido — lixo
  reprova na regex (falha alto).
- **Ausência vs. invalidez:**
  - Fonte **ausente ou vazia** após trim → cai para a próxima fonte.
  - Fonte de env **presente mas inválida** → **aborta** com o código do campo.
  - Exceção (arquivos auto-geridos pela lib — `machine_id`, `agent_uuid` em
    `$DATADIR`): conteúdo vazio **ou** inválido → trata como ausente e
    **regenera + regrava**.

Nenhum valor final pode ser vazio ou reprovado pela regex.

## 6. DATADIR

- Env: `DATADIR`. Padrão: `/data`.
- **Validação preguiçosa (lazy):** o diretório só é validado (existe e é
  diretório) quando um arquivo de fallback em `$DATADIR` precisa ser realmente
  lido ou gravado. No caminho feliz (todas as envs presentes), `$DATADIR` nunca é
  tocado e sua ausência **não** é fatal — suporta filesystem read-only.
- A biblioteca **não cria** o diretório; o orquestrador deve montar o volume.
- Falha (não existe / não é diretório) quando necessário → **código 100**.
- Erro de **I/O real** ao ler um arquivo dentro de `$DATADIR` (ex.: permissão
  negada, disco com falha) também aborta com **código 100** — não é mascarado
  como fonte vazia. Apenas "arquivo ausente" cai para a próxima fonte.

## 7. MACHINE_ID

Regex: `^[0-9a-f]{32}$` (após remover `-` e aplicar lowercase).

Cadeia de resolução:
1. Env `MACHINE_ID` — se presente e **inválida** → aborta **102**.
2. Arquivo `$MACHINE_ID_FILE` (env; padrão `/etc/machine-id`). Se a env apontar
   para arquivo inexistente, troca para `/etc/machine-id`. Conteúdo inválido
   neste nível → **cai** (não aborta).
3. Arquivo `$DATADIR/machine_id` (auto-gerido). Vazio/inválido → cai.
4. **Gerar:** UUIDv7 (`Level1`) com hífens removidos → 32 hex; gravar em
   `$DATADIR/machine_id` (perm 0644).
   - Falha de gravação → **código 113**.

> O código **101** do rascunho foi **removido**: como a cadeia sempre termina em
> geração, machine-id nunca é fatal por arquivo ausente.

## 8. AGENT_NAME

Regex: `^[a-z0-9._-]+$`.

Cadeia:
1. Env `AGENT_NAME` — presente e inválida → aborta **104**. (Nunca grava arquivo.)
2. Arquivo `$DATADIR/agent_name` — presente e inválido → aborta **104**.
3. Fallback `argv[0]`: `filepath.Base` + lowercase + remoção do sufixo `.exe`
   (pontos internos preservados). Resultado vazio → **103**; resultado que reprova
   na regex → **104**.

Fallbacks determinísticos (`argv[0]`) **não** gravam arquivo.

## 9. AGENT_UUID

Formato canônico **com hífens**. Regex (v7 estrita):
`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`.
Sanear = só trim + lowercase (NÃO remove `-`).

Cadeia:
1. Env `AGENT_UUID` — presente e inválida → aborta **107**. (Nunca grava arquivo.)
2. Arquivo `$DATADIR/agent_uuid` (auto-gerido) — vazio/inválido → trata como
   ausente e regenera.
3. **Gerar:** `loghubuuid.GenerateString(loghubuuid.Level1)`; gravar em
   `$DATADIR/agent_uuid` (perm 0644).
   - Falha de geração → **105**; falha de gravação → **106**; valor gerado que
     reprova na regex → **107**.

## 10. HOSTNAME

Regex: `^[a-z0-9.-]+$` (após lowercase).

Cadeia:
1. Env `HOSTNAME` — presente e inválida → aborta **109**.
2. `os.Hostname()` — erro → **108**; valor inválido → **109**.

## 11. WORKSPACE

Regex: `^[a-z0-9.-]+$` (`^` corrigido do rascunho).

Cadeia:
1. Env `WORKSPACE` — presente e inválida → aborta **111**.
2. Arquivo `$DATADIR/workspace` — presente e inválido → aborta **111**.
3. Fallback `default` (não grava arquivo).

> O código **110** do rascunho foi **removido**: como há o fallback `default`,
> workspace nunca é fatal por ausência.

## 12. Observabilidade

- Silencioso por padrão no caminho de sucesso.
- Se a env `LOGHUB_IDENT_DEBUG` estiver definida (qualquer valor não vazio),
  `Initialize()` escreve em stderr **uma linha por campo** indicando a origem
  (`env` / `file` / `generated` / `fallback`) e o valor final.

## 13. Saída de erro

Em qualquer falha: escreve em stderr exatamente

```
lib-loghub-ident: <VARIÁVEL>: <motivo>
```

e chama `os.Exit(<código>)`.

| Código | Variável     | Motivo                                                              |
|--------|--------------|--------------------------------------------------------------------|
| 100    | `DATADIR`    | diretório não existe / não é diretório, ou erro de I/O ao ler arquivo |
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

> Removidos em relação ao rascunho: **101** (machine-id sempre regenerável) e
> **110** (workspace tem fallback `default`). Adicionado: **113**.

## 14. Entregáveis

- `system.go` — interface `system` e implementação `osSystem`.
- `identity.go` — struct interna, getters, regexes, constantes.
- `resolve.go` — lógica de resolução pura (`resolve`).
- `initialize.go` — `Initialize`, guarda de chamada única, stderr/exit, debug.
- `doc.go` — documentação de pacote.
- `*_test.go` — testes unitários (precedência, regex, códigos de saída) via
  `fakeSystem`.
- `README.md` — documentação em PT-BR.
- `examples/minimal/` — exemplo executável mínimo.
