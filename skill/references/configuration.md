# Referência de Configuração — `go-loghub-ident`

Comportamento campo a campo da biblioteca [`go-loghub-ident`](https://github.com/patrickbrandao/go-loghub-ident) (`lhident`) na versão documentada pela skill: cadeia de precedência, saneamento e validação de cada fonte.

---

## 1. Ordem de resolução

`Initialize()` resolve os campos nesta ordem e para na primeira falha:

1. `DATADIR` (só o caminho; a existência é verificada quando necessária)
2. `MACHINE_ID`
3. `AGENT_NAME`
4. `AGENT_UUID`
5. `HOSTNAME`
6. `WORKSPACE`

Consequência prática: com um `$DATADIR` sem permissão de escrita e nenhuma env de identidade, a falha reportada é a do `MACHINE_ID` (113), não a do `AGENT_UUID` (106), porque ele é gerado primeiro.

Regra geral de cada campo: **env → arquivo → fallback**. Uma fonte ausente ou vazia cai para a próxima; uma env presente mas inválida encerra o processo com o código do campo.

---

## 2. Saneamento dos valores

Toda fonte de **campo** (env ou arquivo) passa pelo mesmo pipeline antes da validação:

1. **Primeira linha:** corta no primeiro `\r` ou `\n`.
2. **BOM UTF-8:** remove `EF BB BF` do início (comum em arquivos salvos por editores Windows).
3. **Bordas:** remove espaços e caracteres de controle Unicode das duas pontas (inclui tab, NUL e DEL — um NUL residual de gravação truncada não invalida o valor).
4. **Minúsculas:** `Node01` vira `node01`, `ABCDEF...` vira `abcdef...`.
5. **Específico:** só o `MACHINE_ID` remove todos os hífens.
6. **Validação** do campo (seção 3).

Os **caminhos** (`DATADIR`, `MACHINE_ID_FILE`) não passam por esse pipeline: perdem só os espaços das bordas e **não** são convertidos para minúsculas.

---

## 3. Campo a campo

### 3.1. `DATADIR`
- **Fontes:** env `DATADIR` → `/data` (`lhident.DefaultDataDir`).
- **Formato:** caminho ancorado na raiz, sem caracteres de controle; normalizado com `filepath.Clean`. `dados`, `./dados` e `../dados` são recusados com **100** — a identidade não pode depender do diretório de trabalho.
- **Windows:** `C:\dados` é absoluto; `/data` e `\data` também são aceitos e resolvidos contra a unidade corrente do processo.
- **Existência (lazy):**
  - Na **leitura** de `agent_name`, `workspace`, `machine_id` ou `agent_uuid`, um `$DATADIR` inexistente é tratado como fonte ausente.
  - Quando é preciso **gravar** uma identidade gerada, `$DATADIR` inexistente encerra com **100** (`"/data" não existe`).
  - `$DATADIR` que existe mas não é diretório, ou que dá erro de I/O/permissão no `stat`, encerra com **100** em qualquer caso.
- **Links simbólicos:** o próprio `$DATADIR` pode ser um link para o volume real. Os **arquivos dentro dele** não podem: link, FIFO, dispositivo ou arquivo com mais de 4 KiB encerra com **100**.

### 3.2. `MACHINE_ID`
- **Formato:** `^[0-9a-f]{32}$` depois de remover hífens e converter para minúsculas. Não há verificação de versão: um UUID qualquer sem hífens serve.
- **Cadeia (4 níveis):**
  1. **env `MACHINE_ID`:** presente e inválida → **102**.
  2. **Arquivo do sistema** — `$MACHINE_ID_FILE`, ou `/etc/machine-id` se ela estiver vazia:
     - `MACHINE_ID_FILE` relativo ou com caractere de controle → **100**.
     - `MACHINE_ID_FILE` apontando para arquivo **inexistente** → usa `/etc/machine-id` (linha de debug, sem erro).
     - `MACHINE_ID_FILE` inacessível (permissão, I/O), que não é arquivo comum ou com mais de 4 KiB → **100**.
     - `/etc/machine-id` ausente, ilegível ou inválido → cai em silêncio para o nível 3.
     - Conteúdo inválido (em qualquer dos dois) → cai para o nível 3.
     - Links simbólicos são seguidos aqui (em várias distribuições o `/etc/machine-id` é um link).
  3. **`$DATADIR/machine_id`:**
     - Válido → usado.
     - Ausente → nível 4, sem aviso.
     - Inválido ou vazio → aviso em `stderr` e nível 4. Um arquivo vazio modificado há menos de 10 s é relido até completar essa janela antes de ser declarado corrompido; um vazio mais antigo é regenerado na hora.
  4. **Geração:** UUIDv7 sem hífens, gravado em `$DATADIR/machine_id` (`0644`, arquivo temporário + `fsync` + publicação atômica + `fsync` do diretório). Se outro processo publicou primeiro no mesmo volume, o valor dele é adotado.
     - Falha de geração → **114**. Falha de gravação → **113**.

### 3.3. `AGENT_NAME`
- **Formato:** `^[a-z0-9._-]+$`, 1 a 64 caracteres, nunca `.` nem `..`.
- **Cadeia (3 níveis):**
  1. **env `AGENT_NAME`:** presente e inválida → **104**.
  2. **`$DATADIR/agent_name`:** presente e inválido → **104** (a mensagem mostra tamanho e hash, não o conteúdo). Nunca é escrito pela biblioteca.
  3. **`argv[0]`:** nome base do executável, em minúsculas, sem o sufixo `.exe`.
     - Vazio, `.` ou separador de diretório → **103**.
     - Fora do formato → **104** (ex.: binário com espaço ou `+` no nome).
- **Armadilhas do fallback:** `go run main.go` produz `main`; `go run .` produz o nome do diretório; uma imagem com `ENTRYPOINT ["/app"]` produz `app`. Em produção, defina `AGENT_NAME`.

### 3.4. `AGENT_UUID`
- **Formato:** UUID versão 7 canônico, RFC 9562: `^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$` (36 caracteres). UUIDv4 e UUID sem hífens são recusados.
- **Cadeia (3 níveis):**
  1. **env `AGENT_UUID`:** presente e inválida → **107**.
  2. **`$DATADIR/agent_uuid`:** mesmas regras do `machine_id` (válido → usado; ausente → gera sem aviso; inválido ou vazio → aviso e regeneração).
  3. **Geração:** UUIDv7 gravado em `$DATADIR/agent_uuid` com as mesmas garantias do `machine_id`.
     - Falha de geração → **105**. Falha de gravação → **106**. Valor gerado inválido → **107**.

### 3.5. `HOSTNAME`
- **Formato:** RFC 1123 em minúsculas: até 253 caracteres; rótulos separados por `.`, cada um com 1 a 63 caracteres de `[a-z0-9-]`, sem hífen na primeira ou na última posição. Rótulo vazio (`a..b`, `host.`) é recusado.
- **Cadeia (2 níveis):**
  1. **env `HOSTNAME`:** presente e inválida → **109**.
  2. **`os.Hostname()`:** erro → **108**; valor inválido → **109**.
- Nunca é lido de nem gravado em `$DATADIR`.

### 3.6. `WORKSPACE`
- **Formato:** `^[a-z0-9.-]+$`, 1 a 64 caracteres, nunca `.` nem `..`. Diferente do `AGENT_NAME`, **não aceita `_`**.
- **Cadeia (3 níveis):**
  1. **env `WORKSPACE`:** presente e inválida → **111**.
  2. **`$DATADIR/workspace`:** presente e inválido → **111**. Nunca é escrito pela biblioteca.
  3. **Fallback:** `"default"` (`lhident.DefaultWorkspace`).

---

## 4. Arquivos escritos pelo operador

`agent_name` e `workspace` em `$DATADIR` permitem configurar um volume em vez da env. Uma linha basta; quebra de linha final, BOM e espaços nas bordas são tolerados:

```bash
printf 'pagamentos-api\n' > /data/agent_name
printf 'prod-us-east\n'  > /data/workspace
```

A env, quando presente, sempre vence o arquivo.

---

## 5. Diagnóstico (`LOGHUB_IDENT_DEBUG`)

Qualquer valor não vazio (inclusive `0`) liga o diagnóstico. Cada campo produz uma linha `lib-loghub-ident: debug: <CAMPO>: <origem> = "<valor>"`, onde a origem é uma destas:

| Origem | Significado |
| :--- | :--- |
| `env` | Variável de ambiente. |
| `padrão` | `DATADIR` ausente; usado `/data`. |
| `file <caminho>` | Arquivo do sistema ou de `$DATADIR`; pode vir seguido de `(após estabilização)`, `(definido por outro processo)` ou `(restaurado do registro de regeneração)`. |
| `generated` | Identidade gerada e gravada por este processo. |
| `fallback argv[0]` | `AGENT_NAME` derivado do executável. |
| `os.Hostname` | `HOSTNAME` obtido do sistema. |
| `fallback` | `WORKSPACE` padrão. |

Fontes ignoradas também geram linhas (ex.: `MACHINE_ID_FILE: "/x" inexistente, usando /etc/machine-id`).
