# Guia de Diagnóstico e Resolução de Problemas (Troubleshooting)

Este documento fornece um guia prático para operadores e desenvolvedores investigarem e resolverem falhas de inicialização, códigos de erro e anomalias de identidade ao utilizar `go-loghub-ident`.

---

## 1. Procedimento de Diagnóstico Imediato

Quando um microsserviço entrar em *CrashLoopBackOff* ou falhar ao iniciar:

### Passo 1: Inspecione a Última Linha de `stderr`
A biblioteca sempre escreve a causa exata na última linha antes de sair:
```text
lib-loghub-ident: <VARIÁVEL>: <motivo>
```

### Passo 2: Ative o Modo Debug do Loghub Ident
Adicione a variável de ambiente no deployment ou container:
```bash
LOGHUB_IDENT_DEBUG=1
```
Reinicie o serviço e verifique os logs. As 6 linhas de diagnóstico mostrarão exatamente qual fonte alimentou cada campo até o momento da falha:
```text
lib-loghub-ident: debug: DATADIR: env = "/data"
lib-loghub-ident: debug: MACHINE_ID: env = "0123456789abcdef0123456789abcdef"
lib-loghub-ident: debug: AGENT_NAME: fallback argv[0] = "meu-servico"
...
```

---

## 2. Cenários Comuns de Falha e Soluções

### Cenário 1: Erro 100 em `DATADIR`
- **Mensagem típica:** `lib-loghub-ident: DATADIR: caminho relativo não permitido` ou `diretório necessário não existe`.
- **Causa:**
  1. Foi informado um caminho relativo (ex.: `DATADIR=./dados` ou `DATADIR=data`).
  2. O serviço precisa gerar e persistir `machine_id` ou `agent_uuid`, mas o diretório `/data` (ou o caminho configurado) não foi criado ou montado no container.
  3. O diretório montado possui permissão de somente leitura (`ro`) e não há variáveis explícitas para `MACHINE_ID` e `AGENT_UUID`.
- **Solução:**
  - Defina um caminho absoluto (ex.: `DATADIR=/var/lib/myapp`).
  - No Kubernetes/Docker, assegure que o `volumeMount` está presente e com permissão de escrita (`readOnly: false`).
  - Para containers estritamente *read-only*, passe **todas** as variáveis de ambiente explicitamente (`MACHINE_ID`, `AGENT_NAME`, `AGENT_UUID`, `HOSTNAME`, `WORKSPACE`).

### Cenário 2: Erro 102 em `MACHINE_ID`
- **Mensagem típica:** `lib-loghub-ident: MACHINE_ID: formato inválido (esperado 32 hexadecimais)`.
- **Causa:** A variável `MACHINE_ID` foi definida, mas contém caracteres não-hexadecimais, espaços incorretos ou comprimento diferente de 32 caracteres (mesmo após a remoção de hífens).
- **Solução:**
  - Verifique o valor da env. Deve conter exatamente 32 dígitos hexadecimais (ex.: `abcdef0123456789abcdef0123456789`).

### Cenário 3: Erro 104 em `AGENT_NAME` ou 111 em `WORKSPACE`
- **Mensagem típica:** `lib-loghub-ident: AGENT_NAME: valor não cumpre os requisitos` ou `rejeita . e ..`.
- **Causa:** O nome possui mais de 64 caracteres, caracteres fora de `[a-z0-9._-]`, ou contém `.` ou `..` (tentativa de path traversal). No caso de `WORKSPACE`, o caractere `_` não é permitido.
- **Solução:**
  - Use nomes em minúsculo, hífens e números (ex.: `AGENT_NAME=pagamentos-api`, `WORKSPACE=prod-us-east`).

### Cenário 4: Erro 106 ou 113 (Falha de Gravação em Disco)
- **Mensagem típica:** `lib-loghub-ident: AGENT_UUID: falha ao gravar em disco: no space left on device` ou `permission denied`.
- **Causa:**
  - Disco cheio ou quota de inodes esgotada no volume.
  - O processo roda com um UID que não possui permissão de escrita no diretório montado.
- **Solução:**
  - Libere espaço no volume.
  - Verifique o `securityContext` no Kubernetes (`fsGroup` e permissões do volume montado).

### Cenário 5: Erro 112 (`Initialize() já foi chamado`)
- **Mensagem típica:** `lib-loghub-ident: geral: Initialize() já foi chamado`.
- **Causa:** O código da aplicação invocou `lhident.Initialize()` duas vezes (ex.: na função `init()` e novamente no `main()`, ou dentro de um handler de reinicialização).
- **Solução:**
  - Remova a invocação redundante. `Initialize()` deve ser chamada estritamente uma única vez no início de `main()`.

---

## 3. Investigando Avisos Operacionais (`lib-loghub-ident: aviso:`)

Aviso típico no log do coletor:
```text
lib-loghub-ident: aviso: MACHINE_ID: /data/machine_id tinha conteúdo inválido (16 bytes, hash 8f3a1b0c9e7d4a2f) e será REGERADO; a identidade desta máquina muda a partir de agora
```

### O que significa?
O arquivo preexistente no volume estava corrompido (tamanho truncado ou lixo aleatório). A biblioteca descartou os dados inválidos para permitir que o serviço inicializasse com uma nova identidade válida.

### Como Auditar:
1. O hash informado é um hash **FNV-1a 64-bit** dos bytes corrompidos. Isso permite comparar com backups do volume sem expor segredos em texto claro no log.
2. Verifique se o host sofreu *hard reboot*, `kill -9` de processos ou problemas no storage compartilhado (NFS/SAN).
3. Atualize o registro no servidor Loghub para mapear a transição da identidade antiga para a nova.

---

## 4. Concorrência e Compartilhamento de Volumes

### Containers Principais e Sidecars no Mesmo Pod
- **Comportamento Esperado:** Ambos montam o mesmo volume `/data`. O primeiro a iniciar cria a identidade com `CreateExclusive`; o segundo adota automaticamente o valor do primeiro via `readSettled`. Ambos reportam exatamente o mesmo `MachineID()` e `AgentUUID()`.

### Réplicas Independentes (ReplicaSet / Deployment)
- **Atenção:** **Cada Pod do ReplicaSet DEVE ter seu próprio volume dedicado** (usando `volumeClaimTemplates` em um `StatefulSet` ou diretórios segregados por Pod: `/data/pod-0`, `/data/pod-1`).
- **Problema de Volume Compartilhado:** Se vários Pods independentes montarem o mesmo PVC gravável, eles compartilharão a mesma identidade de agente, causando duplicidade e conflito de telemetria no servidor central Loghub.
