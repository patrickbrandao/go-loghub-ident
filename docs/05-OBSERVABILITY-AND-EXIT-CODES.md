# 05 — Observabilidade, Diagnóstico e Códigos de Saída

> **Status:** Canônico e Normativo  
> **Escopo:** Modo silencioso padrão, protocolo de diagnóstico `LOGHUB_IDENT_DEBUG`, avisos operacionais compulsórios em `stderr` com hash FNV-1a, formato de mensagens de erro e catálogo exaustivo de códigos de saída (100 a 114).

---

## 1. Padrão de Observabilidade

A biblioteca segue o princípio unix de **silêncio no caminho de sucesso**:
- Em condições normais, `Initialize()` não emite absolutamente nenhum caractere em `stdout` ou `stderr`.
- Toda a comunicação de diagnóstico, aviso operacional e falha é direcionada estritamente para **`stderr`** (`os.Stderr`), mantendo `stdout` limpo para pipes e comunicação da aplicação principal.

---

## 2. Modo de Diagnóstico Debug (`LOGHUB_IDENT_DEBUG`)

Quando a variável de ambiente `LOGHUB_IDENT_DEBUG` estiver definida com qualquer valor não-vazio (`LOGHUB_IDENT_DEBUG=1` ou `LOGHUB_IDENT_DEBUG=true`), a biblioteca escreve em `stderr` **exatamente 6 linhas** (uma para cada campo resolvido, incluindo `DATADIR`):

```text
lib-loghub-ident: debug: DATADIR: env = "/data"
lib-loghub-ident: debug: MACHINE_ID: file /etc/machine-id = "0123456789abcdef0123456789abcdef"
lib-loghub-ident: debug: AGENT_NAME: fallback argv[0] = "my-service"
lib-loghub-ident: debug: AGENT_UUID: generated = "018f3a5b-7c8d-7e9f-8a1b-2c3d4e5f6a7b"
lib-loghub-ident: debug: HOSTNAME: os.Hostname = "node01.example.com"
lib-loghub-ident: debug: WORKSPACE: fallback = "default"
```

### Regras do Modo Debug (`BUG-07`):
1. **Formato Rígido:** `lib-loghub-ident: debug: <CAMPO>: <fonte> = "<valor>"`
2. **Fontes Identificadas:**
   - `env`: Lido diretamente de variável de ambiente.
   - `file <caminho>`: Lido de arquivo em disco.
   - `fallback argv[0]`: Extraído do nome do executável.
   - `fallback`: Fallback determinístico (`default` para workspace).
   - `generated`: Gerado via UUIDv7 em runtime.
   - `os.Hostname`: Obtido via chamada de sistema.
3. **Emissão Prévia a Falhas:** As linhas de debug são emitidas **antes** de qualquer encerramento por erro. Se o processo abortar no campo 4 (`AGENT_UUID`), os campos 1, 2 e 3 já estarão registrados no log, permitindo diagnosticar quais fontes alimentaram os campos anteriores.

---

## 3. Avisos Operacionais Compulsórios (`lib-loghub-ident: aviso:`)

Avisos operacionais representam eventos críticos de infraestrutura que exigem auditoria e monitoramento, mas que foram contornados pela biblioteca para manter o serviço operacional.

> **Regra:** Avisos operacionais são emitidos **SEMPRE**, mesmo com `LOGHUB_IDENT_DEBUG` desativada.

```text
lib-loghub-ident: aviso: <mensagem>
```

### Casos de Emissão:

#### 1. Descarte e Regeneração de Identidade Persistida (`BUG-03`)
Ocorre quando `$DATADIR/machine_id` ou `$DATADIR/agent_uuid` contém dados corrompidos ou inválidos:
```text
lib-loghub-ident: aviso: MACHINE_ID: /data/machine_id tinha conteúdo inválido (16 bytes, hash ...) e será REGERADO; a identidade desta máquina muda a partir de agora
```
- **Proteção contra Vazamento de Segredos:** A biblioteca **nunca** imprime o conteúdo corrompido em claro no log. Ela reporta apenas a contagem de bytes e o **hash FNV-1a 64-bit** em hexadecimal. Isso permite aos operadores correlacionar o arquivo com dumps de auditoria sem expor senhas ou tokens que tenham sido gravados indevidamente no arquivo.

#### 2. Adoção de Regeneração Concorrente
Ocorre quando um processo réplica descobre que a identidade corrompida já foi regerada e arbitrada por um processo irmão via `.regen`:
```text
lib-loghub-ident: aviso: MACHINE_ID: adotando regeneração prévia de /data/machine_id (novo valor gravado em /data/.machine_id.regen)
```

---

## 4. Formato Canônico de Erro e Encerramento

Quando ocorre uma falha irrecuperável, a biblioteca:
1. Escreve exatamente uma linha em `stderr`:
   ```text
   lib-loghub-ident: <VARIÁVEL>: <motivo>
   ```
2. Invoca imediatamente `os.Exit(código)`.
3. Essa mensagem é garantidamente a **última linha** escrita pela biblioteca.

---

## 5. Tabela Canônica de Códigos de Saída (Exit Codes)

| Código | Variável | Motivo do Aborto | Condição Disparadora |
| :---: | :--- | :--- | :--- |
| **100** | `DATADIR` | Caminho relativo, contém controles, diretório ausente na gravação, não é diretório, I/O ou symlink recusado. | `$DATADIR` é relativo; contém caracteres de controle; não existe quando uma geração precisa ser salva; erro de permissão/I/O; ou arquivo acessado é link simbólico. |
| **100** | `MACHINE_ID_FILE` | Caminho explícito é relativo, contém controles, inacessível, excede 4 KiB ou não é arquivo comum. | Variável `MACHINE_ID_FILE` foi definida pelo usuário, mas o caminho é relativo, inacessível por permissão, é FIFO/device/diretório ou tem tamanho > 4096 bytes. |
| **102** | `MACHINE_ID` | Formato inválido na variável de ambiente. | Variável `MACHINE_ID` foi definida, mas após remover `-` e espaços não possui exatos 32 caracteres hexadecimais `^[0-9a-f]{32}$`. |
| **103** | `AGENT_NAME` | Todas as fontes de nome do agente estão vazias. | `AGENT_NAME` ausente, `$DATADIR/agent_name` ausente, e `argv[0]` do binário saneou para vazio, `.` ou separador `/` ou `\`. |
| **104** | `AGENT_NAME` | Nome do agente inválido. | Valor informado na env, arquivo ou base de `argv[0]` tem mais de 64 caracteres, caracteres fora de `^[a-z0-9._-]+$`, ou é `.` ou `..`. |
| **105** | `AGENT_UUID` | Falha interna na geração de UUIDv7. | O gerador local de UUID (`go-loghub-uuid`) retornou erro durante a resolução de `AGENT_UUID`. |
| **106** | `AGENT_UUID` | Falha de persistência em disco de `agent_uuid`. | Erro de I/O, disco cheio ou permissão negada ao gravar `$DATADIR/agent_uuid`. |
| **107** | `AGENT_UUID` | Formato inválido de UUIDv7 canônico. | Variável `AGENT_UUID` ou valor gerado não cumpre `^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`. |
| **108** | `HOSTNAME` | Chamada ao sistema `os.Hostname()` falhou. | Variável `HOSTNAME` ausente e a chamada nativa do SO retornou erro. |
| **109** | `HOSTNAME` | Hostname não atende à RFC 1123. | Hostname da env ou do SO excede 253 chars, possui rótulos > 63 chars, contém caracteres inválidos ou possui hífen nas extremidades de um rótulo. |
| **111** | `WORKSPACE` | Formato inválido de workspace. | Valor da env `WORKSPACE` ou arquivo `$DATADIR/workspace` tem mais de 64 chars, caracteres fora de `^[a-z0-9.-]+$`, ou é `.` ou `..`. |
| **112** | *(geral)* | Inicialização duplicada detectada. | `Initialize()` foi invocado mais de uma vez no mesmo ciclo de vida do processo (prefixo `geral:`). |
| **113** | `MACHINE_ID` | Falha de persistência em disco de `machine_id`. | Erro de I/O, disco cheio ou permissão negada ao gravar `$DATADIR/machine_id`. |
| **114** | `MACHINE_ID` | Falha interna na geração do UUID base do machine-id. | O gerador local de UUID (`go-loghub-uuid`) retornou erro durante a composição do `MACHINE_ID`. |

---

## 6. Histórico de Omissões e Adições de Códigos

### Código 101 (Omitido Intencionalmente)
No rascunho original do projeto, o código 101 representava ausência de `MACHINE_ID`. Ele foi **completamente eliminado** porque a cadeia de machine-id sempre culmina na geração de um UUIDv7 local quando nenhum arquivo ou variável é fornecido. O campo nunca falha por ausência.

### Código 110 (Omitido Intencionalmente)
No rascunho original, o código 110 representava ausência de `WORKSPACE`. Ele foi **completamente eliminado** porque o workspace possui o fallback estático `"default"`, sendo impossível falhar por falta de valor.

### Código 114 (Adicionado)
Criado especificamente para separar falhas de geração de UUID em `MACHINE_ID` das falhas de geração em `AGENT_UUID` (105). Sem o 114, o operador receberia uma mensagem genérica de falha em UUID que induziria à investigação do campo errado.
