# REPORT-01 — Revisão de bugs e performance

> Revisão completa de `go-loghub-ident` na `main` @ `4554a4e`.
> Data: 2026-08-27. Toda constatação deste relatório foi **reproduzida
> executando o binário**, não apenas lida no código; a seção de cada achado
> traz o comando e a saída obtida.

---

## Resumo executivo

A biblioteca faz o que promete no caminho feliz: resolve os seis campos, aplica
a precedência env → arquivo → fallback, normaliza para lowercase e valida. Os
getters são de fato quase gratuitos (**0,56 ns/op, zero alocações** — a promessa
da SPEC §2 se confirma). O desenho com a interface `system` injetável é bom e os
testes unitários existentes são bem escritos.

Os problemas estão nas bordas — e, para uma biblioteca de **identidade**, as
bordas são o produto.

**Não há gargalo de performance no caminho quente.** Os ganhos disponíveis são
de custo fixo (tamanho de binário e tempo de carga do pacote) e, sobretudo, de
robustez. Este relatório não inventa problemas de velocidade onde não há.

| Sev.       | # | Achados |
|------------|---|---------|
| 🔴 Crítico | 1 | BUG-01 |
| 🟠 Alto    | 3 | BUG-02, BUG-03, BUG-04 |
| 🟡 Médio   | 3 | BUG-05, BUG-06, BUG-07 |
| 🔵 Baixo   | 5 | BUG-08 … BUG-12 |
| ⚡ Perf     | 2 | PERF-01, PERF-02 |
| 🛠 Melhoria | 5 | MEL-01 … MEL-05 |

Três achados de alta severidade compartilham a mesma raiz e é útil vê-los
juntos: **BUG-04** (gravação não atômica) produz arquivos truncados; **BUG-03**
faz a lib descartar esses arquivos truncados e **trocar a identidade em
silêncio**; **BUG-02** faz um `$DATADIR` ausente derrubar o processo em vez de
usar o fallback. Corrigir só um deixa o ciclo aberto.

### O que foi entregue junto

Suíte de integração em [`../tests/`](../tests/): **88 testes passando**, mais 12
`TestFix_*` pulados que descrevem o comportamento esperado após cada correção.
A suíte cobre **95,7%** das instruções do pacote (reprodutível), contra 85,5%
dos unitários da raiz — a diferença é justamente `Initialize` e `osSystem`, que
os unitários deixam em **0%**.

---

## Metodologia

- Leitura completa de `doc.go`, `identity.go`, `system.go`, `resolve.go`,
  `initialize.go` e `resolve_test.go`, confrontada com `SPEC.md` e `README.md`.
- Compilação de um binário consumidor real, exercitado com dezenas de
  combinações de ambiente, `$DATADIR`, permissões, `umask`, CWD e conteúdo de
  arquivo.
- Medição com `go test -bench`, `go tool cover` e observação de RSS.

**Ambiente:** Go 1.27.0, darwin/arm64 (Apple M2). `go vet ./...`, `gofmt -l .` e
`go test -race ./...` limpos antes e depois das mudanças.

---

# BUGS

## 🔴 BUG-01 — Leitura de arquivo sem limite: OOM ou travamento no boot

**Onde:** [`system.go:50`](../system.go#L50) (`osSystem.ReadFile`), consumido por
[`resolve.go:93`](../resolve.go#L93).

`os.ReadFile` é chamado sem qualquer limite de tamanho e sem verificar o **tipo**
do arquivo. O caminho lido vem de `MACHINE_ID_FILE`, uma **variável de ambiente**
— ou seja, controlada por quem faz o deploy, não pela biblioteca.

### Evidência

```bash
DATADIR=/tmp MACHINE_ID_FILE=/dev/zero AGENT_NAME=svc HOSTNAME=node01 WORKSPACE=prod ./probe
```

| t     | RSS       |
|-------|-----------|
| 1 s   | 3,6 GB    |
| 3 s   | 7,9 GB    |
| 6 s   | 8,9 GB    |

O processo **nunca termina**; foi morto com `kill -9`. Com um FIFO sem escritor,
o `open()` bloqueia e o boot **trava para sempre**, sem consumo de memória e sem
uma linha em stderr.

Não é só `/dev/zero`: qualquer arquivo grande (um log rotacionado para o caminho
errado), qualquer FIFO e qualquer device de caractere produzem o mesmo. E o
mesmo `readFile` atende `$DATADIR/machine_id`, `agent_name`, `agent_uuid` e
`workspace`.

### Impacto

Um erro de digitação em `MACHINE_ID_FILE` derruba o nó por consumo de memória em
segundos, ou pendura o processo indefinidamente. Em ambos os casos **sem
diagnóstico** — o operador vê um container que não sobe e nada mais.

### Correção sugerida

Rejeitar o que não é arquivo comum, e limitar a leitura. `os.Stat` resolve os
dois casos de forma portável, pois não abre o arquivo (e portanto não bloqueia
em FIFO):

```go
// maxIdentFileSize limita a leitura de qualquer fonte de identidade. Nenhum
// campo passa de ~40 bytes; 4 KiB é folga generosa e converte um arquivo
// hostil (FIFO, /dev/zero, log gigante) em "fonte inválida" em vez de OOM.
const maxIdentFileSize = 4 << 10

func (osSystem) ReadFile(path string) ([]byte, error) {
	// Stat NÃO abre o arquivo: um FIFO sem escritor é detectado sem bloquear.
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("open %s: não é um arquivo comum (%s)", path, info.Mode().Type())
	}
	if info.Size() > maxIdentFileSize {
		return nil, fmt.Errorf("open %s: %d bytes excedem o limite de %d",
			path, info.Size(), maxIdentFileSize)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxIdentFileSize))
}
```

O `LimitReader` cobre a janela TOCTOU entre `Stat` e `Open`.

**Testes:** `TestChar_BUG01_UnboundedReadHangs` (confirma o travamento hoje),
`TestFix_BUG01_BoundedRead`, `TestFix_BUG01_HugeFileRejected`.

---

## 🟠 BUG-02 — `$DATADIR` ausente torna fatais fallbacks que a SPEC promete infalíveis

**Onde:** [`resolve.go:122`](../resolve.go#L122) (`readDataFile` → `ensureDataDir`),
alcançado por [`resolve.go:253`](../resolve.go#L253) e
[`resolve.go:372`](../resolve.go#L372).

A SPEC justifica a remoção de dois códigos de erro exatamente com a existência de
fallbacks determinísticos:

> §11: *"O código **110** foi removido: como há o fallback `default`, workspace
> nunca é fatal por ausência."*
> §7: *"O código **101** foi removido: como a cadeia sempre termina em geração,
> machine-id nunca é fatal por arquivo ausente."*

Na prática, para **chegar** ao nível 3 é preciso **passar** pelo nível 2, e o
nível 2 chama `ensureDataDir`, que aborta com **100** se o diretório não existir.
O fallback é inalcançável.

### Evidência

```bash
# Tudo via env, exceto WORKSPACE. Só falta aplicar o literal "default".
env -u DATADIR -u WORKSPACE MACHINE_ID=abcdef0123456789abcdef0123456789 \
    AGENT_NAME=svc AGENT_UUID=019e99e3-42f0-7882-9719-2305ff84949c \
    HOSTNAME=node01 ./probe
```
```
lib-loghub-ident: DATADIR: "/data" não existe ou não é um diretório
exit=100
```

O mesmo acontece com `AGENT_NAME`, cujo nível 3 é `argv[0]` — que também não
depende de disco algum.

### Impacto

Duplo. Operacionalmente: rodar o binário em qualquer máquina sem `/data` (todo
laptop de desenvolvedor, todo container sem volume montado) falha com um erro que
aponta para o diretório errado como culpado. E some com a promessa de suporte a
filesystem read-only da SPEC §6 e do README — ela só vale se **todas** as envs
estiverem presentes, o que o README não diz.

### Correção sugerida

Separar leitura de gravação. Um `$DATADIR` inexistente é "fonte ausente" na
**leitura** e continua fatal na **gravação**:

```go
// readDataFile lê um arquivo de $DATADIR. Um diretório inexistente é tratado
// como fonte ausente — o campo segue para o próximo nível da cadeia. Erro de
// I/O REAL continua abortando com 100 (SPEC §6).
func (r *resolver) readDataFile(name string) (string, *failure) {
	if !r.dataDirExists() { // sem abortar
		r.logf("%s: $DATADIR ausente, fonte ignorada", name)
		return "", nil
	}
	...
}

// writeDataFile continua chamando ensureDataDir: gerar identidade sem onde
// persistir é uma falha legítima.
```

> ⚠️ Isto **muda o contrato** da SPEC §6. As duas leituras são defensáveis; o que
> não se sustenta é a contradição atual entre §6 e §7/§11. Se a decisão for
> manter o comportamento, então §7 e §11 precisam ser corrigidas — e o README
> deve dizer que `$DATADIR` é obrigatório sempre que alguma env faltar.

**Testes:** `TestChar_BUG02_MissingDataDirKillsWorkspaceFallback`,
`TestChar_BUG02_MissingDataDirKillsArgv0Fallback`,
`TestFix_BUG02_FallbacksSurviveMissingDataDir`,
`TestFix_BUG02_WriteStillRequiresDataDir`.

---

## 🟠 BUG-03 — Rotação silenciosa de identidade

**Onde:** [`resolve.go:213-235`](../resolve.go#L213) e
[`resolve.go:306-330`](../resolve.go#L306).

A regra da SPEC §5 — arquivo auto-gerido inválido é tratado como ausente e
regenerado — está implementada. O problema é que ela é executada **em completo
silêncio**: a identidade persistida é descartada, uma nova é gravada por cima, e
o processo segue sem emitir nada. Fora do modo debug, não há registro algum.

### Evidência

Um byte NUL residual — exatamente o que uma gravação truncada por crash deixa:

```bash
printf 'abcdef0123456789abcdef0123456789\x00' > $DD/machine_id
DATADIR=$DD MACHINE_ID_FILE=/nao/existe AGENT_NAME=svc HOSTNAME=node01 WORKSPACE=prod ./probe
```
```
MachineID=01a04494d6b37d6f85955b02f5232338   ← trocado
stderr: (vazio)                              ← nenhum aviso
```
O arquivo foi sobrescrito com o valor novo. `firstLine` só remove `\r`, `\n` e
espaços (ver BUG-09), então o NUL sobrevive, reprova na regex e dispara a
regeneração.

### Impacto

É o pior modo de falha possível para uma biblioteca de identidade. Um agente
volta com outro `machine_id`/`agent_uuid` depois de um `kill -9` no momento
errado; do lado do servidor Loghub ele aparece como uma **máquina nova**,
quebrando continuidade de séries, dedupe e licenciamento por host. E não há
nenhum rastro para correlacionar. Combinado com o BUG-04, o gatilho não é
hipotético: gravação não atômica **produz** arquivos truncados.

### Correção sugerida

Descartar identidade persistida é evento operacional, não detalhe de
diagnóstico. Deve ir para stderr **sempre**:

```go
// warnf registra um evento que o operador precisa ver mesmo fora do modo
// debug — hoje só existe logf, que é condicional.
func (r *resolver) warnf(format string, args ...any) {
	r.warnings = append(r.warnings, fmt.Sprintf(format, args...))
}

// no ponto do descarte:
r.warnf("MACHINE_ID: %s/%s tinha conteúdo inválido (%q) e foi REGERADO; "+
	"a identidade desta máquina mudou", r.dataDir, fileMachineID, v)
```

E em `Initialize`, antes do bloco de debug:

```go
for _, w := range id.warnings {
	fmt.Fprintln(os.Stderr, "lib-loghub-ident: aviso: "+w)
}
```

**Testes:** `TestChar_BUG03_CorruptMachineIDRotatesSilently`,
`TestFix_BUG03_RotationIsAnnounced`.

---

## 🟠 BUG-04 — Corrida entre processos e gravação não atômica

**Onde:** [`resolve.go:141-150`](../resolve.go#L141) (`writeDataFile`), com a
leitura em [`resolve.go:208`](../resolve.go#L208) e
[`resolve.go:302`](../resolve.go#L302).

A sequência é `readDataFile` (não achou) → gerar → `writeDataFile`. Entre a
leitura e a gravação não há exclusão mútua alguma, e `os.WriteFile` grava **no
lugar**, sem `tmp`+`rename`.

### Evidência

12 rodadas de 10 processos com largada simultânea sobre o mesmo `$DATADIR` vazio:

```
rodada 1: 2 UUIDs DISTINTOS entre 10 processos; 1 com identidade != arquivo em disco
rodada 3: 2 UUIDs DISTINTOS entre 10 processos; 1 com identidade != arquivo em disco
rodada 5: 3 UUIDs DISTINTOS entre 10 processos; 3 com identidade != arquivo em disco
=> rodadas com divergência: 3 de 12
```

Dois defeitos aparecem juntos: processos irmãos assumem identidades diferentes,
e alguns rodam com uma identidade que **não é** a persistida — no próximo boot
eles trocam de identidade sozinhos.

### Impacto

O cenário não é exótico: sidecar + container principal com o mesmo volume, um
ReplicaSet com PVC compartilhado, ou simplesmente um serviço reiniciando
enquanto outra instância sobe. A gravação in-place ainda deixa a porta aberta
para arquivos truncados se o processo morrer no meio da escrita — que é
exatamente o insumo do BUG-03.

### Correção sugerida

Deixar o filesystem arbitrar a corrida, com `O_CREATE|O_EXCL` (atômico em
POSIX), e reler o valor do vencedor:

```go
// CreateExclusive grava data apenas se path ainda não existir. Devolve
// (true, nil) se ESTE processo criou o arquivo e (false, nil) se outro
// chegou antes — deixando o filesystem arbitrar a corrida.
CreateExclusive(path string, data []byte, perm os.FileMode) (bool, error)

func (osSystem) CreateExclusive(path string, data []byte, perm os.FileMode) (bool, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return false, err
	}
	if err := f.Chmod(perm); err != nil { // ver BUG-05
		return false, err
	}
	return true, f.Sync()
}
```

No `resolveAgentUUID`, se `created == false`, reler o arquivo e adotar o valor de
lá — todos os processos convergem para o vencedor. Para o caminho de
**sobrescrita** (regeneração de arquivo corrompido), usar `tmp` + `os.Rename`,
que é atômico dentro do mesmo filesystem e nunca expõe conteúdo parcial.

**Testes:** `TestChar_BUG04_ConcurrentStartupRaces` (mede sem falhar — a corrida
é probabilística e um teste intermitente não ajudaria ninguém),
`TestFix_BUG04_ConcurrentStartupAgrees`.

---

## 🟡 BUG-05 — Permissão efetiva não é 0644: o umask se aplica

**Onde:** [`identity.go:28`](../identity.go#L28) (`filePerm = 0o644`),
[`resolve.go:146`](../resolve.go#L146).

`os.WriteFile` passa a permissão ao `open(2)`, onde ela ainda é mascarada pelo
umask do processo. `0644` é um **teto**, não uma garantia — mas a SPEC §7 e §9 a
declaram como o valor gravado.

### Evidência

```bash
( umask 077; DATADIR=$DD ... ./probe )
ls -la $DD
```
```
-rw-------  agent_uuid     ← esperado 0644
-rw-------  machine_id
```

### Impacto

Containers que rodam com umask restritivo (prática comum em imagens
endurecidas) gravam `0600`. Se um segundo processo com UID diferente compartilha
o volume — sidecar de coleta, job de manutenção — ele não consegue ler a
identidade, e o sintoma aparece longe da causa.

### Correção sugerida

`chmod` explícito depois de criar, como no trecho de `CreateExclusive` acima.
Alternativa: assumir e **documentar** que `0644` é o teto e o umask manda.
O que não funciona é a SPEC afirmar uma coisa e o código entregar outra.

**Testes:** `TestChar_BUG05_UmaskOverridesPerm`, `TestFix_BUG05_PermIsAlways0644`.

---

## 🟡 BUG-06 — `..` e valores gigantes passam na validação

**Onde:** [`identity.go:50,53`](../identity.go#L50) (`reAgentName`, `reWorkspace`).

`^[a-z0-9._-]+$` e `^[a-z0-9.-]+$` aceitam `.` e `..` — que são componentes de
caminho relativos — e não impõem limite de comprimento.

### Evidência

```bash
AGENT_NAME=.. WORKSPACE=.. ./probe
# AgentName=..  Workspace=..     exit=0

AGENT_NAME=$(python3 -c "print('a'*5000)") ./probe
# aceito, 5000 caracteres     exit=0
```

### Impacto

`AgentName` e `Workspace` identificam agente e tenant no ecossistema Loghub.
Qualquer consumidor que os use para compor um caminho, uma chave de índice ou um
tópico — `filepath.Join(base, Workspace())` é o uso óbvio — herda uma travessia
de diretório vinda de uma variável de ambiente. A biblioteca é o ponto certo
para barrar isso: é ela que promete entregar valores validados. O comprimento
sem teto vaza para nomes de arquivo, cabeçalhos e chaves de índice rio abaixo.

### Correção sugerida

```go
// maxFieldLen limita os campos textuais. 64 acomoda qualquer nome real de
// agente ou tenant e mantém os valores utilizáveis como componente de caminho,
// rótulo de métrica ou chave de índice.
const maxFieldLen = 64

// validField complementa a regex com as regras que ela não expressa: nada de
// componentes de caminho relativos e nada de valores sem teto.
func validField(s string, max int) bool {
	return s != "" && len(s) <= max && s != "." && s != ".."
}
```

**Testes:** `TestChar_BUG06_DotDotAccepted`, `TestChar_BUG06_NoLengthLimit`,
`TestFix_BUG06_RejectsTraversalAndOversize`.

---

## 🟡 BUG-07 — Modo debug não cumpre a SPEC §12

**Onde:** [`resolve.go:76`](../resolve.go#L76) (`logf`) e todas as chamadas;
[`initialize.go:45-49`](../initialize.go#L45).

A SPEC §12 pede *"uma linha por campo indicando a origem […] **e o valor
final**"*. São **seis** campos.

### Evidência

```bash
LOGHUB_IDENT_DEBUG=1 ... ./probe
```
```
lib-loghub-ident: debug: MACHINE_ID: generated
lib-loghub-ident: debug: AGENT_NAME: env
lib-loghub-ident: debug: AGENT_UUID: generated
lib-loghub-ident: debug: HOSTNAME: env
lib-loghub-ident: debug: WORKSPACE: env
```

Cinco linhas, não seis: **falta `DATADIR`**. E **nenhuma traz o valor final**.

Há ainda um terceiro problema, o mais incômodo na prática:

```bash
LOGHUB_IDENT_DEBUG=1 DATADIR=/nao/existe ./probe
```
```
lib-loghub-ident: DATADIR: "/nao/existe" não existe ou não é um diretório
```

Nenhuma linha de debug. `Initialize` só as imprime **depois** de `resolve`
retornar com sucesso ([`initialize.go:38-41`](../initialize.go#L38) sai antes) —
o diagnóstico é descartado justamente na falha, quando é a única coisa que o
operador tem.

### Correção sugerida

```go
// logf registra origem E valor final, como manda a SPEC §12.
r.logf("MACHINE_ID: env = %q", v)
r.logf("DATADIR: %s = %q", origem, r.dataDir)  // campo hoje ausente
```

E em `Initialize`, mover a emissão do debug para **antes** do tratamento da
falha, para que as linhas dos campos já resolvidos sobrevivam ao aborto.

**Testes:** `TestChar_BUG07_DebugOmitsValuesAndDataDir`,
`TestChar_BUG07_DebugLostOnFailure`, `TestFix_BUG07_DebugFollowsSpec`.

---

## 🔵 BUG-08 — `os.IsNotExist` não desembrulha erros

**Onde:** [`resolve.go:96`](../resolve.go#L96).

```go
if os.IsNotExist(err) {
    return "", nil   // fonte ausente: cai para o próximo nível
}
return "", err       // erro de I/O: aborta com 100
```

`os.IsNotExist` só entende `*PathError`, `*LinkError` e `*SyscallError` diretos.
Um erro embrulhado com `%w` — idioma padrão em Go — passa despercebido:

```
PathError direto               os.IsNotExist=true   errors.Is=true
PathError embrulhado com %w    os.IsNotExist=false  errors.Is=true   ← aqui
```

### Impacto

Baixo hoje, porque `osSystem.ReadFile` devolve o `*PathError` cru. Mas `system` é
uma interface **feita para ser substituída**: qualquer implementação alternativa
que acrescente contexto ao erro converte "arquivo ausente" em `exit 100`. A
própria doc do Go recomenda a troca. Correção de uma linha:

```go
if errors.Is(err, fs.ErrNotExist) {
```

**Teste:** não é observável de fora com o `osSystem` real; o lugar da regressão é
`resolve_test.go`, com um `fakeSystem` devolvendo `fmt.Errorf("ctx: %w", ...)`.

---

## 🔵 BUG-09 — `firstLine` não remove BOM nem bytes de controle

**Onde:** [`resolve.go:83-89`](../resolve.go#L83).

`strings.TrimSpace` não remove BOM UTF-8 (`\ufeff`) nem NUL.

```bash
printf '\xef\xbb\xbfsvc\n' > $DD/agent_name
```
```
lib-loghub-ident: AGENT_NAME: "\ufeffsvc" não casa com ^[a-z0-9._-]+$
exit=104
```

Um `agent_name` gravado por um editor do Windows — que põe BOM por padrão —
derruba o processo. O caso do NUL é o gatilho do BUG-03.

```go
func firstLine(data []byte) string {
	text := strings.TrimPrefix(string(data), "\ufeff") // BOM UTF-8
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		text = text[:i]
	}
	return strings.TrimFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	})
}
```

**Testes:** `TestChar_BUG09_BOMKillsProcess`, `TestFix_BUG09_BOMIsStripped`.

---

## 🔵 BUG-10 — `DATADIR` relativo é aceito

**Onde:** [`resolve.go:161-167`](../resolve.go#L161).

O valor não é validado como caminho absoluto. A identidade passa a depender do
diretório de trabalho:

```bash
cd /base/a && DATADIR=dados ./probe   # AgentUUID=01a0449b-870a-72ad-...
cd /base/b && DATADIR=dados ./probe   # AgentUUID=01a0449b-870e-74f7-...
```

Mesmo serviço, mesma configuração, duas identidades. Um `WorkingDirectory`
diferente no unit do systemd, ou um `chdir` da aplicação, e o agente vira outro.

**Correção:** exigir `filepath.IsAbs(dir)` e abortar com 100 se não for — ou
documentar explicitamente que o caminho é relativo ao CWD.

**Testes:** `TestChar_BUG10_RelativeDataDirFollowsCwd`,
`TestFix_BUG10_RelativeDataDirRejected`.

---

## 🔵 BUG-11 — `HOSTNAME` aceita valores que não são hostnames

**Onde:** [`identity.go:52`](../identity.go#L52).

`^[a-z0-9.-]+$` deixa passar `-`, `.`, `...`, `-host-` e nomes de 300 caracteres
— todos aceitos com `exit=0` e devolvidos por `Hostname()`. A RFC 1123 proíbe
rótulo vazio, hífen nas bordas e nomes acima de 253 caracteres.

**Correção:** validar rótulo a rótulo após a regex. Baixa severidade porque
depende de configuração errada a montante, mas o custo da validação é nulo e
`Hostname()` costuma virar chave de agrupamento rio abaixo.

**Testes:** `TestChar_BUG11_DegenerateHostnamesAccepted`,
`TestFix_BUG11_RejectsDegenerateHostnames`.

---

## 🔵 BUG-12 — Detecção de `argv[0]` vazio ignora o separador do Windows

**Onde:** [`resolve.go:275`](../resolve.go#L275).

```go
if base == "" || base == "." || base == "/" {
```

No Windows, `filepath.Base("C:\\")` devolve `\`, não `/`. O caso escapa da
guarda, reprova na regex e sai com **104** em vez de **103**. Cosmético — o
processo morre de qualquer jeito — mas o código de saída documentado fica
errado numa lib que se declara multiplataforma.

```go
if base == "" || base == "." || base == string(os.PathSeparator) {
```

---

# PERFORMANCE

> Antes de tudo: **o caminho quente já está ótimo.** Getters medidos em
> `0,5607 ns/op` com **zero alocações**, e `0,1170 ns/op` sob `RunParallel`. A
> promessa da SPEC §2 se confirma; não há o que otimizar ali. Os dois itens
> abaixo são de **custo fixo**, não de throughput.

```
BenchmarkGetter_AgentUUID-8      1000000000    0.5607 ns/op   0 B/op   0 allocs/op
BenchmarkGetter_AllSix-8          368217792    3.272  ns/op   0 B/op   0 allocs/op
BenchmarkGetter_Parallel-8       1000000000    0.1170 ns/op   0 B/op   0 allocs/op
```

## ⚡ PERF-01 — O pacote `regexp` custa ~379 KB e ~11,6 µs a todo importador

**Onde:** [`identity.go:48-54`](../identity.go#L48).

As cinco regexes são compiladas em `var` de pacote, ou seja, na **carga**. Todo
binário que importa `go-loghub-ident` paga esse custo — inclusive um que nunca
chame `Initialize()`.

| Medição                            | Valor                              |
|------------------------------------|------------------------------------|
| Compilar as 5 regexes (init)       | **11 622 ns**, 35 888 B, 355 allocs |
| Tamanho de binário com `regexp`    | 2 818 034 B                        |
| Tamanho sem `regexp`               | 2 429 746 B                        |
| **Diferença**                      | **388 288 B (~379 KB)**            |

E, por validação:

| Campo        | Regex     | Manual    | Ganho |
|--------------|-----------|-----------|-------|
| `MACHINE_ID` | 205,0 ns  | 19,38 ns  | 10,6× |
| `AGENT_NAME` | 242,6 ns  | 8,921 ns  | 27,2× |
| `AGENT_UUID` | 219,2 ns  | —         | —     |

As validações acontecem ~6 vezes por boot: em termos absolutos, ~1,3 µs. O ganho
que importa **não é esse** — é o custo fixo. As cinco regexes são classes de
caracteres simples, substituíveis por laços de bytes de ~10 linhas cada, e isso
elimina `regexp`/`regexp/syntax` da árvore de dependências.

**Ressalva honesta:** os 379 KB só se materializam se o binário consumidor não
importar `regexp` por outro motivo. Numa aplicação Go média ele já veio junto e a
economia de tamanho desaparece — mas os **11,6 µs e 355 alocações de init**
continuam sendo pagos em qualquer cenário. Para uma biblioteca destinada a rodar
em **todo** microserviço do ecossistema, vale a troca.

**Bônus:** os validadores manuais tornam natural embutir as regras de BUG-06 e
BUG-11 (comprimento, `..`, rótulos), que a regex não consegue expressar.

## ⚡ PERF-02 — `Stat` + `ReadFile` redundantes em `MACHINE_ID_FILE`

**Onde:** [`resolve.go:195-199`](../resolve.go#L195).

O código faz `Stat` para decidir se troca pelo caminho padrão e em seguida
`ReadFile`, que refaz o mesmo trabalho. Bastaria tentar ler e tratar
`fs.ErrNotExist` como "troca para o padrão" — uma syscall a menos e a janela
TOCTOU entre as duas some. Ganho desprezível em tempo (uma syscall no boot); vale
pela simplificação. Note que a correção do BUG-01 reintroduz um `Stat`
deliberado — nesse caso os dois itens devem ser resolvidos juntos, mantendo um
único `Stat`.

---

# MELHORIAS

## 🛠 MEL-01 — Reportar todas as falhas de configuração de uma vez

`resolve` retorna na **primeira** falha ([`resolve.go:43-62`](../resolve.go#L43)):

```bash
MACHINE_ID=xxx AGENT_NAME='nome invalido' AGENT_UUID=deadbeef \
HOSTNAME=host_ruim WORKSPACE='ws ruim' ./probe
```
```
lib-loghub-ident: MACHINE_ID: "xxx" não casa com ^[0-9a-f]{32}$
exit=102
```

Cinco variáveis erradas, uma reportada. O operador corrige, reinicia, descobre a
próxima — cinco ciclos de deploy para um `.env` errado.

Acumular as falhas, imprimir todas e sair com o código da primeira preserva a
tabela de códigos e transforma cinco ciclos em um. Baixo esforço, alto retorno
operacional.

## 🛠 MEL-02 — Oferecer uma via que não mate o processo

Hoje `Initialize()` é a única porta de entrada e ela chama `os.Exit`. Isso
impede testar a biblioteca em processo, embuti-la num binário que queira
degradar em vez de morrer, ou resolver a identidade dentro de um `TestMain`.

Aditivo, sem quebrar nada:

```go
// TryInitialize faz o mesmo que Initialize, mas devolve o erro em vez de
// encerrar o processo. Initialize passa a ser um invólucro dela.
func TryInitialize() error
```

O `failure` interno já tem tudo (`code`, `variable`, `reason`); basta fazê-lo
implementar `error` e expor o código por um `ExitCode() int`.

## 🛠 MEL-03 — `$DATADIR/machine_id` deveria vencer `/etc/machine-id`?

Questão de desenho, não bug — o código segue a SPEC §7 à risca. Mas vale a
pergunta: em container, `/etc/machine-id` (nível 2) vem da **imagem** e muda a
cada rebuild, enquanto `$DATADIR/machine_id` (nível 3) vive no **volume
persistente**. A ordem atual faz o valor efêmero ter precedência sobre o
durável, e um rebuild da imagem troca o `machine_id` de todos os agentes.
Se a intenção é identidade estável, a ordem talvez devesse ser invertida — ou,
no mínimo, o valor lido de `/etc/machine-id` deveria ser copiado para
`$DATADIR/machine_id` no primeiro boot.

## 🛠 MEL-04 — Falha de gravação derruba um boot que já tem identidade válida

Nos códigos **106** e **113**, a identidade **já foi resolvida com sucesso**; o
que falhou foi persistir. Num filesystem read-only — o cenário que a SPEC §6 diz
querer suportar — o processo morre apesar de ter um valor perfeitamente válido em
mãos. Vale considerar degradar para aviso (a identidade vale para esta execução,
mas não sobrevive ao restart), mantendo o aborto como opção configurável. Mudança
de contrato: exige atualizar a SPEC.

## 🛠 MEL-05 — Higiene de repositório

- **Sem CI.** Nada roda `go vet`, `gofmt -l`, `go test -race` ou build cruzado
  para os alvos que o README anuncia (windows/amd64, darwin/amd64, linux/arm64).
  Um workflow de ~20 linhas cobre tudo.
- **README, seção "Compilar"**: mistura três receitas com linhas comentadas e
  traz `go get ident-test`, que referencia o módulo local do exemplo e não
  funciona. Vale reduzir a uma receita que funcione.
- **README §Persistência** afirma *"No caminho feliz (todas as envs presentes),
  `$DATADIR` nunca é tocado — compatível com filesystem read-only"*. Verdadeiro,
  mas incompleto: falta dizer que basta **uma** env ausente para `$DATADIR` virar
  obrigatório (BUG-02).
- **`go-loghub-uuid`**: o `go.mod` fixa `v0.1.0` e já existe `v0.2.0`. Vale
  avaliar a atualização (fora do escopo desta revisão).

---

# COBERTURA DE TESTES

## Situação anterior

`resolve_test.go` está bem escrito e cobre a lógica pura com um `fakeSystem`.
Mas o recorte deixava de fora exatamente a camada que roda em produção:

| Símbolo                  | Cobertura antes |
|--------------------------|-----------------|
| `Initialize`             | **0,0%**        |
| `osSystem.Getenv`        | **0,0%**        |
| `osSystem.Stat`          | **0,0%**        |
| `osSystem.ReadFile`      | **0,0%**        |
| `osSystem.WriteFile`     | **0,0%**        |
| `osSystem.Hostname`      | **0,0%**        |
| `osSystem.Args`          | **0,0%**        |
| `osSystem.GenerateUUIDv7`| **0,0%**        |
| **Total**                | **85,5%**       |

Nenhum código de saída era verificado ponta a ponta, nem o formato da mensagem
de erro da SPEC §13.

## O que foi criado em [`../tests/`](../tests/)

Pacote `tests` separado, importando a biblioteca pelo caminho de módulo — a
mesma convenção de `go-loghub-uuid`. Como `Initialize()` chama `os.Exit`, o
harness re-executa o próprio binário de teste como subprocesso, com **ambiente
limpo**, e inspeciona código de saída, stdout e stderr.

| Arquivo             | Conteúdo |
|---------------------|----------|
| `doc.go`            | documentação do pacote |
| `README.md`         | como rodar, incluindo cobertura e testes de bug |
| `helper_test.go`    | `TestMain`, harness de subprocesso, utilitários |
| `api_test.go`       | caminho feliz, precedência, persistência, debug |
| `exitcodes_test.go` | tabela da SPEC §13 ponta a ponta |
| `bugs_test.go`      | um par `TestChar_`/`TestFix_` por bug |
| `bench_test.go`     | getters, validação, init e boot completo |

**Resultado:** 88 testes passando, 12 pulados (os `TestFix_*`).
`go vet`, `gofmt -l` e `go test -race ./...` limpos.

**Cobertura da suíte de `./tests/` sozinha: 95,7%** — incluindo `Initialize` e
todo o `osSystem`. Medição reprodutível (duas execuções idênticas):

```bash
GOCOVERDIR=$(mktemp -d) go test ./tests/ -coverpkg=github.com/patrickbrandao/go-loghub-ident
```

`GOCOVERDIR` é necessário porque os contadores nascem nos subprocessos; sem ele
o total aparece como 0,0% e o runtime de cobertura ainda escreve um aviso em
stderr (o harness já o filtra, para não contaminar os testes que inspecionam a
saída de erro).

Códigos **105** (falha na geração de UUID) e **108** (erro de `os.Hostname`) não
são alcançáveis de fora: só ocorrem com o `system` injetado, e os unitários da
raiz já os cobrem. Está anotado no cabeçalho de `exitcodes_test.go`.

## O par `TestChar_` / `TestFix_`

Cada bug tem dois testes:

- **`TestChar_*`** afirma o comportamento **atual**. Passa hoje e passa a falhar
  no dia da correção, com a mensagem `BUG-XX parece corrigido` — o sinal de que o
  `TestFix_*` correspondente deve virar teste permanente.
- **`TestFix_*`** afirma o comportamento **esperado**. Pulado por padrão:

```bash
LOGHUB_IDENT_TEST_FIXES=1 go test ./tests/ -run TestFix -v
```

Assim `go test ./...` fica verde no estado atual sem que nenhum defeito fique
sem registro executável.

Dois cuidados na suíte: o teste de corrida do BUG-04 **mede sem falhar** (a
corrida é probabilística; um teste intermitente seria pior que nenhum), e a
demonstração do BUG-01 usa um **FIFO**, que trava sem consumir memória, em vez de
`/dev/zero`, que derrubaria a máquina de testes.

---

# PLANO DE AÇÃO SUGERIDO

| # | Item | Esforço | Por quê |
|---|------|---------|---------|
| 1 | **BUG-01** leitura limitada + rejeitar não-regulares | P | Único achado que derruba o host |
| 2 | **BUG-04** `O_EXCL` + `tmp`/`rename` | M | Fecha a origem dos arquivos truncados |
| 3 | **BUG-03** avisar em toda rotação de identidade | P | Torna visível o que hoje é silencioso |
| 4 | **BUG-02** decidir e alinhar SPEC × código | M | Contradição interna da SPEC; exige decisão |
| 5 | **BUG-06** `..` e limite de comprimento | P | Fecha travessia de caminho na origem |
| 6 | **BUG-05** `chmod` explícito | P | Uma linha |
| 7 | **BUG-07** debug conforme SPEC §12 | P | Diagnóstico onde mais falta |
| 8 | **MEL-01** reportar todas as falhas juntas | P | Alto retorno operacional |
| 9 | **BUG-08/09/10/11/12** | P | Todos pontuais |
| 10 | **PERF-01** sair do `regexp` | M | Casa bem com BUG-06 e BUG-11 |
| 11 | **MEL-02** `TryInitialize` / **MEL-05** CI | M | Estrutural |

Itens 1–3 fecham o ciclo *gravação parcial → descarte silencioso → identidade
trocada*; se só um bloco for feito, que seja esse.

---

# APÊNDICE — Comandos

```bash
# Suíte completa
go test ./...

# Só a integração, com detector de corrida
go test -race ./tests/

# Sem o teste de corrida do BUG-04 (o mais demorado)
go test -short ./tests/

# Cobertura real, incluindo os subprocessos
GOCOVERDIR=$(mktemp -d) go test ./tests/ -coverpkg=github.com/patrickbrandao/go-loghub-ident

# Benchmarks (números citados neste relatório)
go test ./tests/ -bench=. -benchmem -run=XXX

# Validar uma correção
LOGHUB_IDENT_TEST_FIXES=1 go test ./tests/ -run TestFix -v
```
