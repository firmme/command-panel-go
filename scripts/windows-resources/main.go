//go:build tools

// Generates the Windows resources used by the native release build.
// Usage: windows-resources output.syso [version]
package main

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
)

//go:embed icon.ico app.manifest
var assets embed.FS

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fmt.Fprintln(os.Stderr, "usage: windows-resources output.syso [version]")
		os.Exit(1)
	}
	appVersion := "1.0.0"
	if len(os.Args) == 3 {
		appVersion = os.Args[2]
	}
	if err := generate(appVersion); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(appVersion string) error {
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
	// Windows shows the string table in the Details tab, and the fixed struct
	// is what scripts read, so set both through the package helpers.
	// The fixed file version takes four numbers; the app version is x.y.z.
	fixedVersion := appVersion
	if parts := strings.Split(fixedVersion, "."); len(parts) == 3 {
		fixedVersion += ".0"
	}
	info.SetFileVersion(fixedVersion)
	info.SetProductVersion(fixedVersion)
	for key, value := range map[string]string{
		version.ProductName:        "命令面板",
		version.FileDescription:    "命令面板",
		version.ProductVersion:     appVersion,
		version.FileVersion:        appVersion,
		version.CompanyName:        "firmme",
		version.OriginalFilename:   filepath.Base(os.Args[1]),
	} {
		if err := info.Set(version.LangNeutral, key, value); err != nil {
			return err
		}
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
