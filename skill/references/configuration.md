# Referência Aprofundada de Configuração — `go-loghub-ident`

Este documento detalha o comportamento campo a campo da biblioteca [`go-loghub-ident`](https://github.com/patrickbrandao/go-loghub-ident) (`lhident`), fornecendo a matriz completa de precedência, pipelines de higienização e regras estritas de validação.

---

## 1. Pipeline de Higienização de Strings

Todas as fontes textuais (sejam variáveis de ambiente ou arquivos) passam pelo mesmo pipeline antes da validação:
1. **Primeira Linha (`firstLine`):** Extrai caracteres até o primeiro `\r` ou `\n`.
2. **Remoção de BOM UTF-8:** Elimina `0xEF, 0xBB, 0xBF` (`\ufeff`) presente em arquivos criados em editores Windows.
3. **Trim de Controles e Espaços:** Remove espaços, tabs e bytes de controle Unicode e ASCII (`<= 0x20` e `0x7F`), incluindo bytes nulos residuais `\x00`.
4. **Lowercase:** Conversão compulsória para minúsculas (`strings.ToLower`).
5. **Sanitização Específica:** O campo `MACHINE_ID` remove todos os hífens (`-`). Os demais campos mantêm seus caracteres inalterados.
6. **Validação Manual Estrita:** Verificação contra loops em bytes manuais (sem uso de `regexp`).

---

## 2. Comportamento Detalhado por Campo

### 2.1. `DATADIR`
- **Env:** `DATADIR`
- **Padrão:** `/data` (`lhident.DefaultDataDir`)
- **Regras:**
  - Deve ser caminho absoluto ancorado na raiz (`rootedPath`). Caminhos relativos (`data`, `./data`) são rejeitados com **código 100**.
  - O valor é normalizado com `filepath.Clean` (remove barras duplas e pontos desnecessários).
  - **Validação Lazy:** Não exige a existência de `$DATADIR` se nenhuma escrita em disco for necessária (caminho feliz com envs completas).
  - **Leitura:** Se o diretório não existir, é tratado como *fonte ausente*, permitindo alcançar fallbacks determinísticos (`argv[0]` e `"default"`).
  - **Gravação:** Se a biblioteca precisar persistir uma nova identidade (`machine_id` ou `agent_uuid`) e `$DATADIR` não existir ou não puder ser acessado, aborta com **código 100**.
  - **Symlinks:** Links simbólicos em `$DATADIR` são rejeitados por segurança.

### 2.2. `MACHINE_ID`
- **Formato:** Exatos 32 caracteres hexadecimais em minúsculo `^[0-9a-f]{32}$`.
- **Precedência (4 Níveis):**
  1. `MACHINE_ID` (env): Se presente e inválida → aborta com **código 102**.
  2. `$MACHINE_ID_FILE` (arquivo; padrão `/etc/machine-id`):
     - Se o caminho for informado explicitamente via env e for relativo, inacessível, FIFO ou > 4 KiB → aborta com **código 100**.
     - Se for o `/etc/machine-id` padrão: leitura tolerante a falhas (cai silenciosamente se ausente ou ilegível).
  3. `$DATADIR/machine_id` (arquivo persistido):
     - Se corrompido → emite aviso operacional em `stderr` e cai para a geração local. Se **vazio** → o mesmo, mas antes espera a janela de estabilização quando o arquivo tiver sido modificado há menos de 10 s (pode ser o que um irmão acabou de publicar num volume de rede); um arquivo vazio antigo é regenerado na hora. Se ausente → gera em silêncio.
  4. **Geração Local:**
     - Gera UUIDv7 criptográfico, remove hífens e grava em `$DATADIR/machine_id` com permissão `0644` e `fsync` duplo.
     - Falha de geração de UUID → **código 114**.
     - Falha de gravação no disco → **código 113**.

### 2.3. `AGENT_NAME`
- **Formato:** 1 a 64 bytes em `^[a-z0-9._-]+$`. Rejeita expressamente `.` e `..`.
- **Precedência (3 Níveis):**
  1. `AGENT_NAME` (env): Se presente e inválida → aborta com **código 104**.
  2. `$DATADIR/agent_name` (arquivo): Se presente e inválido → aborta com **código 104**.
  3. **Fallback `argv[0]`:** Extrai `filepath.Base(os.Args[0])`, converte para lowercase e remove o sufixo `.exe`.
     - Se o resultado ficar vazio ou for `.` ou separador `/` ou `\` → aborta com **código 103**.
     - Se não cumprir a validação → aborta com **código 104**.

### 2.4. `AGENT_UUID`
- **Formato:** UUIDv7 canônico com hífens RFC 9562 (36 caracteres: `^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).
- **Precedência (3 Níveis):**
  1. `AGENT_UUID` (env): Se presente e inválida → aborta com **código 107**.
  2. `$DATADIR/agent_uuid` (arquivo persistido): Se corrompido → emite aviso em `stderr` e regenera. Se **vazio** → o mesmo, depois da janela de estabilização quando o arquivo tiver sido modificado há menos de 10 s; um arquivo vazio antigo é regenerado na hora. Se ausente → gera em silêncio.
  3. **Geração Local:** Gera UUIDv7 canônico e grava em `$DATADIR/agent_uuid` com permissão `0644` e `fsync`.
     - Falha de geração → **código 105**.
     - Falha de gravação no disco → **código 106**.
     - Valor gerado inválido → **código 107**.

### 2.5. `HOSTNAME`
- **Formato:** Padrão RFC 1123. Comprimento máximo de 253 caracteres. Rótulos separados por `.` com 1 a 63 caracteres alfanuméricos e hífens, sem hífen no início ou fim de nenhum rótulo.
- **Precedência (2 Níveis):**
  1. `HOSTNAME` (env): Se presente e inválida → aborta com **código 109**.
  2. **Chamada ao SO `os.Hostname()`:**
     - Falha na syscall nativa → **código 108**.
     - Valor retornado inválido segundo a RFC 1123 → **código 109**.

### 2.6. `WORKSPACE`
- **Formato:** 1 a 64 bytes em `^[a-z0-9.-]+$`. Rejeita expressamente `.` e `..` e o caractere `_`.
- **Precedência (3 Níveis):**
  1. `WORKSPACE` (env): Se presente e inválida → aborta com **código 111**.
  2. `$DATADIR/workspace` (arquivo): Se presente e inválido → aborta com **código 111**.
  3. **Fallback Determinístico:** Retorna a constante `"default"` (`lhident.DefaultWorkspace`).
