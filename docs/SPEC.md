# Especificação detalhada — loghub_ident

Documento de referência consumido sob demanda durante a implementação. Pareie com `references/reference-impl.go`.

## Helper de leitura de arquivo

Todas as variáveis que têm arquivo como fonte usam o mesmo helper:

```go
// readFirstLine retorna a primeira linha do arquivo já com whitespace trim.
// Se o arquivo está vazio retorna ("", nil). Em erro de I/O retorna ("", err).
func readFirstLine(path string) (string, error)
```

Regras:
1. `os.ReadFile(path)`.
2. Cortar tudo a partir do primeiro `\r` ou `\n` (primeira linha apenas).
3. `strings.TrimSpace` no que sobrou.
4. Não fazer saneamento de caracteres aqui — saneamento é responsabilidade da função `resolveXxx` de cada variável, porque cada uma tem regex próprio.

## Helper fatal

```go
func fatal(code int, variable, reason string) {
    fmt.Fprintf(os.Stderr, "lib-loghub-ident: %s: %s\n", variable, reason)
    os.Exit(code)
}
```

Sempre usar essa função para qualquer caminho de erro. Nunca chamar `os.Exit` direto sem passar pelo `fatal`, para garantir consistência da mensagem de stderr.

---

## DATADIR

**Fonte 1 (env):** `os.Getenv("DATADIR")` + `TrimSpace`.
**Fallback:** se vazio → `/data`.
**Validação:** `os.Stat(dir)` deve ter sucesso E `info.IsDir()` deve ser `true`.
**Em falha:**
```
lib-loghub-ident: DATADIR: "/data" não existe ou não é um diretório
```
→ `os.Exit(100)`.

> A biblioteca **não cria** o diretório. Container deve montar o volume antes.

---

## MACHINE_ID

**Fonte 1 (env):** `os.Getenv("MACHINE_ID")` + `TrimSpace`.
**Fonte 2 (arquivo):** se env vazio → `readFirstLine(resolveMachineIDFile())`.

A função `resolveMachineIDFile()` retorna o valor da env `MACHINE_ID_FILE` (após trim), ou `DefaultMachineIDFile` (`/etc/machine-id`) se a env estiver vazia.

**Saneamento:** `strings.ReplaceAll(raw, "-", "")` + `strings.ToLower`.
**Regex final:** `^[0-9a-f]{32}$`.

**Falhas:**
- Env vazio E arquivo ausente/ilegível/vazio → código **101**, motivo `env vazio e <caminho> ausente ou ilegível`.
- Valor obtido não casa o regex → código **102**, motivo `"<valor>" não casa com ^[0-9a-f]{32}$`.

> O arquivo de machine-id em distribuições padrão (systemd) já vem nesse formato exato (32 hex, sem hífen, lowercase, \n no final). O TrimSpace cuida do \n.
> O caminho do arquivo é configurável via env `MACHINE_ID_FILE` (padrão `/etc/machine-id`).

---

## AGENT_NAME

**Fonte 1 (env):** `os.Getenv("AGENT_NAME")` + `TrimSpace`.
**Fonte 2 (arquivo):** se vazio → `readFirstLine("$DATADIR/agent_name")`.
**Fonte 3 (fallback):** se ainda vazio:
1. `base := filepath.Base(os.Args[0])`.
2. `base = strings.ToLower(base)`.
3. Remover tudo fora de `[a-z0-9._-]`: usar `regexp.MustCompile(`[^a-z0-9._-]`).ReplaceAllString(base, "")`.
4. Se `base == ""` → código **103**, motivo `env vazio, arquivo ausente e argv[0] saneado ficou vazio`.

**Regex final:** `^[a-z0-9._-]+$` aplicado ao valor já em lowercase.

**Falha de validação:** código **104**, motivo `"<valor>" não casa com ^[a-z0-9._-]+$`.

> Atenção: o saneamento "remove caracteres inválidos" só se aplica ao fallback `argv[0]`. Para valores vindos de env ou de `$DATADIR/agent_name`, **não** remova caracteres silenciosamente — valide e falhe com 104 se vier sujo. Caso contrário, o operador não percebe que o arquivo `agent_name` foi escrito errado.

---

## AGENT_UUID

**Fonte 1 (env):** `os.Getenv("AGENT_UUID")` + `TrimSpace`.
**Fonte 2 (arquivo):** se vazio → `readFirstLine("$DATADIR/agent_uuid")`.
**Fonte 3 (gerar e gravar):** se ainda vazio:
1. `generateUUIDv4()` — gera UUID v4 via `crypto/rand` (RFC 4122). Em erro → código **105**.
2. `os.WriteFile("$DATADIR/agent_uuid", []byte(uuid+"\n"), 0644)` — em erro → código **106**.

**Saneamento:** `strings.ToLower` apenas.
**Regex final:**
```
^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$
```

**Falha de validação:** código **107**, motivo `"<valor>" não é um UUID v4 canônico`.

> A função `generateUUIDv4()` usa `crypto/rand.Read` para gerar 16 bytes aleatórios, seta os bits de versão (4) e variante (RFC 4122), e formata como string canônica em lowercase. O regex valida a versão (`4` no 13º char) e a variante (`8/9/a/b` no 17º char), além do tamanho/charset.

> **Importante:** se o valor veio do env ou do arquivo, **não** sobrescrever o arquivo. Só gravar no caminho "gerar". Isso preserva a identidade entre containers que compartilham o volume.

---

## HOSTNAME

**Fonte 1 (env):** `os.Getenv("HOSTNAME")` + `TrimSpace`.
**Fonte 2 (sistema):** se vazio → `os.Hostname()`.
- Em erro → código **108**, motivo `os.Hostname() falhou: <erro>`.

**Saneamento:** `strings.ToLower`.
**Regex final:** `^[a-z0-9.-]+$`.

**Falha de validação:** código **109**.

> Usar `os.Hostname()` em vez de ler `/proc/sys/kernel/hostname` ou `/etc/hostname` diretamente — é mais compatível e cobre o caso de containers em que esses arquivos podem não estar mapeados.

---

## WORKSPACE

**Fonte 1 (env):** `os.Getenv("WORKSPACE")` + `TrimSpace`.
**Fonte 2 (arquivo):** se vazio → `readFirstLine("$DATADIR/workspace")`.
**Fallback:** **não há**. Workspace é obrigatório.

**Falhas:**
- Env vazio E arquivo ausente/vazio → código **110**.
- Regex `^[a-z0-9.-]+$` não casa → código **111**.

**Saneamento:** `strings.ToLower`.

---

## Tabela de mensagens stderr (exatas)

Use esses textos verbatim para que logs sejam grepáveis em produção.

| Cód | Mensagem |
|-----|----------|
| 100 | `lib-loghub-ident: DATADIR: "<dir>" não existe ou não é um diretório` |
| 101 | `lib-loghub-ident: MACHINE_ID: env vazio e <caminho> ausente ou ilegível` |
| 102 | `lib-loghub-ident: MACHINE_ID: "<valor>" não casa com ^[0-9a-f]{32}$` |
| 103 | `lib-loghub-ident: AGENT_NAME: env vazio, arquivo ausente e argv[0] saneado ficou vazio` |
| 104 | `lib-loghub-ident: AGENT_NAME: "<valor>" não casa com ^[a-z0-9._-]+$` |
| 105 | `lib-loghub-ident: AGENT_UUID: geração de UUID v4 falhou: <erro>` |
| 106 | `lib-loghub-ident: AGENT_UUID: gravação em <path> falhou: <erro>` |
| 107 | `lib-loghub-ident: AGENT_UUID: "<valor>" não é um UUID v4 canônico` |
| 108 | `lib-loghub-ident: HOSTNAME: os.Hostname() falhou: <erro>` |
| 109 | `lib-loghub-ident: HOSTNAME: "<valor>" não casa com ^[a-z0-9.-]+$` |
| 110 | `lib-loghub-ident: WORKSPACE: env vazio e <path> ausente ou vazio` |
| 111 | `lib-loghub-ident: WORKSPACE: "<valor>" não casa com ^[a-z0-9.-]+$` |
| 112 | `lib-loghub-ident: init: Initialize() chamado mais de uma vez` |

---

## Esqueleto de testes

Como o contrato é "stderr + os.Exit", os testes precisam **isolar a chamada do `fatal`**. Há dois caminhos práticos:

### Opção A — Testes de unidade nas funções `resolveXxx`

Refatorar para que cada função interna retorne `(valor, código, motivo)` e que `Initialize` seja a única responsável por chamar `fatal`. Isso permite tabelas como:

```go
func TestResolveMachineID(t *testing.T) {
    tests := []struct {
        name      string
        env       string
        fileBody  string
        fileExist bool
        want      string
        wantCode  int
    }{
        {"env válido sem hífen",    "abcdef0123456789abcdef0123456789", "", false, "abcdef0123456789abcdef0123456789", 0},
        {"env com hífens",          "abcdef01-2345-6789-abcd-ef0123456789", "", false, "abcdef0123456789abcdef0123456789", 0},
        {"arquivo válido",          "", "deadbeef0123456789abcdef01234567\n", true, "deadbeef0123456789abcdef01234567", 0},
        {"env vazio sem arquivo",   "", "", false, "", 101},
        {"valor inválido",          "xyz", "", false, "", 102},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // setup env + fs temporário, invocar resolveMachineID, comparar
        })
    }
}
```

> Recomendação: manter `resolveXxx` internas chamando `fatal` direto (mais simples para o consumidor) **e** expor uma camada paralela `resolveXxxE` que retorna `(string, int, string)` apenas para testes, marcada com comentário "// não chamar fora de testes".

### Opção B — Testes de integração com subprocesso

Compila um pequeno binário de teste que apenas chama `loghub_ident.Initialize()`, e o teste roda o binário e inspeciona stderr + exit code via `exec.Cmd`. Mais fiel ao contrato, mais lento.

```go
func TestInitialize_BadMachineID(t *testing.T) {
    cmd := exec.Command("./testbin/loghub_ident-init")
    cmd.Env = append(os.Environ(),
        "MACHINE_ID=xyz",
        "DATADIR="+tmpDir,
        "WORKSPACE=prod",
    )
    var stderr bytes.Buffer
    cmd.Stderr = &stderr
    err := cmd.Run()
    if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 102 {
        t.Fatalf("esperava exit 102, got %v; stderr=%s", err, stderr.String())
    }
    if !strings.Contains(stderr.String(), "MACHINE_ID:") {
        t.Fatalf("stderr não contém MACHINE_ID: %s", stderr.String())
    }
}
```

### Casos mínimos a cobrir

Para cada variável: caminho feliz via env, caminho feliz via arquivo, caminho feliz via fallback (quando existe), env ausente + arquivo ausente, valor com formato inválido. Mais:
- DATADIR inexistente.
- DATADIR aponta para arquivo regular (não diretório).
- AGENT_UUID gerado é gravado em `$DATADIR/agent_uuid` com perm 0644.
- AGENT_UUID gerado: na segunda execução, é lido do arquivo (não regerado).
- Initialize chamado duas vezes → exit 112.

---

## Estrutura sugerida do repositório

```
lib-loghub-ident/
├── go.mod                       # module github.com/<org>/lib-loghub-ident; go 1.21
├── README.md
├── loghub_ident/
│   ├── doc.go                   # package doc
│   ├── identity.go              # Identity, ID, Initialize, fatal, helpers
│   ├── identity_internal_test.go
│   └── identity_test.go
└── examples/
    └── minimal/
        └── main.go
```

O pacote vive em subdiretório `loghub_ident/` para que o import path seja `github.com/<org>/lib-loghub-ident/loghub_ident` (mais legível que terminar em `lib-loghub-ident` com hífens).
