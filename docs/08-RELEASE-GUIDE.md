# 08 — Ciclo de Lançamento e Governança de Versões

> **Status:** Canônico e Normativo  
> **Escopo:** Políticas de versionamento semântico (SemVer), imutabilidade de tags no ecossistema Go, checklist pré-lançamento e automação via GitHub CLI.

---

## 1. Princípio da Imutabilidade de Tags em Go

No ecossistema da linguagem Go, **tags anotadas de versão são estritamente imutáveis**:
- Imediatamente após uma tag `vX.Y.Z` ser publicada no GitHub, os servidores de cache de módulos (`proxy.golang.org`) e o banco de dados criptográfico de somas de verificação (`sum.golang.org`) registram o hash criptográfico exato daquele commit.
- **Consequência Crítica:** Mover uma tag, deletá-la ou forçar regravação (`git tag -f`, `git push -f` ou `--delete`) **quebra definitivamente o `go get`** de qualquer usuário que já tenha baixado aquela versão com erro de *checksum mismatch* fatal.
- **Regra de Ouro:** Qualquer erro, correção ou ajuste descoberto após a publicação de uma tag DEVE ser resolvido publicando a **próxima versão** (ex.: de `v0.3.1` para `v0.3.2`) — NUNCA alterando a tag existente.

---

## 2. Checklist Pré-Lançamento

Antes de criar uma nova tag de versão, execute este checklist obrigatório:

1. [ ] A branch local é `main` e está perfeitamente sincronizada com `origin/main` (`git status` limpo).
2. [ ] Todos os testes unitários e de integração passaram com detecção de corridas:
   ```bash
   go test -race ./...
   ```
3. [ ] Todos os testes de anti-regressão permanente passaram:
   ```bash
   go test ./tests/ -run TestFix -v
   ```
4. [ ] A verificação estática do compilador não acusou advertências:
   ```bash
   go vet ./...
   ```
5. [ ] O número da versão foi atualizado nos arquivos de exemplo e documentação (`README.md`, `skill/SKILL.md`, `skill/examples/basic/go.mod`).
6. [ ] As alterações foram commitadas e enviadas para o repositório remoto.

---

## 3. Passo a Passo de Publicação

### Passo 1: Listar tags existentes e definir a próxima versão (SemVer)
```bash
git fetch --tags
git tag -l 'v*'
```

### Passo 2: Criar uma Tag Anotada no Git
A tag deve ser **anotada** (`-a`) para conter autor, data e mensagem descritiva:
```bash
git tag -a v0.3.2 -m "v0.3.2 — <resumo das mudanças e correções>"
```

### Passo 3: Enviar a Tag para o Repositório Remoto
```bash
git push origin v0.3.2
```

### Passo 4: Publicar o Release no GitHub
Utilize o utilitário `gh` com a flag `--verify-tag` (que impede a criação acidental de tags não existentes):
```bash
gh release create v0.3.2 --verify-tag --title "v0.3.2" --generate-notes
```

---

## 4. Notas Locais de Desenvolvimento

Para anotações pessoais de desenvolvimento, rascunhos de testes manuais ou scripts temporários que não devam ser versionados, utilize a pasta [`tasks/`](file:///Users/patrickbrandao/Projects/loghub/go-loghub-ident/tasks) (já incluída no `.gitignore`).
