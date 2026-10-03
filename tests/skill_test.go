package tests

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A skill de ../skill é distribuída sozinha: copiada para o diretório de skills
// de outro projeto, sem o resto deste repositório. Estes testes garantem o que
// ela precisa para funcionar assim — frontmatter válido, links que não saem da
// pasta, exemplos que compilam fora daqui — e que a versão documentada é a
// mesma em todo lugar que o checklist de release (docs/08) manda atualizar.

const modulePath = "github.com/patrickbrandao/go-loghub-ident"

// skillDir é a raiz da skill, relativa a este pacote.
var skillDir = filepath.Join("..", "skill")

// skillFrontmatter devolve os campos do frontmatter YAML do SKILL.md. Entende
// o subconjunto que a skill usa: "chave: valor" e blocos dobrados (">", ">-").
func skillFrontmatter(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) == 0 || lines[0] != "---" {
		t.Fatal("SKILL.md não começa com o frontmatter (---)")
	}
	fields := map[string]string{}
	folding := "" // chave do bloco dobrado em andamento
	for _, line := range lines[1:] {
		if line == "---" {
			return fields
		}
		if folding != "" && strings.HasPrefix(line, " ") {
			fields[folding] = strings.TrimSpace(fields[folding] + " " + strings.TrimSpace(line))
			continue
		}
		folding = ""
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("linha de frontmatter não reconhecida: %q", line)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if value == ">" || value == ">-" {
			folding, value = key, ""
		}
		fields[key] = value
	}
	t.Fatal("frontmatter do SKILL.md sem o --- de fechamento")
	return nil
}

// O padrão Agent Skills exige name em [a-z0-9-], até 64 caracteres, sem hífen
// nas pontas nem duplo, e description não vazia com até 1024 caracteres. O
// Claude ainda recusa "anthropic"/"claude" no nome e tags XML na descrição.
func TestSkill_Frontmatter(t *testing.T) {
	fm := skillFrontmatter(t)

	name := fm["name"]
	validName := name != "" && len(name) <= 64 &&
		!strings.HasPrefix(name, "-") && !strings.HasSuffix(name, "-") &&
		!strings.Contains(name, "--") &&
		!strings.Contains(name, "anthropic") && !strings.Contains(name, "claude")
	for i := 0; i < len(name); i++ {
		if c := name[i]; !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			validName = false
		}
	}
	if !validName {
		t.Errorf("name %q fora do padrão de nome de skill", name)
	}

	desc := fm["description"]
	if desc == "" || len(desc) > 1024 {
		t.Errorf("description com %d caracteres (esperado 1 a 1024)", len(desc))
	}
	if strings.ContainsAny(desc, "<>") {
		t.Errorf("description não pode conter tags XML: %q", desc)
	}
}

// mdLink casa o destino de um link Markdown: [texto](destino).
var mdLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)

// Todo link relativo da skill precisa apontar para um arquivo que existe DENTRO
// dela: fora da pasta, ele quebra no projeto que recebe a cópia. Links
// file:// apontam para a máquina de quem escreveu e quebram em qualquer outra.
func TestSkill_LinksStayInside(t *testing.T) {
	root, err := filepath.Abs(skillDir)
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".md" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, m := range mdLink.FindAllStringSubmatch(string(data), -1) {
			target := m[1]
			switch {
			case strings.HasPrefix(target, "file:"):
				t.Errorf("%s: link absoluto da máquina local: %s", rel, target)
				continue
			case strings.HasPrefix(target, "http://"), strings.HasPrefix(target, "https://"),
				strings.HasPrefix(target, "mailto:"), strings.HasPrefix(target, "#"):
				continue
			}
			target, _, _ = strings.Cut(target, "#")
			dest := filepath.Join(filepath.Dir(path), filepath.FromSlash(target))
			if inside, _ := filepath.Rel(root, dest); inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
				t.Errorf("%s: link %s sai da pasta da skill", rel, m[1])
				continue
			}
			if _, err := os.Stat(dest); err != nil {
				t.Errorf("%s: link %s quebrado: %v", rel, m[1], err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// documentedVersion extrai a versão da linha "**Versão documentada:** `vX.Y.Z`"
// do SKILL.md.
func documentedVersion(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile("\\*\\*Versão documentada:\\*\\* `(v[0-9]+\\.[0-9]+\\.[0-9]+)`").FindSubmatch(data)
	if m == nil {
		t.Fatal("SKILL.md sem a linha \"**Versão documentada:** `vX.Y.Z`\"")
	}
	return string(m[1])
}

// requiredVersion devolve a versão de modulePath exigida pela linha
// "require <modulePath> vX.Y.Z" de path, ou "" se não houver.
func requiredVersion(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 3 && fields[0] == "require" && fields[1] == modulePath {
			return fields[2]
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return ""
}

// exampleMods lista os go.mod dos exemplos da skill.
func exampleMods(t *testing.T) []string {
	t.Helper()
	mods, err := filepath.Glob(filepath.Join(skillDir, "examples", "*", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) == 0 {
		t.Fatal("nenhum go.mod em skill/examples")
	}
	return mods
}

// A versão que a skill documenta é a que o README, os "go get" da skill e os
// exemplos usam: o checklist de release (docs/08) manda atualizar todos juntos,
// e uma cópia distribuída com versões misturadas descreve uma coisa e instala
// outra.
func TestSkill_VersionIsConsistent(t *testing.T) {
	want := documentedVersion(t)
	if got := requiredVersion(t, filepath.Join("..", "README.md")); got != want {
		t.Errorf("README.md exige %s %q; SKILL.md documenta %q", modulePath, got, want)
	}
	readme, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for what, re := range map[string]*regexp.Regexp{
		"instalação da skill (V=)":      regexp.MustCompile(`(?m)^V=([0-9]+\.[0-9]+\.[0-9]+)\r?$`),
		"pacote da skill (git archive)": regexp.MustCompile(`(v[0-9]+\.[0-9]+\.[0-9]+):skill`),
	} {
		m := re.FindSubmatch(readme)
		if m == nil {
			t.Errorf("README.md sem a versão na %s", what)
			continue
		}
		if got := "v" + strings.TrimPrefix(string(m[1]), "v"); got != want {
			t.Errorf("README.md usa %s na %s; SKILL.md documenta %q", got, what, want)
		}
	}
	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	gets := regexp.MustCompile(regexp.QuoteMeta(modulePath)+`@(v[0-9]+\.[0-9]+\.[0-9]+)`).FindAllSubmatch(data, -1)
	if len(gets) == 0 {
		t.Errorf("SKILL.md sem \"go get %s@%s\"", modulePath, want)
	}
	for _, m := range gets {
		if got := string(m[1]); got != want {
			t.Errorf("SKILL.md instala %s@%s; documenta %q", modulePath, got, want)
		}
	}
	latest := regexp.MustCompile("Última versão lançada[^|]*\\| `(v[0-9]+\\.[0-9]+\\.[0-9]+)`").FindSubmatch(data)
	if latest == nil {
		t.Error("SKILL.md sem a linha \"Última versão lançada\" na tabela de projeto e versão")
	} else if got := string(latest[1]); got != want {
		t.Errorf("SKILL.md anuncia %s como última versão lançada; documenta %q", got, want)
	}
	for _, mod := range exampleMods(t) {
		rel, _ := filepath.Rel(skillDir, mod)
		if got := requiredVersion(t, mod); got != want {
			t.Errorf("%s exige %s %q; SKILL.md documenta %q", rel, modulePath, got, want)
		}
	}
}

// O go.mod de cada exemplo é o de um consumidor, sem replace: um replace
// relativo aponta para fora da skill e quebra o build da cópia distribuída — e
// um agente o copiaria para o projeto consumidor. tests/examples_test.go
// compila contra este checkout via go.work.
func TestSkill_ExamplesHaveNoReplace(t *testing.T) {
	for _, mod := range exampleMods(t) {
		rel, _ := filepath.Rel(skillDir, mod)
		data, err := os.ReadFile(mod)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "replace") {
				t.Errorf("%s tem replace: %q", rel, strings.TrimSpace(line))
			}
		}
	}
}
