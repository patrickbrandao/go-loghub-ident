
# Git

```
git init;

# Configurar autor
git config --global user.name patrickbrandao;
git config --global user.email patrickbrandao@gmail.com;
git branch -M main;

# Atualiza a URL do repositorio remoto "origin" com a URL correta e completa
git remote add     origin https://github.com/patrickbrandao/go-loghub-ident.git;
git remote set-url origin https://github.com/patrickbrandao/go-loghub-ident.git;

#git push -u origin main;

# Adicionar projeto no git
git add .;

# Aplicar commit inicial
git commit -m "First-Commit";
git push -u origin main;

# Remover tag 0.1.0, deu pau
# git tag -d 0.1.0 && git push origin --delete 0.1.0
git tag -d v0.1.0 && git push origin --delete v0.1.0;

# Cria a tag (o -f foi usado para sobrescrever a antiga);
git tag -a v0.2.0 -m "Release version 0.2.0";
#git tag -f v0.2.0;

# Envia a tag para o GitHub (o -f forcou a atualizacao remota)
git push -f origin v0.1.0;
git push origin v0.1.0;

gh release create v0.1.0 --title "v0.1.0" --generate-notes;

```

