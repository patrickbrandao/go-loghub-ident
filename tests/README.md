# tests — suíte de integração

Testes que exercitam a **API pública** de `go-loghub-ident` contra o sistema
operacional real (sem o `fakeSystem`). Complementam `../resolve_test.go`, que
testa a função pura `resolve` com um `system` injetado.

| Arquivo             | Conteúdo                                                              |
|---------------------|-----------------------------------------------------------------------|
| `helper_test.go`    | `TestMain`, o harness de subprocesso e os utilitários de ambiente     |
| `api_test.go`       | caminho feliz, precedência env → arquivo → fallback, persistência    |
| `exitcodes_test.go` | a tabela de códigos de saída da SPEC §13, ponta a ponta               |
| `bugs_test.go`      | regressões dos defeitos de [`REPORT-01`](../tasks/REPORT-01.md) e [`REPORT-03`](../tasks/REPORT-03.md) |
| `bugs2_test.go`     | regressões dos defeitos de [`REPORT-02`](../tasks/REPORT-02.md)       |
| `bench_test.go`     | custo dos getters, da validação por regex e de um boot completo       |

## Como funciona

`Initialize()` encerra o processo com `os.Exit`, então não pode ser chamada
dentro do processo de teste. `TestMain` re-executa o próprio binário de teste
com `LOGHUB_IDENT_HELPER=1`; nesse modo ele chama `Initialize()`, imprime os
seis valores e termina. Os testes inspecionam código de saída, stdout e stderr.

O subprocesso recebe um **ambiente limpo**: `HOSTNAME`, `DATADIR` e afins da
máquina de quem roda os testes não contaminam a resolução.

## Rodar

```bash
go test ./tests/
```

Com detector de corrida (a suíte cria dezenas de subprocessos concorrentes):

```bash
go test -race ./tests/
```

Sem o teste de corrida entre processos do BUG-04, que é o mais demorado:

```bash
go test -short ./tests/
```

## Cobertura

A cobertura real fica nos subprocessos, e o harness já cuida disso: o
percentual que o `go test` imprime abaixo já vem com os contadores dos
subprocessos somados.

```bash
go test ./tests/ -coverpkg=github.com/patrickbrandao/go-loghub-ident
```

> Não adianta exportar `GOCOVERDIR` para tentar capturar o perfil bruto: o
> `go test` sobrescreve essa variável no processo filho com um diretório
> interno próprio, e o diretório indicado na linha de comando fica vazio. Para
> chegar ao perfil bruto é preciso `go test -work` e olhar o `gocoverdir`
> dentro do diretório de build preservado.

## Testes de bug

Cada defeito catalogado nos relatórios de `../tasks/` tem um `TestFix_BUGxx`
permanente, que afirma o comportamento **corrigido**:

```bash
go test ./tests/ -run TestFix -v
```

Eles nasceram como pares `TestChar_`/`TestFix_`: o `TestChar_` afirmava o
comportamento **defeituoso** — passava enquanto o bug existisse e falhava com
`BUG-XX parece corrigido` no dia da correção — e o `TestFix_`, pulado por
padrão, afirmava o comportamento esperado depois dela. Com os defeitos
corrigidos, os `TestChar_` cumpriram seu papel e saíram; os `TestFix_` ficaram
para impedir que qualquer um dos bugs volte.

Dois códigos de saída não são alcançáveis por fora da biblioteca, porque exigem
que `GenerateUUIDv7` ou `os.Hostname` falhem: **105**/**114** (geração de UUID)
e **108** (hostname do SO). Eles são cobertos pelos testes unitários da raiz,
com o `fakeSystem`.

## Benchmarks

```bash
go test ./tests/ -bench=. -benchmem -run=XXX
```
