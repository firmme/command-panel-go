package main

import "syscall"

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	procSetProcessWorking = kernel32.NewProc("SetProcessWorkingSetSize")
	procGetCurrentProcess = kernel32.NewProc("GetCurrentProcess")
)

// trimWorkingSet tells Windows to page out this process's memory.
// Reduces physical RAM usage; brief page-in delay on next show.
func trimWorkingSet() {
	h, _, _ := procGetCurrentProcess.Call()
	// (SIZE_T)-1 for both = flush to minimum
	procSetProcessWorking.Call(h, ^uintptr(0), ^uintptr(0))
}
