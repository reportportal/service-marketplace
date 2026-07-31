package storage

import (
	"fmt"
	"path"
	"strings"
)

const (
	PathIndex          = "index.json"
	PathAuthorizedKeys = "authorized_keys.json"
)

func PluginStatePath(pluginID string) string {
	return path.Join("plugins", pluginID, "plugin.json")
}

func VersionManifestPath(pluginID, version string) string {
	return path.Join("plugins", pluginID, "versions", version, "manifest.json")
}

func VersionArtifactPath(pluginID, version string) string {
	return path.Join("plugins", pluginID, "versions", version, fmt.Sprintf("%s-%s.jar", pluginID, version))
}

func VersionChangelogPath(pluginID, version string) string {
	return path.Join("plugins", pluginID, "versions", version, "CHANGELOG.md")
}

func VersionScreenshotPath(pluginID, version, filename string) string {
	return path.Join("plugins", pluginID, "versions", version, "screenshots", filename)
}

func VersionPrefix(pluginID, version string) string {
	return path.Join("plugins", pluginID, "versions", version) + "/"
}

func PluginPrefix(pluginID string) string {
	return path.Join("plugins", pluginID) + "/"
}

func CDNPath(objectPath string) string {
	return strings.TrimPrefix(objectPath, "/")
}
