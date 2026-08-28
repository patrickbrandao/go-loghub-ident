# REPORT-03 — Revisao de bugs e melhorias (complementar ao REPORT-01)

> Revisao completa de `go-loghub-ident` na `main` @ HEAD.
> Data: 2026-08-27. Este relatorio eh complementar ao REPORT-01 e cobre
> achados NOVOS — que nao estavam catalogados na revisao anterior.

---

## Resumo executivo

A segunda passagem pelo codigo revelou **4 novos bugs** e **12 melhorias**
que nao constavam no REPORT-01. Nenhum dos novos achados tem severidade
critica, mas dois deles (BUG-13 e BUG-14) representam violacoes do contrato
da SPEC. As melhorias sao majoritariamente de higiene de codigo, robustez e
manutenibilidade.

| Sev.     | # | Achados |
|----------|---|---------|
| Amarelo Medio | 2 | BUG-13, BUG-14 |
| Azul Baixo | 2 | BUG-15, BUG-16 |
| Ferramenta Melh | 12 | IMP-06 … IMP-17 |

---

## Metodologia

- Releitura completa de `identity.go`, `resolve.go`, `system.go`,
  `initialize.go`.
- Auditoria de cada caminho de codigo confrontado com a SPEC.
- Execucao de todos os testes de integracao (`go test -short ./...` e
  `go test -race ./tests/`).
- Verificacao de `go vet` e `gofmt` — **limpos**.
- **Nota:** `resolve_test.go` foi removido da raiz e todos os testes
  residem exclusivamente em `./tests/`. Os 2 unicos testes que nao podiam
  ser adaptados para black-box (simulacao de falha de UUID gen/codigo 105
  e falha de os.Hostname/codigo 108) estao documentados como inalcancaveis
  de fora em `tests/exitcodes_test.go`.

**Estado da suite:** todos os testes em `./tests/`, passando sem `resove_test.go` na raiz. 12 `TestFix_*` pulados do REPORT-01 + 4 novos `TestFix_*` do REPORT-03.

---

# BUGS

## Amarelo BUG-13 — `DataDir` nao eh normalizado com `filepath.Clean`

**Onde:** [`resolve.go:161-167`](../resolve.go#L161) (`resolveDataDirPath`).

A funcao retorna o valor da env `DATADIR` — ou o default `/data` — sem
chamar `filepath.Clean`. Caminhos com barras duplas, `.` ou `..` sao
armazenados literalmente na struct de identidade e devolvidos por
`DataDir()`.

### Evidencia

```bash
DATADIR=/tmp//loghub MACHINE_ID=abc... AGENT_NAME=svc ... ./probe
# DataDir: /tmp//loghub        <- sem filepath.Clean
```

Os usos internos (`readDataFile`, `writeDataFile`) passam o caminho por
`filepath.Join`, que chama `Clean` — entao a lib funciona internamente.
Mas o valor publico exposto por `DataDir()` nao eh limpo.

### Impacto

Consumidor que usa `DataDir()` para compor caminhos sem passar por
`filepath.Join` (ex.: concatenacao simples, logging, exibicao em UI) ve
barras duplas e componentes `..` nao resolvidos. Em modo debug, o path
sujo eh registrado em stderr. Se o valor for usado como chave de cache ou
de identidade, diferencas de formatacao (`/data` vs `/data/`) produzem
chaves distintas para o mesmo diretorio.

### Correcao sugerida

```go
func (r *resolver) resolveDataDirPath() string {
    dir := r.env("DATADIR")
    if dir == "" {
        return DefaultDataDir
    }
    return filepath.Clean(dir)
}
```

**Testes:** `TestChar_BUG13_DataDirNotCleaned` e `TestFix_BUG13_DataDirCleaned` (integracao, em `tests/bugs_test.go`).

---

## Amarelo BUG-14 — Env values skip `firstLine` processing

**Onde:** `resolve.go:181` (`resolveMachineID`), `resolve.go:242`
(`resolveAgentName`), `resolve.go:292` (`resolveAgentUUID`),
`resolve.go:337` (`resolveHostname`), `resolve.go:361` (`resolveWorkspace`).

A SPEC §5 diz: *"Para cada fonte: ler 1a linha -> trim whitespace ->
forcar lowercase -> sanear -> validar regex"*. As fontes de ARQUIVO
seguem esse pipeline (via `readFile` -> `firstLine`). As fontes de ENV
pulam o passo `firstLine`: o valor inteiro da variavel eh usado.

### Evidencia

```bash
# Arquivo com multiplas linhas — primeira linha eh usada (OK per spec):
echo -e "valid-name\nextra" > $DATADIR/agent_name
# -> AgentName: valid-name        # primeira linha

# Env com multiplas linhas — valor inteiro reprovado (inconsistente):
AGENT_NAME=$'valid-name\nextra' ./probe
# -> exit=104 (era para extrair "valid-name" e aceitar)
```

### Impacto

Baixo na pratica: definir env com newline embutida exige esforco
deliberado (`$'...'` em bash, API de container). Mas eh uma discrepancia
entre o contrato da SPEC e a implementacao real — duas fontes do mesmo
campo tem pipelines de saneamento diferentes.

### Correcao sugerida

Aplicar `firstLine` tambem aos valores vindos de env, ou documentar
explicitamente que envs nao passam por `firstLine`. A segunda opcao eh
mais segura (nao quebra retrocompatibilidade), mas a primeira honra o
texto da SPEC.

**Testes:** `TestChar_BUG14_EnvSkipsFirstLine` e `TestFix_BUG14_FirstLineAppliedToEnv`
(integracao).

---

## Azul BUG-15 — `Stat` em `MACHINE_ID_FILE` engole erros que nao sao
"not found"

**Onde:** [`resolve.go:195`](../resolve.go#L195).

```go
} else if _, err := r.sys.Stat(midFile); err != nil {
    midFile = DefaultMachineIDFile
}
```

Qualquer erro de `Stat` — permissao negada, I/O, caminho muito longo —
eh tratado como "arquivo inexistente" e a lib cai silenciosamente para
`/etc/machine-id`. A SPEC §7.2 diz: *"Se a env apontar para arquivo
inexistente, troca para /etc/machine-id"*. "Inexistente" nao eh "qualquer
erro".

### Evidencia

```bash
mkdir -m 000 /tmp/restricted
MACHINE_ID_FILE=/tmp/restricted/mid ./probe
# -> MachineID: <valor de /etc/machine-id ou gerado>
#   (EACCES no Stat foi silenciosamente ignorado)
```

### Impacto

Operador configura `MACHINE_ID_FILE` para um caminho com problema de
permissao e nao recebe diagnostico — a lib usa outro arquivo em silencio.
O machine-id usado nao eh o que o operador pretendeu, e nao ha rastro do
erro.

### Correcao sugerida

```go
} else if _, err := r.sys.Stat(midFile); err != nil {
    if errors.Is(err, fs.ErrNotExist) {
        midFile = DefaultMachineIDFile
    } else {
        return "", newFailure(100, "DATADIR",
            fmt.Sprintf("Stat em MACHINE_ID_FILE %q falhou: %v", midFile, err))
    }
}
```

**Testes:** `TestChar_BUG15_StatErrorSwallowed` e
`TestFix_BUG15_StatErrorReported` (integracao, via permissoes reais do
SO).

---

## Azul BUG-16 — Classes de caracteres ambiguas nas regexes

**Onde:** [`identity.go:52-53`](../identity.go#L52).

```go
reHostname  = regexp.MustCompile(`^[a-z0-9.-]+$`)
reWorkspace = regexp.MustCompile(`^[a-z0-9.-]+$`)
```

`.-` dentro de `[]` eh tecnicamente um operador de range entre `.` (ASCII
46) e `-` (ASCII 45). Eh um range reverso (vazio), que Go trata como
dois caracteres literais. Funciona, mas eh fragil: se o codigo for
portado para outro motor de regex, ou se o comportamento do Go mudar
(improvavel mas possivel), a regex para de funcionar.

`reAgentName = ^[a-z0-9._-]+$` tem `_-` na mesma situacao.

### Correcao sugerida

Colocar o hifen no inicio da classe (onde nunca eh range) ou escapa-lo:

```go
reAgentName = regexp.MustCompile(`^[-a-z0-9._]+$`)
reHostname  = regexp.MustCompile(`^[-a-z0-9.]+$`)
reWorkspace = regexp.MustCompile(`^[-a-z0-9.]+$`)
```

Mudanca semantica nenhuma — o conjunto de caracteres aceitos eh identico.

**Testes:** `TestFix_BUG16_RegexClassesAreClean` (regressao de formato,
em `tests/bugs_test.go`).

---

# MELHORIAS

## Ferramenta IMP-06 — Expor `IsInitialized() bool`

O `atomic.Bool` `initialized` ja existe em `initialize.go`. Expo-lo como
getter publico permite que consumidores validem o contrato happens-before
em runtime:

```go
func IsInitialized() bool { return initialized.Load() }
```

Zero custo, zero alocacoes.

## Ferramenta IMP-07 — Expor linhas de debug programaticamente

Hoje as linhas de diagnostico so sao acessiveis via stderr com
`LOGHUB_IDENT_DEBUG`. Um getter `Debug() []string` (ou `Debug() string`
com join) permitiria logging estruturado, exportacao para metricas ou
inclusao em resposta de health-check.

## Ferramenta IMP-08 — Regenerar `examples/minimal/go.sum`

O arquivo contem apenas 2 linhas (para `go-loghub-uuid`). Deveria ser
regenerado com `go mod tidy` para incluir hashes de todas as dependencias
transitivas. Nao quebra nada hoje por causa do `replace`, mas eh
incompleto.

## Ferramenta IMP-09 — `fakeSystem.WriteFile` ignora o parametro de permissao

```go
func (f *fakeSystem) WriteFile(path string, data []byte, _ os.FileMode) error {
```

O `_ os.FileMode` descarta a permissao. Testes unitarios nao conseguem
verificar se a permissao correta (0644) esta sendo passada nas chamadas
a `WriteFile`. Armazenar `perm` no fake permitiria testar isso.

## Ferramenta IMP-10 — `BenchmarkBoot_AllFromEnv` cria `&testing.T{}` invalido

Em `tests/bench_test.go:197`:

```go
t := &testing.T{}
```

`testing.T` nao foi projetado para criacao manual. Se `run()` chamar
`t.FailNow()`, o comportamento eh indefinido (goroutine termina mas
o benchmark nao para). Substituir por `testing.TB` adaptado ou por
um helper que nao dependa de `*testing.T`.

## Ferramenta IMP-11 — `writeDataFile` nao evita duplo `\n`

`writeDataFile` sempre apende `+"\n"`. Se o conteudo ja terminar
com `\n`, o arquivo fica com linha em branco ao final — `firstLine`
lida corretamente, mas o conteudo em disco fica com ruido.

```go
content = strings.TrimRight(content, "\n\r") + "\n"
```

## Ferramenta IMP-12 — Escrita atomica para arquivos de identidade

`writeDataFile` usa `os.WriteFile` (nao atomico). Um crash no meio
da escrita deixa o arquivo truncado — que o BUG-03 mostra ser o gatilho
para rotacao silenciosa de identidade. Escrever em arquivo temporario
e renomear (`os.Rename`) eh atomico no mesmo filesystem:

```go
tmp := path + ".tmp"
if err := sys.WriteFile(tmp, data, perm); err != nil {
    return err
}
return sys.Rename(tmp, path)
```

**Relacionado:** a correcao do BUG-04 (`O_EXCL` para geracao inicial)
ja resolve a corrida entre processos; a escrita atomica resolve o
problema de crash durante sobrescrita (regeneracao).

## Ferramenta IMP-13 — Adicionar CI com `go vet`, `gofmt`, `go test -race`

Ja mencionado em MEL-05 do REPORT-01. Reforcado aqui porque a suite
cresceu e vale ainda mais.

## Ferramenta IMP-14 — `writeDataFile` nao valida tipo de arquivo de destino

Se `$DATADIR/agent_uuid` existir como diretorio (criado por erro de
configuracao), `os.WriteFile` falha com "is a directory". A mensagem
de erro do SO pode nao ser clara. Vale verificar o tipo antes de
escrever.

## Ferramenta IMP-15 — Validar `HOSTNAME` contra RFC 1123

Ja parcialmente coberto pelo BUG-11. A validacao poderia incluir:
- Comprimento maximo de 253 caracteres
- Cada rotulo <= 63 caracteres
- Sem hifen no inicio ou fim de rotulo
- Sem rotulo vazio

## Ferramenta IMP-16 — `resolver` struct poderia ter `dataDirClean`

Em vez de chamar `filepath.Clean` toda vez que `r.dataDir` eh usado
(via `filepath.Join`, que ja limpa), armazenar o path ja limpo em
`r.dataDirClean` e usar esse nos logs e na struct de identidade.

## Ferramenta IMP-17 — Adicionar `debug` ao fakeSystem para testes

O fakeSystem nao tem um campo `debug` para verificar se `logf` foi
chamada corretamente. Adicionar `[]string` permitiria asserts mais
precisos sobre as linhas de diagnostico.

---

# COBERTURA DE TESTES

## Novos testes adicionados (todos na pasta `./tests/`)

| Arquivo | Teste | Proposito |
|---------|-------|-----------|
| `bugs_test.go` | `TestChar_BUG13_DataDirNotCleaned` | Caracterizacao BUG-13 |
| `bugs_test.go` | `TestFix_BUG13_DataDirCleaned` | Correcao BUG-13 |
| `bugs_test.go` | `TestChar_BUG14_EnvSkipsFirstLine` | Caracterizacao BUG-14 |
| `bugs_test.go` | `TestFix_BUG14_FirstLineAppliedToEnv` | Correcao BUG-14 |
| `bugs_test.go` | `TestChar_BUG15_StatErrorSwallowed` | Caracterizacao BUG-15 |
| `bugs_test.go` | `TestFix_BUG15_StatErrorReported` | Correcao BUG-15 |
| `bugs_test.go` | `TestFix_BUG16_RegexClassesAreClean` | Regressao BUG-16 |

**Total:** 3 testes de caracterizacao (passam hoje, falham quando
corrigido) + 4 testes de correcao (pulados).

**Nota:** `resolve_test.go` da raiz foi removido. Todos os testes que
continham cobertura equivalente em `./tests/` via o harness de subprocesso.
Os codigos 105 e 108 permanecem cobertos apenas por documentacao (falha de
UUID gen e falha de os.Hostname sao inalcancaveis de fora da lib).

---

# PLANO DE ACAO (combinado com REPORT-01)

| # | Item | Relatorio | Esforco |
|---|------|-----------|---------|
| — | BUG-01 … BUG-12, PERF-01/02, MEL-01…05 | REPORT-01 | — |
| + | **BUG-13** path do DataDir | REPORT-03 | P |
| + | **BUG-14** firstLine em envs | REPORT-03 | P |
| + | **BUG-15** erro de Stat engolido | REPORT-03 | P |
| + | **BUG-16** classes de regex | REPORT-03 | P |
| + | IMP-06 … IMP-17 | REPORT-03 | P (maioria) |

---

# APENDICE — Comandos

```bash
# Suite completa
go test -short ./...

# Caracterizacao (confirma os bugs atuais)
go test -race ./tests/ -run TestChar -v

# Validar correcoes
LOGHUB_IDENT_TEST_FIXES=1 go test ./tests/ -run TestFix -v

# Cobertura
GOCOVERDIR=$(mktemp -d) go test ./tests/ -coverpkg=github.com/patrickbrandao/go-loghub-ident
go tool covdata textfmt -i=$GOCOVERDIR -o=cover.out
go tool cover -func=cover.out
```
