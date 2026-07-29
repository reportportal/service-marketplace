package com.epam.reportportal.marketplace.util;

import java.util.regex.Pattern;

/**
 * Plugin id and version shapes from the OpenAPI contract, plus path-safety guards so a value that
 * matches the documented pattern still cannot escape a storage key segment (e.g. {@code 1.0.0-../x}).
 */
public final class PluginIdentifiers {

  /**
   * OpenAPI {@code PluginId}: lowercase alphanumerics with internal hyphens, 2–64 chars.
   */
  public static final String ID_REGEX = "^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$";

  /**
   * OpenAPI {@code Version}: semver core with optional pre-release / build metadata.
   */
  public static final String VERSION_REGEX =
      "^(0|[1-9]\\d*)\\.(0|[1-9]\\d*)\\.(0|[1-9]\\d*)(?:-[\\w.-]+)?(?:\\+[\\w.-]+)?$";

  private static final Pattern ID_PATTERN = Pattern.compile(ID_REGEX);
  private static final Pattern VERSION_PATTERN = Pattern.compile(VERSION_REGEX);

  private PluginIdentifiers() {}

  public static boolean isValidId(String pluginId) {
    return pluginId != null
        && ID_PATTERN.matcher(pluginId).matches()
        && isPathSafeSegment(pluginId);
  }

  public static boolean isValidVersion(String version) {
    return version != null
        && VERSION_PATTERN.matcher(version).matches()
        && isPathSafeSegment(version);
  }

  /**
   * Rejects path separators and {@code ..} so the value remains a single object-store key segment.
   */
  public static boolean isPathSafeSegment(String value) {
    return !value.isEmpty()
        && value.indexOf('/') < 0
        && value.indexOf('\\') < 0
        && !value.contains("..");
  }

  public static String idRequirement() {
    return "must match " + ID_REGEX + " and must not contain path separators or '..'";
  }

  public static String versionRequirement() {
    return "must be a semver version matching " + VERSION_REGEX
        + " and must not contain path separators or '..'";
  }
}
