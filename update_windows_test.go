//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

func TestGitHubUpdateProvider(t *testing.T) {
	payload := []byte("update bytes")
	sum := sha256.Sum256(payload)
	var releases []githubRelease
	for _, version := range []string{"v1.2.0", "v1.10.0", "v2.0.0-beta.1"} {
		r := githubRelease{Tag: version}
		r.Assets = append(r.Assets, struct {
			ID     int64  `json:"id"`
			Name   string `json:"name"`
			Size   int64  `json:"size"`
			Digest string `json:"digest"`
		}{4, "command-panel-windows-amd64.exe", int64(len(payload)), "sha256:" + hex.EncodeToString(sum[:])})
		releases = append(releases, r)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/"+updateRepository+"/releases" {
			json.NewEncoder(w).Encode(releases)
			return
		}
		if r.URL.Path == "/repos/"+updateRepository+"/releases/assets/4" {
			w.Write(payload)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	p := &githubUpdateProvider{client: server.Client(), baseURL: server.URL}
	req := updater.CheckRequest{CurrentVersion: "1.1.0", Platform: "windows", Arch: "amd64"}
	rel, err := p.Check(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if rel == nil || rel.Version != "1.10.0" {
		t.Fatalf("unexpected release: %+v", rel)
	}
	if !bytes.Equal(rel.Verification.Digest, sum[:]) {
		t.Fatal("digest mismatch")
	}
	var out bytes.Buffer
	if err = p.Download(context.Background(), rel, &out, func(int64, int64) {}); err != nil || !bytes.Equal(out.Bytes(), payload) {
		t.Fatalf("download: %v", err)
	}
	req.CurrentVersion = "1.10.0"
	rel, err = p.Check(context.Background(), req)
	if err != nil || rel != nil {
		t.Fatalf("up-to-date: %+v %v", rel, err)
	}
	req.CurrentVersion = "1.0.0"
	req.Arch = "arm64"
	if _, err = p.Check(context.Background(), req); err == nil {
		t.Fatal("missing architecture was accepted")
	}
	releases[1].Assets[0].Digest = ""
	req.Arch = "amd64"
	if _, err = p.Check(context.Background(), req); err == nil {
		t.Fatal("missing digest was accepted")
	}
}
func TestIgnoreUpdatePersists(t *testing.T) {
	dir := t.TempDir()
	app := &App{config: &Config{filePath: filepath.Join(dir, "commands.json")}}
	if err := app.IgnoreUpdate("v1.2.3"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "update-settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings updateSettings
	if err = json.Unmarshal(data, &settings); err != nil || settings.IgnoredVersion != "1.2.3" {
		t.Fatalf("settings: %s %v", data, err)
	}
	if err = app.IgnoreUpdate("not a version"); err == nil {
		t.Fatal("invalid version accepted")
	}
}
func TestUpdateDownloadRejectsWrongSize(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "too long") }))
	defer s.Close()
	p := &githubUpdateProvider{client: s.Client(), baseURL: s.URL}
	rel := &updater.Release{Artifact: updater.Artifact{Size: 3}, Metadata: map[string]any{"assetID": int64(4)}}
	if err := p.Download(context.Background(), rel, &bytes.Buffer{}, func(int64, int64) {}); err == nil || !strings.Contains(err.Error(), "大小") {
		t.Fatalf("unexpected error: %v", err)
	}
}
