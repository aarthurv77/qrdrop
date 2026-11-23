//go:build windows

// Este arquivo só é compilado no Windows (a linha //go:build lá em cima). Nos
// outros sistemas vale console_other.go, com as mesmas funções.

package main

import (
	"bufio"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// Funções da API do Windows (kernel32.dll) que o pacote syscall não traz prontas.
var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	getConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	setConsoleMode        = kernel32.NewProc("SetConsoleMode")
)

// enableVirtualTerminalProcessing é o ENABLE_VIRTUAL_TERMINAL_PROCESSING da
// documentação da Microsoft: com ele o console entende cores ANSI.
const enableVirtualTerminalProcessing = 0x0004

// waitIfOwnConsole segura a janela aberta quando o qrdrop.exe foi aberto com
// dois cliques. Nesse caso o Windows cria uma janela de console só para ele,
// que fecha junto com o programa, e a mensagem de erro (ou o resumo) some
// antes de dar para ler. Num terminal já aberto (cmd, PowerShell) o próprio
// terminal também está ligado ao console: a lista tem 2 ou mais processos e
// não esperamos nada.
func waitIfOwnConsole() {
	var pids [2]uint32
	n, _, _ := getConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n != 1 { // 0 = sem console; 2 ou mais = aberto de um terminal
		return
	}
	fmt.Print("\nAperte Enter para fechar esta janela.")
	bufio.NewReader(os.Stdin).ReadString('\n')
}

// enableANSI liga as cores ANSI no console (Windows 10 em diante). Devolve
// false se a saída não é um console (foi redirecionada para um arquivo) ou se
// o Windows recusou; aí o QR sai sem cores.
func enableANSI() bool {
	h := syscall.Handle(os.Stdout.Fd())
	var mode uint32
	if syscall.GetConsoleMode(h, &mode) != nil {
		return false
	}
	ok, _, _ := setConsoleMode.Call(uintptr(h), uintptr(mode|enableVirtualTerminalProcessing))
	return ok != 0
}
