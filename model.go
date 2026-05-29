package main

import "time"

type Command struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Command    string    `json:"command"`
	CWD        string    `json:"cwd"`
	Note       string    `json:"note"`
	AutoStart  bool      `json:"autoStart"`
	Order      int       `json:"order"`
	Archived   bool      `json:"archived"`
	ArchivedAt string    `json:"archivedAt"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type CommandStore struct {
	Commands []Command `json:"commands"`
}

type RunState struct {
	Cmd       *ManagedCommand `json:"cmd"`
	PID       int             `json:"pid"`
	StartTime time.Time       `json:"startTime"`
	EndTime   time.Time       `json:"endTime"`
	ExitCode  int             `json:"exitCode"`
	Running   bool            `json:"running"`
	Duration  string          `json:"duration"`
	LogPath   string          `json:"logPath"`
	LogSize   int64           `json:"logSize"`
}

type ManagedCommand struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	CWD     string `json:"cwd"`
	Name    string `json:"name"`
}
