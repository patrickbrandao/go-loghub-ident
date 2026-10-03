# Diagnóstico e Resolução de Problemas — `go-loghub-ident`

Roteiro para investigar falhas de boot, códigos de saída e mudanças inesperadas de identidade. As mensagens abaixo são as que a biblioteca escreve de fato; caminhos e valores variam.

---

## 1. Roteiro imediato

Quando o serviço não sobe (ou entra em *CrashLoopBackOff*):

1. **Leia o código de saída e a última linha de `stderr`.** Em falha, a biblioteca sempre termina com:
   ```text
   lib-loghub-ident: <VARIÁVEL>: <motivo>
   ```
   No Kubernetes: `kubectl logs <pod> --previous` e `kubectl get pod <pod> -o jsonpath='{.status.containerStatuses[*].lastState.terminated.exitCode}'`.
2. **Ligue o diagnóstico** com `LOGHUB_IDENT_DEBUG=1` e reinicie. As linhas `debug:` dos campos já resolvidos saem antes da mensagem de erro e mostram de qual fonte veio cada valor:
   ```text
   lib-loghub-ident: debug: DATADIR: env = "/data"
   lib-loghub-ident: debug: MACHINE_ID: env = "0123456789abcdef0123456789abcdef"
   lib-loghub-ident: debug: AGENT_NAME: fallback argv[0] = "meu-servico"
   lib-loghub-ident: AGENT_UUID: gravação em /data/agent_uuid falhou: open /data/.agent_uuid.tmp3588345345: permission denied
   ```
3. **Localize o código** na tabela da seção 8 do `SKILL.md` e no cenário correspondente abaixo.

---

## 2. Falhas de boot

### 2.1. `DATADIR: "/data" não existe` (100)
- **Causa:** a biblioteca precisa gerar `machine_id` ou `agent_uuid` e o diretório configurado (ou o padrão `/data`) não existe. É a primeira falha típica em máquina de desenvolvimento e em container sem volume montado.
- **Correção:** aponte `DATADIR` para um diretório que exista (`DATADIR=/tmp/meu-servico-data`), monte o volume no caminho esperado, ou defina `MACHINE_ID` e `AGENT_UUID` na env para que nada precise ser gravado.

### 2.2. `DATADIR: "dados" é relativo ao diretório de trabalho; use um caminho absoluto` (100)
- **Causa:** `DATADIR` relativo (`dados`, `./dados`). A identidade não pode mudar conforme o diretório de onde o processo é iniciado.
- **Correção:** caminho absoluto. Variações da mesma família: `"/data" não é um diretório` (o caminho é um arquivo) e `"/data" inacessível: ...` (permissão negada no diretório pai, erro de I/O).

### 2.3. `MACHINE_ID: gravação em /data/machine_id falhou: ... permission denied` (113) ou `AGENT_UUID: gravação em /data/agent_uuid falhou: ...` (106)
O processo não consegue criar arquivos em `$DATADIR`. O 113 aparece primeiro quando nenhum dos dois valores vem da env, porque o `MACHINE_ID` é resolvido antes.
- **Container nonroot com volume do root:** o diretório que `VOLUME ["/data"]` cria numa imagem sem `/data` pertence ao root, e o volume nomeado herda esse dono. Crie `/data` na imagem com o dono do usuário do processo antes do `VOLUME` (veja `examples/docker/Dockerfile`).
- **Bind mount** (`-v /srv/meu-servico:/data`): o diretório do host precisa pertencer ao UID do container (`chown 65532:65532 /srv/meu-servico` para distroless nonroot).
- **Kubernetes:** defina `securityContext.fsGroup` no Pod (ex.: `65532`).
- **Volume read-only** (`readOnly: true`, `:ro`): a leitura de arquivos já existentes funciona; a geração não. Pré-popule `machine_id` e `agent_uuid` ou defina `MACHINE_ID` e `AGENT_UUID`.
- **`no space left on device`:** libere espaço ou inodes no volume.

### 2.4. `MACHINE_ID: "xyz" não casa com ^[0-9a-f]{32}$` (102)
- **Causa:** a env `MACHINE_ID` existe, mas, depois de remover hífens e converter para minúsculas, não tem exatamente 32 hexadecimais.
- **Correção:** 32 caracteres `0-9a-f`. Um UUID qualquer serve (`uuidgen | tr -d -`). Para não fixar, remova a env: a biblioteca usa `/etc/machine-id` ou gera um.

### 2.5. `AGENT_UUID: "a0a2d6e8-b95e-4562-b98b-5283d7c66e1f" não é um UUIDv7 canônico` (107)
- **Causa:** a env `AGENT_UUID` não é um UUID **versão 7** com hífens. O caso mais comum é um UUIDv4 gerado com `uuidgen`: o primeiro dígito do terceiro grupo é `4`, não `7`.
- **Correção:** use o `agent_uuid` que a biblioteca gerou num `DATADIR`, gere um UUIDv7 (Python 3.14+: `python3 -c 'import uuid; print(uuid.uuid7())'`), ou remova a env para que a biblioteca gere e persista o valor.

### 2.6. `AGENT_NAME: "meu servico" não casa com ^[a-z0-9._-]+$ (máx. 64 caracteres)` (104) e `WORKSPACE: "prod_us" não casa com ^[a-z0-9.-]+$ (máx. 64 caracteres)` (111)
- **Causa:** caractere fora do conjunto permitido, mais de 64 caracteres, ou o valor é `.`/`..`. Maiúsculas não são problema (são convertidas). Diferença que mais confunde: `AGENT_NAME` aceita `_`, `WORKSPACE` não.
- **Se o valor veio de arquivo**, a mensagem é `conteúdo de /data/agent_name (N bytes, hash ...) não casa com ...` — o conteúdo não é reproduzido; inspecione o arquivo.
- **Se veio de `argv[0]`** (`"x+y" (de argv[0]) não casa com ...`), renomeie o binário ou defina `AGENT_NAME`.
- **Correção:** `AGENT_NAME=pagamentos-api`, `WORKSPACE=prod-us-east`.

### 2.7. `HOSTNAME: "my_host" não casa com ^[a-z0-9.-]+$ nem com as regras de rótulo da RFC 1123` (109)
- **Causa:** o hostname da máquina ou a env `HOSTNAME` tem `_`, espaço, ponto final (`host.example.com.`), rótulo vazio ou com mais de 63 caracteres, ou hífen na ponta de um rótulo. Acontece em estações de desenvolvimento e VMs com nomes livres.
- **Correção:** defina `HOSTNAME` com um nome válido (`HOSTNAME=dev-maria`). Em Docker, `--hostname`.

### 2.8. `DATADIR: leitura de /data/agent_name falhou: fonte de identidade inválida: /data/agent_name é um link simbólico` (100)
- **Causa:** um arquivo dentro de `$DATADIR` é link simbólico, FIFO, dispositivo ou tem mais de 4 KiB. A recusa impede que um co-inquilino do volume faça o processo ler um arquivo arbitrário do host.
- **Correção:** substitua o link por um arquivo comum com o valor. O **próprio** `$DATADIR` pode ser um link; só o conteúdo dele não.

### 2.9. `MACHINE_ID_FILE: ...` (100)
- **Mensagens:** `"etc/machine-id" é relativo ao diretório de trabalho; use um caminho absoluto`, `"/host/machine-id" inacessível: stat /host/machine-id: permission denied`, `leitura de "/host/machine-id" falhou: permission denied`, `"/host" não é um arquivo comum utilizável (...)`.
- **Causa:** o caminho que o operador indicou explicitamente não pôde ser usado. Um caminho **inexistente** não é erro: a biblioteca registra em debug e usa `/etc/machine-id`.
- **Correção:** caminho absoluto de um arquivo comum legível, com até 4 KiB.

### 2.10. `geral: Initialize() chamado mais de uma vez` (112)
- **Causa:** duas chamadas no mesmo processo — `init()` e `main()`, uma biblioteca interna que também inicializa, um handler de reload, ou `Initialize()` dentro de cada `TestXxx`.
- **Correção:** uma única chamada, no `main()` do binário; em testes, uma no `TestMain` (seção 4 do `SKILL.md`).

### 2.11. Boot demora até 10 segundos
- **Causa:** `$DATADIR/machine_id` ou `agent_uuid` está **vazio** e foi modificado há menos de 10 s. A biblioteca espera essa janela porque pode ser o arquivo que um processo irmão acabou de publicar num volume de rede (NFS). Um arquivo vazio mais antigo é regenerado sem espera.
- **Correção:** normalmente nenhuma. Se o arquivo vazio vem de um script de provisionamento (`touch`), remova esse passo.

---

## 3. Identidade inesperada (sem falha)

| Sintoma | Causa provável | Correção |
| :--- | :--- | :--- |
| `AgentUUID`/`MachineID` mudam a cada deploy ou reinício | `$DATADIR` não persiste: `emptyDir`, volume anônimo, `docker run --rm` sem volume nomeado. Não há aviso, porque o arquivo está ausente, não corrompido. | Volume nomeado (Docker) ou PVC por instância (Kubernetes). |
| `Hostname` muda a cada recriação | Docker usa o ID do container como hostname; num Deployment o nome do Pod muda a cada rollout. | `--hostname` no Docker; StatefulSet no Kubernetes. |
| Todas as réplicas com o mesmo `AgentUUID` | Réplicas montando o mesmo volume gravável (um PVC `ReadWriteMany` num Deployment, um diretório NFS comum). | Um volume por réplica (`volumeClaimTemplates`). |
| App e sidecar com o mesmo `AgentName` | `AGENT_NAME` ausente e as duas imagens com o mesmo nome de binário (ex.: `/app`). | `AGENT_NAME` em cada container. |
| `AgentName` é `main`, `app` ou o nome do diretório | Fallback `argv[0]` (`go run main.go`, `ENTRYPOINT ["/app"]`, `go run .`). | Defina `AGENT_NAME`. |
| Pods no mesmo nó com `MachineID` diferentes | Esperado: em containers o `/etc/machine-id` normalmente não existe e cada volume gera o seu. | Para identificar o nó, monte o `/etc/machine-id` do host (`hostPath`, `readOnly`). |
| Arquivos `.machine_id.tmp*` ou `*.claim` em `$DATADIR` | Restos de uma gravação interrompida por queda do processo. | Inofensivos; podem ser apagados com o serviço parado. |

---

## 4. Avisos operacionais (`lib-loghub-ident: aviso:`)

```text
lib-loghub-ident: aviso: MACHINE_ID: /data/machine_id tinha conteúdo inválido (16 bytes, hash 8f3a1b0c9e7d4a2f) e será substituído (o aviso seguinte diz se foi regenerado ou restaurado de um registro)
lib-loghub-ident: aviso: MACHINE_ID: /data/machine_id foi REGERADO com valor novo (32 bytes, hash 5d41402abc4b2a76); a identidade desta máquina muda a partir de agora
```

### O que significa
O arquivo de identidade no volume estava corrompido (truncado, lixo ou 0 bytes). A biblioteca descartou o conteúdo e o serviço subiu com uma identidade nova. O aviso sai em todo boot que encontrar o problema, com ou sem `LOGHUB_IDENT_DEBUG`.

Se a segunda linha disser `foi RESTAURADO a partir do registro de regeneração /data/.machine_id.regen`, outro processo já tinha regenerado o valor e ele foi reaproveitado. O registro também pode vir de uma recuperação anterior: quem **esvazia** o arquivo para forçar uma identidade nova recebe de volta o valor do registro. Para uma identidade realmente nova, **apague** o arquivo com o serviço parado; a criação do zero descarta o registro.

### Como auditar
1. O hash é **FNV-1a 64-bit** dos bytes inválidos: compare com o mesmo arquivo em backups do volume sem expor o conteúdo no log.
2. Procure a causa da corrupção: *hard reboot*, `kill -9` durante provisionamento, problemas no storage (NFS/SAN), alguém editando o arquivo à mão.
3. Atualize no servidor Loghub o mapeamento da identidade antiga para a nova.

---

## 5. Volumes compartilhados

- **App e sidecar no mesmo Pod:** os dois montam o mesmo `/data`. O primeiro a subir cria a identidade; o outro adota o mesmo arquivo, mesmo que suba no mesmo instante. Os dois reportam o mesmo `MachineID()` e `AgentUUID()` — a instância é o Pod — e `AGENT_NAME` os distingue. Se cada container precisar do seu próprio `AgentUUID`, defina `AGENT_UUID` em cada um ou dê a cada um o seu `DATADIR`.
- **Réplicas independentes:** **cada réplica precisa do seu volume.** Com um volume gravável comum, todas compartilham a identidade e aparecem no servidor Loghub como um único agente. Use `volumeClaimTemplates` num StatefulSet (`examples/kubernetes/statefulset.yaml`).
