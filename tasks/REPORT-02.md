# REPORT-02 — Revisão de bugs e performance complementar

> Revisão de `go-loghub-ident` expandindo o escopo do `REPORT-01.md`.
> Data: 2026-08-27. Foram identificados novos problemas, desde vazamentos de performance a comportamentos de concorrência e conformidade com a especificação (SPEC).

---

## Resumo executivo

| Sev.       | # | Achados |
|------------|---|---------|
| 🔴 Crítico | 1 | BUG-17 |
| 🟠 Alto    | 1 | BUG-19 |
| 🟡 Médio   | 1 | BUG-18 |
| ⚡ Perf     | 1 | PERF-03 |
| 🛠 Melhoria | 1 | MEL-06 |

Os novos defeitos documentados se concentram nas bordas da camada de persistência e em desvios entre a implementação e os códigos de saída definidos pela SPEC. A suíte de testes de integração e unitária foi atualizada para demonstrar as falhas detectadas.

---

# BUGS

## 🔴 BUG-17 — Arquivos de identidade são gravados sem fsync (risco de corrupção de SO)

**Onde:** `system.go:52` e `resolve.go:146` (`WriteFile`).

A gravação dos arquivos de identidade (`machine_id` e `agent_uuid`) na pasta `$DATADIR` utiliza diretamente `os.WriteFile`, que executa o fluxo `open(O_WRONLY|O_CREATE) -> write -> close`. 
A ausência de uma chamada `fsync` (ou método `f.Sync()`) antes do fechamento indica que os dados não estão garantidos de terem sido descarregados no disco persistente. 

### Impacto
Em um cenário de falha de hardware ou *kernel panic* após o boot do software, o arquivo persistente pode ser danificado, ficando com tamanho zero (`0 bytes`) ou com lixo (null bytes, `\x00`).
Em uma próxima inicialização, isso ativa a regra de rotação e sobrescreve a identidade do nó (levando ao cenário exposto pelo `BUG-03`). Para arquivos críticos de identidade persistente, a durabilidade (D do ACID) exige `f.Sync()`.

### Correção sugerida
Implementar um método alternativo em `osSystem` que abra o arquivo manualmente, grave, e certifique com `Sync()` antes do `Close()`. Combinar isso idealmente com a solução atômica de criar arquivos no BUG-04.


## 🟠 BUG-19 — Erro de I/O em `$MACHINE_ID_FILE` é engolido silenciosamente

**Onde:** `resolve.go:199` e `resolve.go:94`.

A SPEC determina que falha e conteúdo ausente ou inválido no fallback de `MACHINE_ID_FILE` (default: `/etc/machine-id`) faça o sistema continuar (cair) para o Nível 3, o `$DATADIR/machine_id`. No entanto, erros duros de disco, como Permissão Negada ou Bad Sector durante o `sys.Stat(midFile)` ou `sys.ReadFile`, não deveriam ser silenciados, da mesma forma que ocorrem para o `DATADIR`.

### Evidência

```bash
touch /tmp/restrito && chmod 000 /tmp/restrito
MACHINE_ID_FILE=/tmp/restrito ./probe
```
Resultado: A biblioteca trata `/tmp/restrito` não como um erro, mas sim assumindo silenciosamente que a fonte falhou ou não existe, pulando direto pro `$DATADIR`.

### Impacto
Se um operador definir o caminho do `MACHINE_ID_FILE` mas as permissões do container ou de execução forem indevidas, ele não terá visibilidade do erro. O comportamento correto, ao se deparar com erros de I/O legítimos (`os.ErrPermission`, etc.), deve ser gerar falha clara e fatal, ou um aviso antes de pular ao fallback.

### Correção sugerida
Diferenciar erros de I/O genuínos da ausência (`os.IsNotExist`).

---

## 🟡 BUG-18 — Código de saída 105 é usado indevidamente para erro de geração de `MACHINE_ID`

**Onde:** `resolve.go:222-225`.

Durante a geração de fallback de `MACHINE_ID`, o código atual faz:
```go
	uuid, err := r.sys.GenerateUUIDv7()
	if err != nil {
		return "", newFailure(105, "MACHINE_ID", ...)
```
A [SPEC.md](SPEC.md) seção 13 define o código de erro 105 exclusivamente para:
`105 | AGENT_UUID | falha na geração local de UUIDv7`

### Impacto
Se a biblioteca não conseguir gerar um `MACHINE_ID` por falta de entropia (embora improvável, simulável com fakeSystem em unit tests), o processo emitirá o código `105`. Operadores lendo a tabela oficial esperarão que `105` signifique estritamente falha em `AGENT_UUID`, levando a diagnósticos enganosos. Não há um código de falha documentado para a não-geração do `MACHINE_ID`.

### Correção sugerida
Alinhar implementação com especificação. Criar um novo código (ex: `114`) para falha na geração de `MACHINE_ID` na tabela da SPEC, ou expandir a definição de `105` para contemplar ambos.

---

# PERFORMANCE

## ⚡ PERF-03 — Função `firstLine` aloca strings grandes desnecessariamente

**Onde:** `resolve.go:83-89`.

Ao sanitizar qualquer arquivo de entrada lido (que pode chegar a 4KB, caso implementado o fix do `BUG-01`), a função faz:
```go
text := string(data)
if i := strings.IndexAny(text, "\r\n"); i >= 0 {
    text = text[:i]
}
```

Isso copia todos os bytes recuperados (`data`) diretamente para o heap como `string` de uma só vez, criando alocações grandes sem necessidade. 

### Correção sugerida
Manipular fatias no formato nativo `[]byte` (através do pacote `bytes`), realizando o trim. O cast `string(...)` deve acontecer unicamente nos poucos bytes finais que compõem a primeira linha do identificador.

```go
func firstLine(data []byte) string {
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		data = data[:i]
	}
	return string(bytes.TrimSpace(data))
}
```


---

# MELHORIAS

## 🛠 MEL-06 — Inicialização estrita "Lazy" ou estática (Sync.Once) de Regex

**Onde:** `identity.go`. 

Se as Expressões Regulares (`PERF-01`) forem mantidas pela legibilidade em detrimento da velocidade da validação manual, é uma boa prática não as pré-compilar no momento da carga do binário (função interna do pacote). `Initialize()` tem execução programática explicitada pela documentação. Utilizando `sync.Once` dentro de `Initialize()`, elimina-se o custo fixo de boot (`11,6 µs` e `355 allocs`) para os aplicativos que importam a biblioteca indiretamente mas não o ativam.

---
**Nota sobre os testes:**
Os testes reproduzindo `BUG-18` (falha na tabela de exit code) e `BUG-19` (I/O engolido em arquivo extra) foram adicionados ao pacote sob os arquivos de testes `resolve_test.go` e `tests/bugs2_test.go` respectivamente.
