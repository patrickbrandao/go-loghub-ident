# Publicar uma versão

Pré-requisitos: árvore limpa em `main`, CI verde e `go test -race ./...` local.

Tags de módulo Go são, na prática, imutáveis: depois que `proxy.golang.org` e
`sum.golang.org` registram `vX.Y.Z`, mover ou apagar a tag quebra o `go get` de
quem já a usa (checksum mismatch). Erro em uma versão se corrige publicando a
PRÓXIMA versão — nunca com `git tag -f`, `git push -f` ou `--delete`.

```sh
# 1. Confira as versões já publicadas e escolha a próxima (SemVer)
git fetch --tags && git tag -l 'v*'

# 2. Atualize a versão citada em README.md e docs/DEPLOY-FAST.md e faça o commit

# 3. Tag ANOTADA no commit de main que passou na CI
git tag -a v0.3.0 -m "v0.3.0 — <resumo das mudanças>"
git push origin v0.3.0

# 4. Release no GitHub a partir da tag existente. --verify-tag impede que o gh
#    crie a tag sozinho a partir de main se ela não existir.
gh release create v0.3.0 --verify-tag --title "v0.3.0" --generate-notes
```

Para anotações pessoais de desenvolvimento ou rascunhos de comandos locais,
use o diretório `notes/` (ignorado no `.gitignore`).
