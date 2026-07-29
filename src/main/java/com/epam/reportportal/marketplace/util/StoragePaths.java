package com.epam.reportportal.marketplace.util;

public final class StoragePaths {

  public static final String INDEX = "index.json";
  public static final String AUTH_KEYS = "auth/authorized_keys.json";
  private static final String MANIFEST_NAME = "marketplace-manifest.json";

  private StoragePaths() {}

  public static String pluginJson(String pluginId) {
    return "plugins/" + pluginId + "/plugin.json";
  }

  public static String versionDir(String pluginId, String version) {
    return "plugins/" + pluginId + "/versions/" + version;
  }

  public static String manifestPath(String pluginId, String version) {
    return versionDir(pluginId, version) + "/manifest.json";
  }

  public static String jarPath(String pluginId, String version) {
    return versionDir(pluginId, version) + "/" + pluginId + "-" + version + ".jar";
  }

  public static String changelogPath(String pluginId, String version) {
    return versionDir(pluginId, version) + "/CHANGELOG.md";
  }

  public static String assetsPath(String pluginId, String version) {
    return versionDir(pluginId, version) + "/assets.json";
  }

  public static String advisoryPath(String pluginId, String version) {
    return versionDir(pluginId, version) + "/advisory.json";
  }

  public static String screenshotPath(String pluginId, String version, String filename) {
    return versionDir(pluginId, version) + "/screenshots/" + filename;
  }

  public static String pluginsPrefix() {
    return "plugins/";
  }

  public static String manifestEntryName() {
    return MANIFEST_NAME;
  }
}
