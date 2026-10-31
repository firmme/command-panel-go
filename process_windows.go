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
	return windows.TerminateJobObject(j.handle, 1)
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
