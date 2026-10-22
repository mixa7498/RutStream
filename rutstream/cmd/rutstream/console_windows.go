//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// attachConsole подключает GUI-приложение к консоли родителя (cmd/PowerShell).
//
// Сборка идёт с -H windowsgui: при двойном щелчке консоль не появляется,
// а при запуске команды из cmd вывод должен быть виден. AttachConsole решает
// ровно эту задачу; если родительской консоли нет, функция молча выходит.
func attachConsole() {
	const attachParentProcess = ^uint32(0) // 0xFFFFFFFF
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	attach := kernel32.NewProc("AttachConsole")
	if r, _, _ := attach.Call(uintptr(attachParentProcess)); r == 0 {
		return // консоли нет или она уже подключена
	}
	// После AttachConsole стандартные дескрипторы могут указывать в никуда.
	// Переоткрываем их только если они действительно недействительны: если
	// вывод уже перенаправлен в файл или канал (например, `rutstream check | ...`),
	// трогать его нельзя — иначе вывод уйдёт в консоль мимо получателя.
	if !validStdHandle(os.Stdout) {
		if out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
			os.Stdout = out
		}
	}
	if !validStdHandle(os.Stderr) {
		if errOut, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
			os.Stderr = errOut
		}
	}
	if !validStdHandle(os.Stdin) {
		if in, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
			os.Stdin = in
		}
	}
	// UTF-8, чтобы русский текст в check/search не превращался в мусор.
	_ = windows.SetConsoleOutputCP(65001)
	_ = windows.SetConsoleCP(65001)
}

// validStdHandle сообщает, что дескриптор указывает на файл, канал или консоль.
func validStdHandle(f *os.File) bool {
	if f == nil {
		return false
	}
	kind, err := windows.GetFileType(windows.Handle(f.Fd()))
	return err == nil && kind != windows.FILE_TYPE_UNKNOWN
}
