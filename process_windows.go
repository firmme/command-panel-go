//go:build windows

package main

import (
	"fmt"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Each shell and its descendants belong to one job. The shell starts suspended
// so it cannot spawn an untracked child before assignment.
type processJob struct {
	mu     sync.Mutex
	handle windows.Handle
	root   uint32 // shell PID, used to sweep children that escaped the job
}

func newProcessJob() (*processJob, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	return &processJob{handle: h}, nil
}

func (j *processJob) assignAndResume(pid uint32) error {
	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(p)
	if err := windows.AssignProcessToJobObject(j.handle, p); err != nil {
		return err
	}
	j.mu.Lock()
	j.root = pid
	j.mu.Unlock()
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		_, err = windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		return err
	}
	return fmt.Errorf("cannot find suspended process thread: %w", err)
}

func (j *processJob) terminate() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle == 0 {
		return nil
	}
	if err := windows.TerminateJobObject(j.handle, 1); err != nil {
		return err
	}
	// TerminateJobObject kills the job's own members. PowerShell 7 gives its
	// children a nested job of their own, so they leave this job and survive;
	// sweep the tree until a round finds nothing left to kill.
	for round := 0; round < 16 && j.root != 0; round++ {
		if !j.killDescendantsLocked(j.root) {
			break
		}
	}
	return nil
}

// killDescendantsLocked snapshots the process table, kills every descendant of
// root that is still running, and reports whether it found any.
func (j *processJob) killDescendantsLocked(root uint32) bool {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snapshot)
	parentOf := make(map[uint32]uint32)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		parentOf[entry.ProcessID] = entry.ParentProcessID
	}
	killed := false
	for pid := range parentOf {
		if pid == root || !isDescendant(pid, root, parentOf) {
			continue
		}
		process, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
		if err != nil {
			continue
		}
		if err := windows.TerminateProcess(process, 1); err == nil {
			killed = true
		}
		windows.CloseHandle(process)
	}
	return killed
}

func isDescendant(pid, root uint32, parentOf map[uint32]uint32) bool {
	for depth := 0; depth < 32; depth++ {
		parent, ok := parentOf[pid]
		if !ok || parent == 0 || parent == pid {
			return false
		}
		if parent == root {
			return true
		}
		pid = parent
	}
	return false
}

func (j *processJob) wait() error {
	// JOBOBJECT_BASIC_ACCOUNTING_INFORMATION, shared by Windows architectures.
	var accounting struct {
		UserTime, KernelTime, PeriodUserTime, PeriodKernelTime           int64
		PageFaults, TotalProcesses, ActiveProcesses, TerminatedProcesses uint32
	}
	for {
		j.mu.Lock()
		err := windows.QueryInformationJobObject(j.handle, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil)
		j.mu.Unlock()
		if err != nil {
			return err
		}
		if accounting.ActiveProcesses == 0 {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (j *processJob) close() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle != 0 {
		windows.CloseHandle(j.handle)
		j.handle = 0
	}
}
