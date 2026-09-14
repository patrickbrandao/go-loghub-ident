# 04 — Persistência, Concorrência e Filesystem Seguro

> **Status:** Canônico e Normativo  
> **Escopo:** Layout de arquivos em `$DATADIR`, garantias de gravação durável (`fsync`), criação atômica exclusiva (`CreateExclusive`), Plano B com trava `.claim`, estabilização em NFS (`readSettled`), protocolo de arbitragem de corrupção (`.regen`), defesas contra FIFO/symlinks e teto de 4 KiB.

---

## 1. Layout de Arquivos em `$DATADIR`

O diretório persistente de dados armazena os seguintes arquivos gerenciados e monitorados pela biblioteca:

```text
$DATADIR/
├── machine_id           # 32 hexadecimais (0644) — gerado ou lido
├── agent_uuid           # UUIDv7 canônico (0644) — gerado ou lido
├── agent_name           # Nome do agente (opcional, somente leitura pela lib)
├── workspace            # Nome do workspace (opcional, somente leitura pela lib)
├── .machine_id.regen    # Registro oculto de arbitragem de regeneração
├── .agent_uuid.regen    # Registro oculto de arbitragem de regeneração
└── .<name>.claim        # Arquivo de trava temporária para filesystems sem hard link
```

### Regras de Permissão e Umask
- Todo arquivo persistido pela biblioteca recebe permissão explícita `0644` (`-rw-r--r--`).
- **Defesa contra Umask Restritivo (`BUG-05`):** Ambientes Docker e Kubernetes corporativos frequentemente aplicam `umask 0077` ou `0027`. Uma chamada padrão a `os.Create` ou `os.WriteFile` resultaria em arquivos `0600`, impedindo que sidecars rodando com outro UID pudessem ler a identidade. A biblioteca executa `fchmod(0644)` explícito no descritor de arquivo antes de publicá-lo.

---

## 2. Gravação Durável com `fsync` Duplo

Para impedir corrupção por queda abrupta de energia (*crash*, `kill -9` ou reinício de host), a biblioteca implementa `writeTemp` com sincronização completa de metadados e blocos (**`BUG-17`**):

1. **Criação do Arquivo Temporário:** Cria um arquivo temporário no **mesmo diretório** do destino (`os.CreateTemp(dir, "."+<arquivo>+".tmp")`, ou seja, `.machine_id.tmp<aleatório>`), assegurando que temporário e destino residam no mesmo filesystem.
2. **Escrita do Conteúdo:** Escreve os bytes brutos da identidade sanitizada.
3. **Imposição de Permissões:** Executa `f.Chmod(perm)` diretamente sobre o descritor aberto.
4. **Fsync do Arquivo:** Invoca `f.Sync()` antes de fechar o arquivo, descarregando as páginas de cache do kernel para o disco físico.
5. **Fechamento Seguro:** Executa `f.Close()`.
6. **Fsync do Diretório:** Após publicar o arquivo (seja por `os.Link` ou `os.Rename`), a biblioteca abre o diretório pai em modo read-only e executa `dir.Sync()`. Isso garante a persistência dos metadados da entrada de diretório na tabela do filesystem.

---

## 3. Criação Atômica Exclusiva (`CreateExclusive`)

Quando múltiplos processos irmãos inicializam simultaneamente competindo pelo mesmo volume (ex.: container principal e sidecars em um mesmo Pod Kubernetes), a criação da identidade a frio utiliza o filesystem como árbitro de exclusão mútua (**`BUG-04`**):

```mermaid
flowchart TD
    Start["Início da Criação"] --> WT["Gravar temporário com fsync duplo"]
    WT --> Link["Tentar os.Link(tmp, target)"]
    Link -->|Sucesso| Win["Vencedor! Remove tmp, faz syncDir e conclui"]
    Link -->|ErrExist| Loser["Perdedor! Remove tmp e aguarda readSettled()"]
    Link -->|Erro de Hard Link<br>(EXDEV, ENOTSUP, Windows)| PlanB["Acionar Plano B: createExclusiveDirect via .claim"]
```

### 3.1. Mecanismo Primário: `os.Link` (Hard Link)
- A biblioteca tenta criar um link rígido do arquivo temporário apontando para o nome definitivo (`machine_id` ou `agent_uuid`).
- A chamada de sistema `link()` é atômica no nível do kernel:
  - **Vencedor (`err == nil`):** O arquivo foi criado com sucesso. O temporário é removido e o diretório pai recebe `fsync`. Retorna `(true, nil)`.
  - **Perdedor (`os.IsExist(err)`):** Outro processo concluiu a criação milissegundos antes. O processo remove seu temporário, entra em [`readSettled`](#4-estabilização-em-nfs-e-leitura-estabilizada-readsettled) e adota o valor gravado pelo vencedor. Retorna `(false, nil)`.

### 3.2. Mecanismo de Plano B: Trava `.claim` com TTL (`createExclusiveDirect`)
Em filesystems onde hard links não são suportados (como montagens CIFS, certos compartilhamentos virtuais ou Windows):
1. A biblioteca detecta o erro de suporte a hard link e comuta para `createExclusiveDirect`.
2. Tenta criar um arquivo de trava exclusivo: `$DATADIR/.<arquivo>.claim`.
3. O `.claim` é criado vazio, com `O_EXCL`: o que importa é a sua existência e o seu `ModTime`.
4. **Proteção contra Deadlock (TTL de 10s):** Se o arquivo `.claim` já existir, a biblioteca verifica seu tempo de modificação (`ModTime`). Se for mais antigo que 10 segundos (sinal de processo morto antes de concluir), a trava expirada é removida e a criação prossegue.
5. O vencedor publica o arquivo final via `os.Rename(tmp, target)` de forma atômica.

---

## 4. Estabilização em NFS e Leitura Estabilizada (`readSettled`)

Em volumes de rede (como NFS ou EFS) com latência de atributos e consistência fraca (*close-to-open consistency*), um processo perdedor da corrida pode abrir o arquivo recém-criado pelo vencedor antes que os bytes tenham sido propagados no cache de atributos do cliente NFS, observando um arquivo vazio de 0 bytes (**`BUG-20`**).

Para prevenir declarações falsas de corrupção:
- A função `readSettled` (`resolve.go`) entra em um loop de espera ativa:
  - **Até 500 tentativas** com intervalo de **20 ms** (tolerância máxima total de **10 segundos**).
  - A cada ciclo, tenta ler o arquivo e valida se o conteúdo é não-vazio e atende ao formato canônico.
  - Se estabilizar com sucesso, devolve o valor validado.
  - **A janela é contada a partir do `mtime` do arquivo** (`settleDeadline`, via `system.Stat`), não do início da espera (**`BUG-23`**): um arquivo modificado há mais de 10 s não é artefato de corrida em andamento — é um resíduo (um `touch` de provisionamento, uma cópia interrompida) — e é declarado corrompido na hora, sem esperar; um arquivo recém-modificado espera só o que falta da janela. Se o `Stat` falhar, a janela conta a partir de agora (o comportamento cauteloso).
  - Só quando a janela se esgota sem estabilização é que o arquivo é declarado corrompido.
- Quem chama `readSettled`: o perdedor da corrida de criação (§3), quem lê um registro `.regen` (§5) e `readManaged`, a leitura inicial dos arquivos auto-geridos, quando encontra o arquivo **presente mas vazio** — o único caso em que um conteúdo inválido pode ser transitório. Conteúdo inválido não vazio é declarado corrompido de imediato, e arquivo ausente cai para a geração em silêncio.

---

## 5. Protocolo de Arbitragem de Regeneração (`.regen`)

Se um arquivo persistido (`machine_id` ou `agent_uuid`) contiver dados inválidos (ex.: corrompido por desligamento inadequado ou arquivo truncado de 0 bytes):

```mermaid
sequenceDiagram
    autonumber
    actor P1 as Processo Réplica A
    actor P2 as Processo Réplica B
    participant FS as Filesystem ($DATADIR)

    Note over P1, P2: Ambos detectam arquivo machine_id corrompido
    P1->>P1: Emite aviso de descarte com hash FNV-1a
    P2->>P2: Emite aviso de descarte com hash FNV-1a
    
    P1->>FS: CreateExclusive(".machine_id.regen", novo_id_A)
    Note over P1, FS: P1 vence a corrida do .regen
    P2->>FS: CreateExclusive(".machine_id.regen", novo_id_B)
    Note over P2, FS: P2 perde (ErrExist)
    
    P1->>FS: ReplaceFile("machine_id", novo_id_A)
    P2->>FS: readSettled(".machine_id.regen") -> lê novo_id_A
    P2->>P2: Emite aviso de adoção da identidade do .regen
    P2->>FS: ReplaceFile("machine_id", novo_id_A)
    Note over P1, P2: Ambos convergem para novo_id_A!
```

### Passos do Protocolo:
1. **Emissão de Aviso Operacional:** Todo processo que lê um arquivo corrompido emite imediatamente um aviso compulsório em `stderr` reportando o tamanho em bytes e o **hash FNV-1a 64-bit** do conteúdo corrompido (**`BUG-03`**), sem imprimir o conteúdo em claro para evitar vazamento de segredos.
2. **Disputa pelo Arquivo `.regen`:** A recuperação concorrente não pode usar `CreateExclusive` sobre o arquivo alvo (pois o nome já existe) nem `ReplaceFile` cega (pois cada réplica geraria um ID diferente na memória). A concorrência é arbitrada pelo arquivo `$DATADIR/.<arquivo>.regen`.
3. **Processo Vencedor:**
   - O primeiro a criar `.<arquivo>.regen` via `CreateExclusive` torna-se o líder da recuperação.
   - Escreve sua nova identidade gerada no `.regen`.
   - Substitui o arquivo principal via `ReplaceFile` (arquivo temporário + `os.Rename`).
4. **Processos Perdedores:**
   - Detectam que perderam a criação do `.regen`.
   - Aguardam a estabilização via `readSettled` no arquivo `.regen`.
   - Adotam integralmente o valor vencedor gravado no `.regen`.
   - Emitem aviso operacional em `stderr` informando restauração e adoção da identidade vencedora.
   - Publicam o mesmo valor via `ReplaceFile`.
5. **Retenção do Registro:** O arquivo `.regen` **NUNCA** é apagado ao final da recuperação. Removê-lo reabriria a disputa para processos retardatários. O `.regen` só é removido quando uma identidade é criada **do zero** (arranque a frio sem arquivo preexistente).

---

## 6. Segurança de I/O e Limites de Leitura

### 6.1. Teto Rígido de Leitura de 4 KiB (`maxIdentFileSize`)
- Nenhuma operação de leitura de identidade lê mais de **4096 bytes** (`4 KiB`).
- A leitura prévia verifica `FileInfo.Size() > maxIdentFileSize`. Arquivos gigantes são recusados antes de alocar memória (**`BUG-01`**). Sem este limite, um arquivo de 8 MiB ou `/dev/zero` consumiria até 9 GB de memória em segundos.

### 6.2. Recusa Estrita de Arquivos Não-Comuns
- Antes de abrir qualquer arquivo, a biblioteca verifica `fileInfo.Mode().IsRegular()`.
- **FIFOs / Named Pipes:** Se `$MACHINE_ID_FILE` apontar para um FIFO sem escritor, o `os.Open` convencional travaria a inicialização do processo indefinidamente. A biblioteca detecta o FIFO via `Stat`/`Lstat` e aborta com erro 100 sem abrir o arquivo (**`BUG-01`**).
- **Dispositivos e Sockets:** Diretórios, dispositivos de bloco, dispositivos de caracteres (`/dev/zero`, `/dev/null`) e sockets são terminantemente recusados com erro 100.

### 6.3. Bloqueio de Links Simbólicos (`ReadFileNoFollow`)
- Para ler arquivos em `$DATADIR`, a biblioteca utiliza `ReadFileNoFollow`.
- São três barreiras encadeadas (**`BUG-18`**): `os.Lstat` antes de abrir (um link é recusado sem ser aberto); `O_NOFOLLOW` no próprio `open` em Linux, macOS e BSDs (`system_nofollow.go`), para que o kernel recuse um link plantado entre o `Lstat` e o `open`; e `os.SameFile` depois de abrir, comparando o descritor com o que foi inspecionado. Nos Unix cujo `syscall` não expõe `O_NOFOLLOW` (Solaris, illumos, AIX, DragonFly) e no Windows valem a primeira e a terceira.
- Se o `SameFile` falhar sem ser por link (um irmão publicou o arquivo via `ReplaceFile` naquele instante), a leitura é refeita do zero até 5 vezes (**`BUG-21`**) em vez de abortar.
- Se o arquivo for um link simbólico, a leitura é abortada com **código de saída 100**, impedindo que um invasor no container aponte um symlink para `/etc/shadow` ou chaves de serviço do host.

### 6.4. Regra de Isolamento de Volumes
- **O volume gravável `$DATADIR` NUNCA deve ser compartilhado entre pods, instâncias ou réplicas independentes.**
- Processos que compartilham o mesmo diretório convergem automaticamente para o mesmo `machine_id` e `agent_uuid`. O compartilhamento é intencional e seguro **apenas** entre containers de um mesmo Pod (ex.: container principal e sidecar).
- Nessa topologia o `AgentUUID` identifica a **instância** (o Pod), não cada processo: a distinção entre app e sidecar vem do `AgentName`, e a tupla (`AgentUUID`, `AgentName`, `Hostname`) continua única. Se o servidor Loghub chaveia agentes só por `AgentUUID`, dê a cada container o seu próprio `AGENT_UUID` via env, ou um `DATADIR` próprio.
- A identidade só é estável se o volume sobreviver à recriação do container ou do Pod: volume nomeado no Docker e PVC dedicado (ou `volumeClaimTemplates`) no Kubernetes. Um `emptyDir` ou um volume anônimo é apagado na recriação, e a biblioteca gera identidades novas **sem aviso**, porque não há arquivo corrompido, apenas ausente.
