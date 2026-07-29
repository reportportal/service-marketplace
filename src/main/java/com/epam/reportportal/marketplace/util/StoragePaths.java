package com.epam.reportportal.marketplace.util;

import com.epam.reportportal.marketplace.domain.AccessTier;

public final class StoragePaths {

  public static final String INDEX = "index.json";
  public static final String AUTH_KEYS = "auth/authorized_keys.json";
  public static final String PRIVATE_PREFIX = "private/";
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

  public static String premiumJarPath(String pluginId, String version) {
    return PRIVATE_PREFIX + jarPath(pluginId, version);
  }

  public static String jarPath(String pluginId, String version, AccessTier accessTier) {
    return accessTier == AccessTier.PREMIUM
        ? premiumJarPath(pluginId, version)
        : jarPath(pluginId, version);
  }

  public static String privateVersionDir(String pluginId, String version) {
    return PRIVATE_PREFIX + versionDir(pluginId, version);
  }

  public static boolean isPrivate(String key) {
    return key != null && key.startsWith(PRIVATE_PREFIX);
  }

  /**
   * Objects that may be exposed through the public CDN. Everything else — premium artifacts and
   * the licence entitlement store (ADR-011: origin only, never CDN-served) — is denied by default
   * so a new storage prefix cannot become publicly readable by omission.
   */
  public static boolean isPublic(String key) {
    return key != null && (key.equals(INDEX) || key.startsWith(pluginsPrefix()));
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
