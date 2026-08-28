// Pacote tests contém a suíte de testes de integração da biblioteca
// go-loghub-ident: exercita a API pública (Initialize e os seis getters)
// contra o sistema operacional REAL, sem o fakeSystem.
//
// Estes arquivos ficam FORA da pasta principal de propósito: a raiz do
// repositório guarda apenas o que é útil em produção. Aqui a biblioteca é
// importada pelo seu caminho de módulo, exatamente como um projeto externo
// faria — o que cobre justamente a camada que os testes unitários da raiz
// não alcançam (Initialize e osSystem, ambos com 0% de cobertura).
//
// # Como os testes funcionam
//
// Initialize() encerra o processo com os.Exit em qualquer falha, então não
// pode ser chamada dentro do processo de teste. A suíte usa o padrão de
// subprocesso: TestMain re-executa o próprio binário de teste com a variável
// LOGHUB_IDENT_HELPER definida; nesse modo o binário chama Initialize(),
// imprime os seis valores em stdout e termina. Os testes inspecionam o código
// de saída e a saída padrão.
//
// # Testes de bug
//
// Os arquivos bugs_test.go e bugs2_test.go guardam os testes de regressão dos
// defeitos catalogados em tasks/REPORT-01.md, REPORT-02.md e REPORT-03.md, um
// TestFix_BUGxx por defeito corrigido.
//
// Eles nasceram como pares TestChar_/TestFix_: o TestChar_ afirmava o
// comportamento defeituoso — passava enquanto o bug existisse e falhava com
// "BUG-xx parece corrigido" no dia da correção — e o TestFix_, pulado por
// padrão, afirmava o comportamento esperado depois dela. Com os defeitos
// corrigidos, os TestChar_ cumpriram seu papel e saíram; os TestFix_ ficaram,
// agora permanentes, encarregados de impedir que qualquer um dos bugs volte.
package tests
