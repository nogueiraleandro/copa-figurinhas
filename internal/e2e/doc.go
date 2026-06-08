// Package e2e contém testes de navegador (Chrome headless via chromedp) que
// exercitam a interface real: JavaScript do cliente, mascote/lottie e o ranking
// ao vivo do telão via SSE.
//
// Os testes ficam atrás da build tag `e2e`, então o `go test ./...` normal não
// os compila nem exige Chrome. Este arquivo (sem tag) existe apenas para que o
// pacote tenha sempre um arquivo Go compilável — caso contrário `go test ./...`
// falharia com "build constraints exclude all Go files".
//
// Para rodar os testes de navegador:
//
//	go test ./internal/e2e -tags e2e -v
package e2e
