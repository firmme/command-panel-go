//go:build tools

// Generates the Windows resources used by the native release build.
package main

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"runtime"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
)

//go:embed icon.ico app.manifest
var assets embed.FS

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: windows-resources output.syso")
	}
	arch, ok := map[string]winres.Arch{"amd64": winres.ArchAMD64, "arm64": winres.ArchARM64, "386": winres.ArchI386}[runtime.GOARCH]
	if !ok {
		return fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
	}
	iconData, err := assets.ReadFile("icon.ico")
	if err != nil {
		return err
	}
	icon, err := winres.LoadICO(bytes.NewReader(iconData))
	if err != nil {
		return err
	}
	resources := winres.ResourceSet{}
	if err := resources.SetIcon(winres.RT_ICON, icon); err != nil {
		return err
	}
	manifestData, err := assets.ReadFile("app.manifest")
	if err != nil {
		return err
	}
	manifest, err := winres.AppManifestFromXML(manifestData)
	if err != nil {
		return err
	}
	resources.SetManifest(manifest)
	var info version.Info
	if err := info.UnmarshalJSON([]byte(`{"fixed":{"file_version":"1.0.0"},"info":{"0000":{"ProductVersion":"1.0.0","FileDescription":"命令面板","ProductName":"命令面板"}}}`)); err != nil {
		return err
	}
	resources.SetVersionInfo(info)
	output, err := os.Create(os.Args[1])
	if err != nil {
		return err
	}
	if err := resources.WriteObject(output, arch); err != nil {
		output.Close()
		return err
	}
	return output.Close()
}
